package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Snapshotter is the agent-facing API that wires BlobStore + EventLog
// + RefStore together under a single workdir-relative root and
// maintains the "tip" ref automatically.
//
// Layout under workdir:
//
//	.kcpos/snapshots/
//	├── objects/   ← BlobStore
//	├── events/    ← EventLog
//	└── refs/      ← RefStore  (including refs/tip auto-managed by Append)
//
// All paths are relative to the Snapshotter's workdir; replay to a
// different machine just changes the workdir, the snapshot itself is
// self-contained.
//
// Concurrency: Append is mutex-serialised so concurrent goroutines
// (e.g. parallel tool batches under runBatchConcurrent) can't race
// the tip ref's read-modify-write. Without this lock, two concurrent
// Append calls would both read tip=T, both write events with
// parent=T, and the second's tip Set would overwrite the first's —
// one event becomes an orphan (file exists, no ref reaches it,
// replay walks past it).
//
// PutBlob is naturally concurrent-safe (content-addressed, atomic
// rename, idempotent on equal content). RefStore.Set is per-file
// atomic. Only the cross-store dependency (Events.Append + tip
// advance) needs the lock.
type Snapshotter struct {
	Blobs  *BlobStore
	Events *EventLog
	Refs   *RefStore

	workdir string

	// mu serialises Append's tip read+write. Held over the entire
	// Append body so the (read tip → write event → write tip)
	// sequence is atomic.
	mu sync.Mutex
}

const (
	snapshotsSubdir = ".kcpos/snapshots"
	tipRefName      = "tip"
)

// NewSnapshotter creates a Snapshotter rooted at workdir. The
// snapshots subdirectory is created lazily on first write. If the
// workdir doesn't yet have a snapshots dir, reads return their normal
// not-found errors and writes initialize the layout.
func NewSnapshotter(workdir string) *Snapshotter {
	root := filepath.Join(workdir, snapshotsSubdir)
	return &Snapshotter{
		Blobs:   NewBlobStore(filepath.Join(root, "objects")),
		Events:  NewEventLog(filepath.Join(root, "events")),
		Refs:    NewRefStore(filepath.Join(root, "refs")),
		workdir: workdir,
	}
}

// Workdir reports the workdir the snapshotter was constructed against.
// Useful for replay code that needs to know where to materialize
// restored files.
func (s *Snapshotter) Workdir() string { return s.workdir }

// SnapshotsRoot returns the on-disk root of the snapshot store
// (workdir/.kcpos/snapshots). Phase 4 CLI commands use this when
// printing diagnostics.
func (s *Snapshotter) SnapshotsRoot() string {
	return filepath.Join(s.workdir, snapshotsSubdir)
}

// Append records an event whose parent is the current tip, then
// advances tip to the new event. Returns the stored event with its
// computed ID.
//
// First call (no tip yet): parent = "", event becomes genesis.
// Idempotency: if the computed id already exists in the log, returns
// the existing event without bumping the tip.
//
// Failure mode notes:
//   - Event write fails → tip unchanged, return error.
//   - Tip set fails after event write → event is in the log but tip
//     points at the prior event. Next Append will re-derive parent
//     from tip; the orphan event is harmless (still inspectable, just
//     not part of the linear chain until manually re-anchored).
func (s *Snapshotter) Append(eventType string, payload interface{}) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	parent, err := s.Refs.Get(tipRefName)
	if err != nil && err != ErrRefNotFound {
		return Event{}, fmt.Errorf("snapshotter: read tip: %w", err)
	}
	ev, err := s.Events.Append(parent, eventType, payload)
	if err != nil {
		return Event{}, err
	}
	if ev.ID == parent {
		// Idempotent reuse — tip already points here, nothing to do.
		return ev, nil
	}
	if err := s.Refs.Set(tipRefName, ev.ID); err != nil {
		return ev, fmt.Errorf("snapshotter: advance tip: %w (event %s written but tip stale)", err, ev.ID[:16])
	}
	return ev, nil
}

// Capture wraps Append with a stderr warning on failure. The agent
// must never crash because snapshotting can't keep up (e.g. disk
// full, permission issue) — but silently swallowing the failure
// hides degradation. Capture is the common pattern at hook sites:
// drop the result, log the error, never propagate.
func (s *Snapshotter) Capture(stage, eventType string, payload interface{}) {
	if _, err := s.Append(eventType, payload); err != nil {
		fmt.Fprintf(os.Stderr, "[snapshot/%s] capture failed: %v\n", stage, err)
	}
}

// TakeWorkdir is a thin pass-through to the top-level
// TakeWorkdirSnapshot helper, scoped to this Snapshotter's workdir.
// Used by tool-call hooks to take pre/post snapshots without
// repeating the workdir lookup.
func (s *Snapshotter) TakeWorkdir() (*WorkdirSnapshot, error) {
	return TakeWorkdirSnapshot(s.workdir)
}

// DiffToSideEffects converts a workdir diff into the SideEffect list
// for a ToolExecEvent. Each Added/Modified path becomes a FileWrite
// side_effect (after storing the content in the blob pool); each
// Deleted path becomes a FileDelete side_effect. Returns nil when
// the diff is empty (read-only tool calls).
//
// The blob pool is content-addressed and idempotent, so re-storing
// the same content across many tool calls is a no-op after the
// first. Total storage stays proportional to unique file versions,
// not call count.
func (s *Snapshotter) DiffToSideEffects(diff *WorkdirDiff) ([]SideEffect, error) {
	if diff.IsEmpty() {
		return nil, nil
	}
	var effects []SideEffect
	// Added + Modified → FileWrite. We must read the current content
	// to store as a blob; the diff carries the sha but not the
	// bytes (cheap to re-read since the file just changed).
	for _, p := range append(append([]string{}, diff.Added...), diff.Modified...) {
		full := filepath.Join(s.workdir, p)
		body, err := os.ReadFile(full)
		if err != nil {
			// Concurrent delete? Honest tools.Execute completed; we
			// missed the chance to capture. Skip rather than fail.
			continue
		}
		sha, err := s.PutBlob(body)
		if err != nil {
			return nil, fmt.Errorf("snapshotter: blob put for %s: %w", p, err)
		}
		info, err := os.Stat(full)
		var mode uint32
		if err == nil {
			mode = uint32(info.Mode().Perm())
		}
		se, err := NewSideEffect(SideKindFileWrite, FileWrite{
			Path:       p,
			ContentSha: sha,
			Mode:       mode,
		})
		if err != nil {
			return nil, err
		}
		effects = append(effects, se)
	}
	for _, p := range diff.Deleted {
		se, err := NewSideEffect(SideKindFileDelete, FileDelete{Path: p})
		if err != nil {
			return nil, err
		}
		effects = append(effects, se)
	}
	return effects, nil
}

// PutBlob is the convenience pass-through; matches Append's idempotent
// semantics. Returns the sha for use in side-effect payloads.
func (s *Snapshotter) PutBlob(content []byte) (string, error) {
	return s.Blobs.Put(content)
}

// AppendFileWrite is the common case: record one file write as a
// tool.exec event with one file.write side-effect. The Tool / Args /
// Result fields describe the originating tool call so forensic
// queries can group "all write_file calls by H_confirm_one" etc.
func (s *Snapshotter) AppendFileWrite(tool string, args interface{}, result, errMsg string, path string, content []byte, mode os.FileMode) (Event, error) {
	sha, err := s.PutBlob(content)
	if err != nil {
		return Event{}, fmt.Errorf("snapshotter: put file blob: %w", err)
	}
	se, err := NewSideEffect(SideKindFileWrite, FileWrite{
		Path:       path,
		ContentSha: sha,
		Mode:       uint32(mode),
	})
	if err != nil {
		return Event{}, err
	}
	argsRaw, _ := json.Marshal(args)
	payload := ToolExecEvent{
		Tool:        tool,
		Args:        argsRaw,
		Result:      result,
		Err:         errMsg,
		SideEffects: []SideEffect{se},
	}
	return s.Append(EventTypeToolExec, payload)
}

// SetMilestone records a milestone event AND points a milestone ref
// at it. Phase 2's Outer Router hook calls this on every state
// transition (named "milestone/<phase>") so Phase 5 rollback can
// target "the most recent milestone of kind X" cheaply.
//
// Partial-write semantics (2026-05-22 audit C2): Append succeeds first,
// then Refs.Set. If the ref write fails, the event has already been
// committed and CANNOT be undone — the chain itself is valid (the
// event is correctly sha-chained and still walkable from any descendant
// tip), it just isn't reachable by ref name. The caller's error tells
// them rollback to this milestone won't work; the event is harmless
// otherwise. We also stderr-warn here so the leak is diagnosable even
// when the caller drops the returned error.
//
// Compare with `git tag` overwrite leaving dangling commits — same
// shape, same acceptable cost.
func (s *Snapshotter) SetMilestone(name, reason string) (Event, error) {
	ev, err := s.Append(EventTypeMilestone, MilestoneEvent{Name: name, Reason: reason})
	if err != nil {
		return Event{}, err
	}
	if err := s.Refs.Set("milestone/"+name, ev.ID); err != nil {
		fmt.Fprintf(os.Stderr, "[snapshot] SetMilestone(%q): event %s appended but ref write failed: %v — event is unreferenced but chain remains valid\n", name, shortID(ev.ID), err)
		return ev, fmt.Errorf("snapshotter: set milestone ref: %w", err)
	}
	return ev, nil
}

// shortID renders a sha for diagnostic messages — first 12 chars is
// enough to disambiguate within any reasonable chain.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// Tip returns the current tip event id, or "" with no error when the
// log is empty.
func (s *Snapshotter) Tip() (string, error) {
	id, err := s.Refs.Get(tipRefName)
	if err == ErrRefNotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

// IsEnabled reports whether the snapshotter's storage layout has been
// initialised on disk. Used by the agent driver to choose between
// "log this event" and "no-op" when snapshotting is opt-in for a
// particular run.
func (s *Snapshotter) IsEnabled() bool {
	_, err := os.Stat(s.SnapshotsRoot())
	return err == nil
}
