package snapshot

import "context"

// snapshotKey is the unexported context key for attaching a
// Snapshotter to an in-flight request. Unexported so external code
// can't accidentally collide on the same key.
type snapshotKey struct{}

// WithSnapshotter returns a child ctx carrying s. Pass the returned
// ctx through the call tree wherever snapshot capture should occur.
// Calling with a nil Snapshotter is allowed and yields a ctx that
// FromContext returns nil for — useful for "snapshotting is opt-in"
// codepaths.
func WithSnapshotter(parent context.Context, s *Snapshotter) context.Context {
	return context.WithValue(parent, snapshotKey{}, s)
}

// FromContext returns the Snapshotter attached to ctx, or nil if no
// snapshotter is in flight. The nil case is THE common case for
// non-snapshotting runs (tests, dry-runs, REPL chat) — callers MUST
// nil-check before dereferencing.
//
// Pattern at every capture site:
//
//	if s := snapshot.FromContext(ctx); s != nil {
//	    _, _ = s.Append(snapshot.EventTypeXxx, payload)
//	}
//
// The early return when s == nil is the snapshotting opt-out; every
// non-snapshotting call site stays zero-cost.
func FromContext(ctx context.Context) *Snapshotter {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(snapshotKey{}).(*Snapshotter)
	return s
}
