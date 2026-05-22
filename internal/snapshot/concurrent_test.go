package snapshot

import (
	"sync"
	"testing"
)

// 2026-05-22 — Phase 2 audit regression. PB-30 batches run tool
// batches concurrently via runBatchConcurrent; without the
// Snapshotter.Append mutex, two goroutines could read tip=T at the
// same time, each write an event with parent=T, and the second's
// tip Set would overwrite the first's — orphaning the first event.
//
// The chain integrity property to assert post-fix: after N concurrent
// Appends, ALL N events are reachable by walking back from tip, and
// no two events share the same parent (the chain is linear).
func TestSnapshotter_ConcurrentAppendsAreSerialised(t *testing.T) {
	s := NewSnapshotter(t.TempDir())
	const N = 50

	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, err := s.Append("test", map[string]int{"i": i})
			if err != nil {
				t.Errorf("concurrent Append %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	// Property 1: exactly N events on disk.
	all, err := s.Events.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != N {
		t.Errorf("expected %d events, got %d (race may have produced collisions)", N, len(all))
	}

	// Property 2: chain is linear — every event except genesis has
	// a parent that points at a real event, and no two events share
	// the same parent.
	parentCount := map[string]int{}
	for _, ev := range all {
		parentCount[ev.ParentID]++
	}
	for parent, count := range parentCount {
		if count > 1 {
			t.Errorf("multiple events (%d) share parent %q — concurrent race produced branch", count, shortOrEmpty(parent))
		}
	}

	// Property 3: walking from tip back through parents reaches all
	// N events. Without the lock, some events would be orphans
	// (file exists, no ref reaches them through the linear chain).
	tip, err := s.Tip()
	if err != nil {
		t.Fatal(err)
	}
	if tip == "" {
		t.Fatal("tip empty after N successful Appends")
	}
	reached := 0
	cursor := tip
	for cursor != "" {
		ev, err := s.Events.Get(cursor)
		if err != nil {
			t.Fatalf("walking chain at %s: %v", cursor[:8], err)
		}
		reached++
		cursor = ev.ParentID
	}
	if reached != N {
		t.Errorf("chain from tip reaches %d events, want %d — %d are orphaned", reached, N, N-reached)
	}
}

// Capture wraps Append + stderr warn. Test the silent-failure path:
// when the underlying Append succeeds, no stderr noise; when it fails,
// stderr line written. We don't redirect stderr here (race vs other
// tests' stderr); the test only verifies Capture doesn't panic and
// records the event on the happy path.
func TestSnapshotter_CaptureHappyPath(t *testing.T) {
	s := NewSnapshotter(t.TempDir())
	s.Capture("test", "test.type", map[string]int{"x": 1})

	all, err := s.Events.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("Capture must record one event, got %d", len(all))
	}
}

// shortOrEmpty trims sha for log readability; empty stays empty
// (genesis parent).
func shortOrEmpty(s string) string {
	if s == "" {
		return "(genesis)"
	}
	if len(s) < 8 {
		return s
	}
	return s[:8]
}
