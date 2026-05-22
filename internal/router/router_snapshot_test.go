package router

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/creator915/Koncept_OS/internal/snapshot"
)

// Phase 2d integration: run a minimal router pipeline with a
// Snapshotter attached to ctx and verify every state transition
// produced an outer.transition event in the chain.
//
// This is the load-bearing test for "snapshot capture is non-invasive
// and complete" — if a transition's event is missing, replay can
// never reproduce that transition.
func TestRouter_RunEmitsOuterTransitionEvents(t *testing.T) {
	dir := t.TempDir()
	snap := snapshot.NewSnapshotter(dir)

	r := NewRouter()
	r.Register(&HandlerFunc{
		In:  "A",
		Out: []string{"B"},
		Run: func(ctx context.Context, in TypedValue) (TypedValue, error) {
			tv, _ := NewTypedValue("B", map[string]string{"step": "1"})
			return tv, nil
		},
	})
	r.Register(&HandlerFunc{
		In:  "B",
		Out: []string{"C"},
		Run: func(ctx context.Context, in TypedValue) (TypedValue, error) {
			tv, _ := NewTypedValue("C", map[string]string{"step": "2"})
			return tv, nil
		},
	})
	r.RegisterTerminal("C")

	ctx := snapshot.WithSnapshotter(context.Background(), snap)
	initial, _ := NewTypedValue("A", map[string]string{"start": "true"})

	final, err := r.Run(ctx, initial)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if final.Type != "C" {
		t.Fatalf("expected terminal C, got %s", final.Type)
	}

	// Verify the event log contains the two transitions.
	events, err := snap.Events.List()
	if err != nil {
		t.Fatal(err)
	}
	var transitions []snapshot.OuterTransitionEvent
	for _, ev := range events {
		if ev.Type != snapshot.EventTypeOuterTransition {
			continue
		}
		var tr snapshot.OuterTransitionEvent
		if err := json.Unmarshal(ev.Payload, &tr); err != nil {
			t.Fatal(err)
		}
		transitions = append(transitions, tr)
	}
	if len(transitions) != 2 {
		t.Fatalf("expected 2 outer.transition events (A→B, B→C), got %d", len(transitions))
	}
	if transitions[0].From != "A" || transitions[0].To != "B" {
		t.Errorf("first transition: got %q→%q want A→B", transitions[0].From, transitions[0].To)
	}
	if transitions[1].From != "B" || transitions[1].To != "C" {
		t.Errorf("second transition: got %q→%q want B→C", transitions[1].From, transitions[1].To)
	}

	// Tip ref must point at the last event recorded.
	tip, _ := snap.Tip()
	if tip != events[len(events)-1].ID {
		t.Errorf("tip ref out of sync: tip=%s last event=%s", tip, events[len(events)-1].ID)
	}

	// On-disk layout verification: events/ exists, refs/tip.txt exists.
	if _, err := snap.Refs.Get("tip"); err != nil {
		t.Errorf("refs/tip must be set after run: %v", err)
	}
	_ = filepath.Join // silence import in case rearranged
}

// Router with NO snapshotter attached → no events recorded, no
// crash. The hook must be zero-cost / silent for non-snapshotting
// runs (REPL, tests, dry-runs).
func TestRouter_RunNoSnapshotterIsTransparent(t *testing.T) {
	r := NewRouter()
	r.Register(&HandlerFunc{
		In:  "X",
		Out: []string{"Y"},
		Run: func(ctx context.Context, in TypedValue) (TypedValue, error) {
			tv, _ := NewTypedValue("Y", nil)
			return tv, nil
		},
	})
	r.RegisterTerminal("Y")

	initial, _ := NewTypedValue("X", nil)
	final, err := r.Run(context.Background(), initial)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if final.Type != "Y" {
		t.Errorf("expected Y, got %s", final.Type)
	}
}
