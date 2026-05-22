package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/snapshot"
)

// pickRollbackMilestone walks a priority list (most-work-invested
// first); the first available milestone ref wins.
func TestPickRollbackMilestone_Priority(t *testing.T) {
	dir := t.TempDir()
	snap := snapshot.NewSnapshotter(dir)
	a, _ := snap.Append("test", "anchor-a")
	b, _ := snap.Append("test", "anchor-b")
	c, _ := snap.Append("test", "anchor-c")

	// Only architecture milestone set — should win.
	_ = snap.Refs.Set("milestone/architecture-s_root", a.ID)
	id, name := pickRollbackMilestone(snap, "s_root")
	if name != "architecture" || id != a.ID {
		t.Errorf("with only architecture milestone: got (%q,%s) want (architecture,%s)", name, id[:8], a.ID[:8])
	}

	// Add graph-declared — beats architecture.
	_ = snap.Refs.Set("milestone/graph-declared-s_root", b.ID)
	id, name = pickRollbackMilestone(snap, "s_root")
	if name != "graph-declared" || id != b.ID {
		t.Errorf("graph-declared must beat architecture: got (%q,%s) want (graph-declared,%s)", name, id[:8], b.ID[:8])
	}

	// Add checkpointed — beats everything else.
	_ = snap.Refs.Set("milestone/checkpointed-s_root", c.ID)
	id, name = pickRollbackMilestone(snap, "s_root")
	if name != "checkpointed" || id != c.ID {
		t.Errorf("checkpointed must beat earlier phases: got (%q,%s) want (checkpointed,%s)", name, id[:8], c.ID[:8])
	}
}

// No milestones present (very-early failure) → ("", "") signalling
// retry would be useless.
func TestPickRollbackMilestone_NoneAvailable(t *testing.T) {
	dir := t.TempDir()
	snap := snapshot.NewSnapshotter(dir)
	id, name := pickRollbackMilestone(snap, "s_root")
	if id != "" || name != "" {
		t.Errorf("no milestones must return empty pair, got (%q,%s)", name, id)
	}
}

// Multi-session: milestones for s_root must NOT match a query for
// s_other — milestone refs are session-scoped.
func TestPickRollbackMilestone_SessionScoped(t *testing.T) {
	dir := t.TempDir()
	snap := snapshot.NewSnapshotter(dir)
	a, _ := snap.Append("test", "anchor")
	_ = snap.Refs.Set("milestone/graph-declared-s_root", a.ID)

	if id, name := pickRollbackMilestone(snap, "s_root"); name != "graph-declared" {
		t.Errorf("s_root lookup should find milestone, got (%q,%s)", name, id[:8])
	}
	if id, name := pickRollbackMilestone(snap, "s_other"); id != "" || name != "" {
		t.Errorf("s_other lookup must not match s_root's milestone, got (%q,%s)", name, id[:8])
	}
}

// milestoneNameFor returns "" for states that aren't worth
// snapshotting as rollback targets (mid-loop / terminal). Sanity
// check the table — accidentally adding a state name here would
// flood the milestone namespace.
func TestMilestoneNameFor(t *testing.T) {
	for input, want := range map[string]string{
		"Outer.Task":          "",
		"Outer.Architecture":  "architecture",
		"Outer.GraphDeclared": "graph-declared",
		"Outer.SomeConfirmed": "",
		"Outer.AllConfirmed":  "all-confirmed",
		"Outer.Aggregated":    "aggregated",
		"Outer.Built":         "built",
		"Outer.Checkpointed":  "checkpointed",
		"Outer.GatePassed":    "gate-passed",
		"Outer.Finished":      "",
		"Outer.Obstacle":      "",
		"BogusType":           "",
	} {
		got := milestoneNameFor(input)
		if got != want {
			t.Errorf("milestoneNameFor(%q): got %q want %q", input, got, want)
		}
	}
}

// TestRollbackPriorityCoversMilestoneNameFor (2026-05-22 audit T1) —
// every non-empty name returned by milestoneNameFor MUST either appear
// in rollbackMilestonePriority OR be the documented exclusion
// "gate-passed". Drift between the two lists would silently disable
// rollback to the new milestone — Phase 7's most-work-invested target
// selection would skip it without diagnostic.
//
// Bidirectional check: also verify the priority list doesn't reference
// names milestoneNameFor never emits (dead entries).
func TestRollbackPriorityCoversMilestoneNameFor(t *testing.T) {
	// Names milestoneNameFor can emit. Mirrors the switch in
	// outer_loop.go::milestoneNameFor; if that switch grows, this set
	// must too. The mirror is intentional: a duplicate forces a
	// reviewer to confirm the test got updated.
	emittedNames := map[string]bool{}
	for _, outerType := range []string{
		"Outer.Architecture", "Outer.GraphDeclared", "Outer.AllConfirmed",
		"Outer.Aggregated", "Outer.Built", "Outer.Checkpointed",
		"Outer.GatePassed",
	} {
		if name := milestoneNameFor(outerType); name != "" {
			emittedNames[name] = true
		}
	}
	priorityNames := map[string]bool{}
	for _, n := range rollbackMilestonePriority {
		priorityNames[n] = true
	}
	// Forward: every emitted name must be in priority OR be the
	// documented exclusion "gate-passed".
	for emitted := range emittedNames {
		if emitted == "gate-passed" {
			continue // intentional exclusion per outer_retry.go comment
		}
		if !priorityNames[emitted] {
			t.Errorf("milestoneNameFor emits %q but rollbackMilestonePriority does not list it — Phase 7 retry will skip this milestone silently. Add to rollbackMilestonePriority in outer_retry.go, or document the exclusion.", emitted)
		}
	}
	// Backward: every priority entry must be emittable by
	// milestoneNameFor (no dead refs).
	for p := range priorityNames {
		if !emittedNames[p] {
			t.Errorf("rollbackMilestonePriority lists %q but milestoneNameFor never emits this name — dead entry, remove or wire up.", p)
		}
	}
}

// buildLessonPreamble: empty when no lessons.
func TestBuildLessonPreamble_NoLessons(t *testing.T) {
	chdirTo(t, t.TempDir())
	out := buildLessonPreamble(nil, &OuterDeps{RootSessionID: "s_root"})
	if out != "" {
		t.Errorf("no lessons should yield empty preamble, got %q", out)
	}
}

// buildLessonPreamble: includes each lesson body and the framing
// delimiter so the agent can grep for it.
//
// 2026-05-22 (audit C1): test now attaches the Snapshotter via
// ctx instead of relying on the prior cwd-fallback. The fallback was
// removed because it silently misrouted whenever cwd != agent workdir.
func TestBuildLessonPreamble_RendersLessons(t *testing.T) {
	dir := t.TempDir()
	chdirTo(t, dir)
	snap := snapshot.NewSnapshotter(dir)
	// Need to force IsEnabled — write any event so the snapshots
	// dir exists.
	_, _ = snap.Append("test", "init")
	// Write two lessons.
	_ = snap.WriteLesson(&snapshot.Lesson{
		BranchRef: "attempt/1", Failure: "f1", WhatWentWrong: "lesson body 1",
		GeneratedBy: "heuristic:foo",
	})
	_ = snap.WriteLesson(&snapshot.Lesson{
		BranchRef: "attempt/2", Failure: "f2", WhatWentWrong: "lesson body 2",
		GeneratedBy: "heuristic:bar",
	})
	ctx := snapshot.WithSnapshotter(context.Background(), snap)
	out := buildLessonPreamble(ctx, &OuterDeps{RootSessionID: "s_root"})
	for _, want := range []string{
		"Prior attempts failed",
		"[attempt/1]",
		"[attempt/2]",
		"End of prior-attempt lessons",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preamble missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// resultPhaseSummary: extracts phase + first reason; empty for
// non-Obstacle terminal.
func TestResultPhaseSummary(t *testing.T) {
	finishedResult := RoutedRunResult{TerminalType: "Outer.Finished"}
	if got := resultPhaseSummary(finishedResult); got != "" {
		t.Errorf("non-Obstacle should yield empty summary, got %q", got)
	}
}

// chdirTo is provided by hooks_test.go in this package — reuse it.
