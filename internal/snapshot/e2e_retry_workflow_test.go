package snapshot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Phase 8 e2e: simulate the full Phase 5+6+7 retry workflow against
// the snapshot store directly, without requiring a live LLM or
// outer Router. Walks the data shapes the agent-side
// RunRoutedTurnWithRetry produces and verifies the round-trip
// integrity.
//
// Scenario:
//   1. Attempt 1 runs through three Outer.* transitions, writes a
//      few source files, then terminates at Outer.Obstacle with a
//      "vacuous-oracle-guard" reason.
//   2. Retry loop picks the graph-declared milestone as rollback
//      target.
//   3. Workdir restored to the graph-declared state.
//   4. Lesson synthesized + written.
//   5. Attempt 2 starts: sees the lesson on disk, completes
//      Outer.Finished.
//
// Each step's invariants are asserted: event chain integrity,
// archive ref reachability, restored file content correctness,
// lesson markdown format, prompt-injectable body presence.
func TestE2E_RetryWorkflow_VacuousOracleFailRecover(t *testing.T) {
	workdir := t.TempDir()
	s := NewSnapshotter(workdir)

	// === ATTEMPT 1 ===
	// Real flow per outer_loop.go RunRoutedTurnWithPersist:
	//   1. r.Step calls h.Handle — sub-agent loop runs and produces
	//      tool.exec events (each with file/graph side_effects).
	//   2. r.Step returns the next state.
	//   3. outer_loop.go emits outer.transition event for
	//      current→next.
	//   4. outer_loop.go calls SetMilestone for next (Phase 7) —
	//      milestone ref points at the outer.transition event id,
	//      which is the LATEST event after that phase's work.
	//
	// So the event ORDER is: tool.exec*, outer.transition,
	// SetMilestone. Rolling back to the milestone replays everything
	// up to and including the outer.transition — including all the
	// tool.execs from that phase. This is the load-bearing ordering
	// invariant.
	architecturePayload, _ := json.Marshal(map[string]string{"rootSessionID": "s_root"})

	// Phase 1: H_architect's sub-agent writes session file.
	preA, _ := s.TakeWorkdir()
	_ = os.MkdirAll(filepath.Join(workdir, "K", "sessions"), 0o755)
	_ = os.WriteFile(filepath.Join(workdir, "K", "sessions", "s_root.json"), []byte(`{"id":"s_root","output":{"architecture":"v1"}}`), 0o644)
	postA, _ := s.TakeWorkdir()
	effectsA, _ := s.DiffToSideEffects(preA.Diff(postA))
	_, _ = s.Append(EventTypeToolExec, ToolExecEvent{
		Tool: "session_set_architecture", SideEffects: effectsA,
	})
	// Outer.Task → Outer.Architecture transition + milestone.
	_, _ = s.Append(EventTypeOuterTransition, OuterTransitionEvent{
		From: "Outer.Task", To: "Outer.Architecture", Payload: architecturePayload,
	})
	_, _ = s.SetMilestone("architecture-s_root", "auto: reached Outer.Architecture")

	// Phase 2: H_graph_declare's sub-agent writes graph.
	preG, _ := s.TakeWorkdir()
	_ = os.WriteFile(filepath.Join(workdir, "K", "graph.json"), []byte(`{"objects":{"Foo":{}}}`), 0o644)
	postG, _ := s.TakeWorkdir()
	effectsG, _ := s.DiffToSideEffects(preG.Diff(postG))
	_, _ = s.Append(EventTypeToolExec, ToolExecEvent{
		Tool: "graph_create_object", SideEffects: effectsG,
	})
	// Outer.Architecture → Outer.GraphDeclared transition + milestone.
	_, _ = s.Append(EventTypeOuterTransition, OuterTransitionEvent{
		From: "Outer.Architecture", To: "Outer.GraphDeclared", Payload: architecturePayload,
	})
	_, _ = s.SetMilestone("graph-declared-s_root", "auto: reached Outer.GraphDeclared")

	// Phase 3: H_confirm_one sub-agent writes source (bad version
	// that triggers vacuous-oracle). No milestone for SomeConfirmed
	// (per-object intermediate state, not a real rollback target).
	preB, _ := s.TakeWorkdir()
	_ = os.WriteFile(filepath.Join(workdir, "main.c"), []byte("// agent forgot to compile\n"), 0o644)
	postB, _ := s.TakeWorkdir()
	effectsB, _ := s.DiffToSideEffects(preB.Diff(postB))
	_, _ = s.Append(EventTypeToolExec, ToolExecEvent{
		Tool: "write_file", SideEffects: effectsB,
	})
	_, _ = s.Append(EventTypeOuterTransition, OuterTransitionEvent{
		From: "Outer.GraphDeclared", To: "Outer.SomeConfirmed", Payload: architecturePayload,
	})

	// Phase 4: terminal Outer.Obstacle.
	obstaclePayload, _ := json.Marshal(map[string]interface{}{
		"phase":   "confirm_one",
		"reasons": []string{"vacuous-oracle-guard: /workspace/executable hash matches reference (agent never compiled)"},
	})
	_, _ = s.Append(EventTypeOuterTransition, OuterTransitionEvent{
		From: "Outer.SomeConfirmed", To: "Outer.Obstacle", Payload: obstaclePayload,
	})

	// Verify the milestones were set (Phase 7 auto-milestone hook).
	if _, err := s.Refs.Get("milestone/architecture-s_root"); err != nil {
		t.Fatalf("architecture milestone missing: %v", err)
	}
	if _, err := s.Refs.Get("milestone/graph-declared-s_root"); err != nil {
		t.Fatalf("graph-declared milestone missing: %v", err)
	}

	// === RETRY DECISION ===
	// pickRollbackMilestone equivalent: choose graph-declared (most-
	// recent real work milestone). This is what
	// agent.pickRollbackMilestone returns for this state.
	graphDeclaredEvt, _ := s.Refs.Get("milestone/graph-declared-s_root")

	// Rollback to graph-declared milestone.
	rb, err := s.Rollback(graphDeclaredEvt, "auto-attempt-1")
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// Workdir should now be at graph-declared state: K/graph.json
	// exists (was written before the milestone), main.c does NOT.
	if _, err := os.Stat(filepath.Join(workdir, "K", "graph.json")); err != nil {
		t.Errorf("graph.json should still exist after rollback to graph-declared: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workdir, "main.c")); !os.IsNotExist(err) {
		t.Errorf("main.c should be removed by rollback (written AFTER graph-declared milestone); got err=%v", err)
	}

	// Archived branch ref points at the failed tip.
	failedTipFromRef, _ := s.Refs.Get(rb.ArchivedBranchRef)
	if failedTipFromRef != rb.FailedTip {
		t.Errorf("archive ref must match failed tip: %s vs %s", failedTipFromRef[:16], rb.FailedTip[:16])
	}

	// === LESSON SYNTHESIS ===
	lesson, err := s.SynthesizeLesson(rb.ArchivedBranchRef, nil)
	if err != nil {
		t.Fatalf("synthesize lesson: %v", err)
	}
	if lesson.GeneratedBy != "heuristic:vacuous-oracle" {
		t.Errorf("expected heuristic vacuous-oracle, got %q", lesson.GeneratedBy)
	}
	if err := s.WriteLesson(lesson); err != nil {
		t.Fatalf("write lesson: %v", err)
	}

	// === ATTEMPT 2 starts: read lessons (would happen in
	// H_architect's buildLessonPreamble).
	lessons, err := s.ReadLessons()
	if err != nil {
		t.Fatal(err)
	}
	if len(lessons) != 1 {
		t.Fatalf("expected 1 lesson visible to attempt 2, got %d", len(lessons))
	}
	if !strings.Contains(lessons[0].Body, "compile") {
		t.Errorf("attempt-2-visible lesson body must mention compile (vacuous-oracle fix): %s", lessons[0].Body)
	}

	// Attempt 2: same event order as attempt 1 (tool.exec first,
	// then outer.transition, then milestone). Architecture is
	// already set on disk (rollback preserved it), but the agent
	// re-records the transition to advance the new branch's chain.
	_, _ = s.Append(EventTypeOuterTransition, OuterTransitionEvent{
		From: "Outer.Task", To: "Outer.Architecture", Payload: architecturePayload,
	})
	_, _ = s.SetMilestone("architecture-s_root", "auto: reached Outer.Architecture (attempt 2)")

	// Agent NOW writes main.c + compile.sh (lessons told them to).
	preF, _ := s.TakeWorkdir()
	_ = os.WriteFile(filepath.Join(workdir, "main.c"), []byte("int main(){return 0;}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(workdir, "compile.sh"), []byte("#!/bin/sh\ngcc main.c -o executable\n"), 0o755)
	postF, _ := s.TakeWorkdir()
	effectsF, _ := s.DiffToSideEffects(preF.Diff(postF))
	_, _ = s.Append(EventTypeToolExec, ToolExecEvent{
		Tool: "write_file", SideEffects: effectsF,
	})

	// Outer.SomeConfirmed → Outer.AllConfirmed → Outer.Finished.
	_, _ = s.Append(EventTypeOuterTransition, OuterTransitionEvent{
		From: "Outer.SomeConfirmed", To: "Outer.AllConfirmed", Payload: architecturePayload,
	})
	_, _ = s.SetMilestone("all-confirmed-s_root", "auto: reached Outer.AllConfirmed")
	_, _ = s.Append(EventTypeOuterTransition, OuterTransitionEvent{
		From: "Outer.AllConfirmed", To: "Outer.Finished", Payload: architecturePayload,
	})

	// === VERIFY POST-RETRY STATE ===
	// 1. New tip points at Outer.Finished transition.
	tip, _ := s.Tip()
	tipEv, _ := s.Events.Get(tip)
	if tipEv.Type != EventTypeOuterTransition {
		t.Errorf("tip should be a transition event, got %s", tipEv.Type)
	}
	// 2. Old archived branch still reachable via WalkFrom.
	var archivedEvents int
	_ = s.Events.WalkFrom(rb.FailedTip, func(Event) error {
		archivedEvents++
		return nil
	})
	if archivedEvents < 5 {
		t.Errorf("archived branch should still contain attempt-1's events (expected ≥5), got %d", archivedEvents)
	}
	// 3. Lesson still on disk.
	lessonsAfter, _ := s.ReadLessons()
	if len(lessonsAfter) != 1 {
		t.Errorf("lesson should persist across attempts, got %d lessons", len(lessonsAfter))
	}
	// 4. New branch has its own events (not orphaned).
	var newBranchEvents int
	_ = s.Events.WalkFrom(tip, func(Event) error {
		newBranchEvents++
		return nil
	})
	if newBranchEvents < 6 {
		t.Errorf("new branch must reach all attempt-2 events from its tip (expected ≥6), got %d", newBranchEvents)
	}

	// 5. The architecture event in the new branch shares parent with
	// the architecture event in the archived branch (both children
	// of the rollback target). This is the load-bearing "branched
	// history" property — rollback created a Y in the chain.
	// Verify by listing all events with parent == graphDeclaredEvt's
	// parent... actually graphDeclaredEvt is the rollback TARGET, so
	// both branches' first child after target should have target as
	// parent.
	allEvents, _ := s.Events.List()
	var childrenOfTarget []string
	for _, ev := range allEvents {
		if ev.ParentID == graphDeclaredEvt {
			childrenOfTarget = append(childrenOfTarget, ev.ID)
		}
	}
	if len(childrenOfTarget) < 2 {
		t.Errorf("rollback target should have ≥2 children (archived branch + new branch), got %d", len(childrenOfTarget))
	}
}
