package snapshot

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// RollbackResult describes the outcome of a Rollback call. Returned
// so the caller (Phase 7 auto-retry, or `kcpos snap rollback` CLI)
// can report it AND so Phase 6 lesson synthesis can walk the
// archived branch by name.
type RollbackResult struct {
	// RolledBackTo is the event the chain was rewound to. Tip now
	// points here; new events append as children of this event.
	RolledBackTo string

	// ArchivedBranchRef is the ref name preserving the failed branch
	// tip (e.g. "attempt/1"). Phase 6 reads events backward from
	// this ref via WalkFrom to build the lesson summary.
	ArchivedBranchRef string

	// FailedTip is the pre-rollback tip — the head of the archived
	// branch. Equivalent to s.Refs.Get(ArchivedBranchRef) but
	// returned directly so the caller doesn't need a second lookup.
	FailedTip string

	// EventsArchived counts how many events live in the failed
	// branch (from the event AFTER RolledBackTo up to and including
	// FailedTip). Useful for logging "rolled back, archiving 47
	// events for review".
	EventsArchived int
}

// Rollback rewinds the chain to `target` and archives the events
// between `target+1` and the current tip under a named ref. Workdir
// state is restored to `target` (Phase 5 mode: via tmp-dir swap).
//
// The branchName argument is appended under "attempt/" so refs like
// "attempt/1", "attempt/post-gate-fail" land in a predictable
// namespace. Pass "" to auto-allocate the next "attempt/<N>" slot.
//
// Rollback is the only operation that produces branched event
// histories. After Rollback:
//   - tip ref points at `target`
//   - attempt/<branchName> ref points at the old tip
//   - the chain has two children at `target`: the archived branch's
//     first event AND (next append) the new branch's first event
//   - Walk (linear) is unreliable on the now-branched history;
//     callers should use WalkFrom from a specific tip ref.
func (s *Snapshotter) Rollback(target, branchName string) (*RollbackResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !validSha(target) {
		return nil, fmt.Errorf("rollback: target %q is not a valid event id", target)
	}
	// Validate target exists and is on the current linear chain
	// (ancestor of tip). Rolling back to an event in some OTHER
	// archived branch would invalidate the tip's ancestry — refuse.
	tip, err := s.Refs.Get(tipRefName)
	if err != nil {
		return nil, fmt.Errorf("rollback: read tip: %w (rollback requires a non-empty chain)", err)
	}
	if tip == target {
		return nil, fmt.Errorf("rollback: target IS the current tip — nothing to roll back")
	}
	ancestry, err := chainAncestry(s.Events, tip)
	if err != nil {
		return nil, fmt.Errorf("rollback: walk tip ancestry: %w", err)
	}
	if !contains(ancestry, target) {
		return nil, fmt.Errorf("rollback: target %s is not an ancestor of tip %s — refusing to rewind across branches", target[:16], tip[:16])
	}

	// Choose branch ref name. Auto-allocate "attempt/<N>" when not
	// supplied; N = (max existing N) + 1.
	if branchName == "" {
		branchName = autoAttemptName(s.Refs)
	}
	// Validate the user-supplied name here (vs leaving it for
	// RefStore.Set to reject) so the error message names rollback
	// as the rejecter — clearer for the caller than a generic
	// "refstore: name traversal" mid-rollback.
	if strings.Contains(branchName, "..") || strings.HasPrefix(branchName, "/") || strings.Contains(branchName, "\x00") {
		return nil, fmt.Errorf("rollback: branchName %q contains illegal segments (\"..\", leading \"/\", or null byte) — pass a simple identifier like \"gate-fail\" or \"method-use-rule-attempt-3\"", branchName)
	}
	refKey := "attempt/" + branchName

	// Archive the failed tip BEFORE moving tip ref. If anything
	// after this fails, the archive ref still points at the failed
	// branch and the operator can recover.
	if err := s.Refs.Set(refKey, tip); err != nil {
		return nil, fmt.Errorf("rollback: archive branch ref: %w", err)
	}

	// Restore workdir to target's state. Phase 5 strategy: replay
	// into a tmp dir, then swap the tmp's contents into the
	// (cleaned) source workdir. This bypasses Replay's "target
	// must differ from source" guard, which is the right safety
	// rail for ad-hoc replay but explicitly NOT for rollback —
	// rollback's intent IS to overwrite the live workdir.
	if err := s.restoreWorkdirInPlace(target); err != nil {
		// Best-effort recovery: tip ref still points at the failed
		// branch tip; user can fix-forward.
		return nil, fmt.Errorf("rollback: restore workdir: %w (branch %s saved; tip still at %s)", err, refKey, tip[:16])
	}

	// Reset tip to target. After this point, Append creates children
	// of target — the new branch.
	if err := s.Refs.Set(tipRefName, target); err != nil {
		return nil, fmt.Errorf("rollback: reset tip: %w (workdir restored; branch %s saved; tip stale)", err, refKey)
	}

	archivedCount := indexOf(ancestry, target) // count of events strictly between target and tip
	return &RollbackResult{
		RolledBackTo:      target,
		ArchivedBranchRef: refKey,
		FailedTip:         tip,
		EventsArchived:    archivedCount,
	}, nil
}

// restoreWorkdirInPlace replays the chain up to `target` into a temp
// dir, then swaps the temp dir's contents into the source workdir
// (after cleaning the source's watched roots). This is the ONLY path
// that writes into the source workdir directly — all other Replay
// calls go to a distinct target as the safety rail.
//
// ATOMICITY CAVEAT (Phase 5 scope): the clean → copy sequence is
// NOT atomic. If clean succeeds but copy fails mid-way, the source
// workdir is left in a partial state — some watched roots have been
// wiped, others may have new content from a half-completed copy.
// True atomic dir swap requires renaming an entire directory tree,
// which only works when source and tmp are on the same filesystem
// (os.MkdirTemp picks /tmp by default — often a different fs from
// the project workdir). Document the risk for Phase 5; revisit with
// same-fs-tmp staging or backup-dir strategy in Phase 8 if real
// failures surface. In practice the steps are millisecond-scale and
// the disk-full / permission cases that would fail the copy would
// usually have failed the clean too.
func (s *Snapshotter) restoreWorkdirInPlace(target string) error {
	tmp, err := os.MkdirTemp("", "kcpos-rollback-restore-*")
	if err != nil {
		return fmt.Errorf("mktmp: %w", err)
	}
	defer os.RemoveAll(tmp)

	// Replay (unlocked: caller already holds s.mu, and Replay's
	// Events.List does not acquire the lock).
	if _, err := s.replayUnlocked(ReplayOptions{
		TargetWorkdir: tmp,
		CleanFirst:    false, // tmp is fresh, nothing to clean
		StopAt:        target,
	}); err != nil {
		return fmt.Errorf("replay to tmp: %w", err)
	}

	// Clean source workdir's watched roots.
	if err := cleanTargetWorkdir(s.workdir); err != nil {
		return fmt.Errorf("clean source: %w", err)
	}
	// Copy tmp → source.
	if err := copyTree(tmp, s.workdir); err != nil {
		return fmt.Errorf("copy tmp into source: %w", err)
	}
	return nil
}

// replayUnlocked is the inner Replay body — same logic as Replay but
// doesn't acquire s.mu. Rollback holds the lock for the whole
// rewind operation; calling Replay (which would re-lock) would
// deadlock.
//
// Identical to Replay except for: (a) accepts s.workdir as target
// when explicitly chosen (rollback's exemption from the same-dir
// guard), (b) doesn't lock.
func (s *Snapshotter) replayUnlocked(opts ReplayOptions) (int, error) {
	if opts.TargetWorkdir == "" {
		return 0, fmt.Errorf("replay: TargetWorkdir required")
	}
	// NOTE: the same-dir guard is intentionally skipped here —
	// callers using replayUnlocked have proven they intend to
	// overwrite source state (rollback). Public Replay still
	// enforces the guard.
	if opts.CleanFirst {
		if err := cleanTargetWorkdir(opts.TargetWorkdir); err != nil {
			return 0, fmt.Errorf("replay: clean target: %w", err)
		}
	}
	if err := os.MkdirAll(opts.TargetWorkdir, 0o755); err != nil {
		return 0, fmt.Errorf("replay: mkdir target: %w", err)
	}
	// Use WalkFrom from target so branched histories don't trip us.
	tip := opts.StopAt
	if tip == "" {
		t, err := s.Refs.Get(tipRefName)
		if err != nil {
			return 0, fmt.Errorf("replay: no StopAt and no tip ref: %w", err)
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
		applied++
		return nil
	}); err != nil {
		return applied, err
	}
	return applied, nil
}

// chainAncestry returns the linear ancestry of `tipID`, from tip
// back to genesis (tip first, genesis last). Used by Rollback to
// validate the target is on tip's ancestry path.
func chainAncestry(log *EventLog, tipID string) ([]string, error) {
	var ids []string
	cursor := tipID
	for cursor != "" {
		ev, err := log.Get(cursor)
		if err != nil {
			return nil, err
		}
		ids = append(ids, cursor)
		cursor = ev.ParentID
	}
	return ids, nil
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func indexOf(ss []string, s string) int {
	for i, x := range ss {
		if x == s {
			return i
		}
	}
	return -1
}

// autoAttemptName returns the next free "attempt/<N>" slot — finds
// the highest existing N and returns N+1 as a string. With no prior
// attempts, returns "1".
func autoAttemptName(r *RefStore) string {
	refs, err := r.List()
	if err != nil {
		return "1"
	}
	maxN := 0
	for name := range refs {
		if !strings.HasPrefix(name, "attempt/") {
			continue
		}
		rest := strings.TrimPrefix(name, "attempt/")
		var n int
		if _, err := fmt.Sscanf(rest, "%d", &n); err == nil && n > maxN {
			maxN = n
		}
	}
	return fmt.Sprintf("%d", maxN+1)
}

// copyTree recursively copies the contents of src into dst.
// Directories are mkdir'd, files have their content + mode
// preserved. Used by restoreWorkdirInPlace to swap a replayed tmp
// dir into the live source workdir.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		// Regular file copy.
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer out.Close()
		if _, err := io.Copy(out, in); err != nil {
			return err
		}
		return nil
	})
}
