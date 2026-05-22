package snapshot

import (
	"context"
	"testing"
)

// Round-trip: WithSnapshotter then FromContext returns the same
// instance.
func TestContext_RoundTrip(t *testing.T) {
	s := NewSnapshotter(t.TempDir())
	ctx := WithSnapshotter(context.Background(), s)
	got := FromContext(ctx)
	if got != s {
		t.Errorf("FromContext must return same *Snapshotter, got %p want %p", got, s)
	}
}

// Bare ctx → FromContext returns nil. The nil case is the load-bearing
// opt-out — every non-snapshotting code path relies on it.
func TestContext_NoSnapshotterIsNil(t *testing.T) {
	got := FromContext(context.Background())
	if got != nil {
		t.Errorf("bare ctx must yield nil snapshotter, got %p", got)
	}
}

// FromContext on a nil ctx → nil (no panic). Defensive — some
// internal call sites may receive a nil ctx during shutdown / tests.
func TestContext_NilCtxIsSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("FromContext on nil ctx must not panic, got %v", r)
		}
	}()
	got := FromContext(nil)
	if got != nil {
		t.Errorf("nil ctx must yield nil snapshotter, got %p", got)
	}
}

// Attaching a nil Snapshotter is allowed but FromContext still
// returns nil — symmetric semantics so feature-flag callers don't
// have to special-case.
func TestContext_NilSnapshotterIsNil(t *testing.T) {
	ctx := WithSnapshotter(context.Background(), nil)
	if got := FromContext(ctx); got != nil {
		t.Errorf("nil snapshotter must round-trip to nil, got %p", got)
	}
}

// Nested ctx: a child ctx with a different Snapshotter overrides the
// parent's. Standard context.Value semantics.
func TestContext_ChildOverridesParent(t *testing.T) {
	parent := NewSnapshotter(t.TempDir())
	child := NewSnapshotter(t.TempDir())
	pctx := WithSnapshotter(context.Background(), parent)
	cctx := WithSnapshotter(pctx, child)
	if got := FromContext(cctx); got != child {
		t.Errorf("child ctx must shadow parent, got %p want %p", got, child)
	}
	if got := FromContext(pctx); got != parent {
		t.Errorf("parent ctx unchanged, got %p want %p", got, parent)
	}
}
