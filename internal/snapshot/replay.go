package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReplayOptions controls how Replay reconstructs workdir state.
type ReplayOptions struct {
	// TargetWorkdir is where the reconstructed state is materialised.
	// MUST differ from the source workdir (the one events were
	// captured into) to avoid clobbering the live state.
	// If empty, Replay errors.
	TargetWorkdir string

	// CleanFirst removes the contents of TargetWorkdir's watched
	// roots before replay. The replay walk only re-creates state
	// that's covered by SideEffects; pre-existing files outside the
	// event log would otherwise leak through and corrupt the
	// reconstructed state.
	//
	// Set false ONLY when TargetWorkdir is known-empty (test
	// fixtures, scratch dirs) — production replay should keep this
	// true.
	CleanFirst bool

	// StopAt limits replay to events up to AND INCLUDING this event
	// id (inclusive). Empty string means "replay through tip" — the
	// full chain.
	StopAt string
}

// Replay reconstructs workdir state by walking from the chain tip
// backward to genesis, then re-applying every SideEffect in forward
// order. Returns the count of events applied and any error.
//
// Replay does NOT re-invoke the LLM, probes, or external tools —
// recorded outputs are the source of truth. The contract is:
// "apply the recorded side_effects to the target workdir and you
// will land on a workdir state byte-identical to the capture
// moment". Determinism is in the side_effect replay, not in
// re-computing the cause.
//
// StopAt semantics: when set, walk the ancestry of StopAt itself
// (not of the current tip). This means StopAt CAN be in an archived
// branch — replay reconstructs that branch's state without needing
// the live tip to know about it. When StopAt is empty, replay
// follows the current tip ref.
//
// 2026-05-22 — Phase 5 refactor: switched from linear childOf-map
// walk (broken on branched histories — only followed one child of
// any node) to WalkFrom-based ancestry walk. Each tip's ancestry
// is unambiguous; replay to ANY event (including archived branch
// events) now works correctly.
func (s *Snapshotter) Replay(opts ReplayOptions) (int, error) {
	if opts.TargetWorkdir == "" {
		return 0, fmt.Errorf("replay: TargetWorkdir required")
	}
	if opts.TargetWorkdir == s.workdir {
		return 0, fmt.Errorf("replay: TargetWorkdir must differ from source (refusing to clobber live state)")
	}
	if opts.CleanFirst {
		if err := cleanTargetWorkdir(opts.TargetWorkdir); err != nil {
			return 0, fmt.Errorf("replay: clean target: %w", err)
		}
	}
	if err := os.MkdirAll(opts.TargetWorkdir, 0o755); err != nil {
		return 0, fmt.Errorf("replay: mkdir target: %w", err)
	}
	// Determine the tip we walk FROM. StopAt acts as that tip when
	// supplied (so we can target archived branches); otherwise we
	// follow the current chain tip.
	tip := opts.StopAt
	if tip == "" {
		t, err := s.Refs.Get(tipRefName)
		if err != nil {
			if err == ErrRefNotFound {
				return 0, nil // empty chain — replay is a no-op
			}
			return 0, fmt.Errorf("replay: read tip: %w", err)
		}
		tip = t
	}
	applied := 0
	if err := s.Events.WalkFrom(tip, func(ev Event) error {
		if ev.Type == EventTypeToolExec {
			if err := s.replayToolExec(opts.TargetWorkdir, ev); err != nil {
				return fmt.Errorf("replay event %s: %w", ev.ID[:16], err)
			}
		}
		// LLMTurn, OuterTransition, Milestone events have no
		// side_effects — they're records, not mutations.
		applied++
		return nil
	}); err != nil {
		// Surface the user-supplied StopAt (when set) in the error
		// so they can match it against `kcpos snap list` output.
		if opts.StopAt != "" {
			return applied, fmt.Errorf("replay: StopAt %q not found in any event chain (verify with `kcpos snap list` and retry): %w", opts.StopAt, err)
		}
		return applied, err
	}
	return applied, nil
}

// replayToolExec applies the SideEffects attached to one
// ToolExecEvent.
func (s *Snapshotter) replayToolExec(targetWorkdir string, ev Event) error {
	var payload ToolExecEvent
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	for _, se := range payload.SideEffects {
		if err := s.applySideEffect(targetWorkdir, se); err != nil {
			return fmt.Errorf("apply %s: %w", se.Kind, err)
		}
	}
	return nil
}

// applySideEffect dispatches per Kind to the concrete reconstruction
// step. Unknown Kind is a hard error — replay must not silently
// produce a divergent state when the schema evolves.
func (s *Snapshotter) applySideEffect(targetWorkdir string, se SideEffect) error {
	switch se.Kind {
	case SideKindFileWrite:
		var fw FileWrite
		if err := se.Decode(&fw); err != nil {
			return err
		}
		return s.applyFileWrite(targetWorkdir, fw)
	case SideKindFileDelete:
		var fd FileDelete
		if err := se.Decode(&fd); err != nil {
			return err
		}
		return s.applyFileDelete(targetWorkdir, fd)
	case SideKindExecutableReplace,
		SideKindGraphMutation,
		SideKindBundleUpdate,
		SideKindSessionMutation:
		// Phase 3 scope: filesystem only. These kinds are reserved
		// for Phase 5+ (rollback, container interactions); not
		// emitted by current capture code so the absent
		// implementation can't break a real replay.
		return fmt.Errorf("side_effect kind %q not yet implemented in Phase 3 replay", se.Kind)
	default:
		return fmt.Errorf("unknown side_effect kind %q (forward-compat: extend applySideEffect or stop using new kinds with old replay binaries)", se.Kind)
	}
}

func (s *Snapshotter) applyFileWrite(targetWorkdir string, fw FileWrite) error {
	if err := validReplayPath(fw.Path); err != nil {
		return err
	}
	content, err := s.Blobs.Get(fw.ContentSha)
	if err != nil {
		return fmt.Errorf("file_write %s: blob get: %w", fw.Path, err)
	}
	full := filepath.Join(targetWorkdir, fw.Path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("file_write %s: mkdir: %w", fw.Path, err)
	}
	mode := os.FileMode(fw.Mode)
	if mode == 0 {
		mode = 0o644
	}
	if err := os.WriteFile(full, content, mode); err != nil {
		return fmt.Errorf("file_write %s: write: %w", fw.Path, err)
	}
	return nil
}

func (s *Snapshotter) applyFileDelete(targetWorkdir string, fd FileDelete) error {
	if err := validReplayPath(fd.Path); err != nil {
		return err
	}
	full := filepath.Join(targetWorkdir, fd.Path)
	err := os.Remove(full)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("file_delete %s: %w", fd.Path, err)
	}
	return nil
}

// validReplayPath rejects paths that would write outside
// TargetWorkdir. Critical security guard: a tampered event payload
// with Path="../../etc/passwd" must not let replay touch anything
// outside the target.
func validReplayPath(p string) error {
	if p == "" {
		return fmt.Errorf("path required")
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("path must be workdir-relative, got absolute %q", p)
	}
	clean := filepath.Clean(p)
	if strings.HasPrefix(clean, "..") || strings.Contains(clean, "/../") {
		return fmt.Errorf("path traversal rejected: %q", p)
	}
	return nil
}

// cleanTargetWorkdir removes the watched subdirectories under
// targetWorkdir (so replay starts from a known-empty state). We
// only clean what we'd otherwise snapshot — leaving truly external
// content (e.g. a probe binary the harness wrote) alone.
//
// Errors during clean are returned (don't silently leak state from a
// prior replay).
func cleanTargetWorkdir(workdir string) error {
	// Resolve which subdirectories / top-level files to remove. We
	// reuse the same rules as the snapshotter so clean ↔ snapshot
	// stay in lockstep.
	entries, err := os.ReadDir(workdir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, ent := range entries {
		name := ent.Name()
		if shouldSkipTopLevel(name) && name != ".kcpos" {
			continue
		}
		fullPath := filepath.Join(workdir, name)
		if ent.IsDir() {
			if name == ".kcpos" {
				// Inside .kcpos clean only typecalc/, never snapshots/.
				typecalcDir := filepath.Join(fullPath, "typecalc")
				if _, err := os.Stat(typecalcDir); err == nil {
					if err := os.RemoveAll(typecalcDir); err != nil {
						return err
					}
				}
				continue
			}
			if !shouldRecurseDir(name) {
				continue
			}
			if err := os.RemoveAll(fullPath); err != nil {
				return err
			}
		} else {
			if !shouldHashTopLevelFile(name) {
				continue
			}
			if err := os.Remove(fullPath); err != nil {
				return err
			}
		}
	}
	return nil
}
