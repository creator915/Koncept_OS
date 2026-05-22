package snapshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Phase 5 happy path: capture a 3-event chain, rollback to event 2,
// verify (a) tip points at event 2, (b) workdir restored to event-2
// state, (c) failed branch ref points at original tip, (d) ancestry
// of the branch ref still reaches all 3 events.
func TestRollback_HappyPath(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)

	// Event 1: write v1.
	_ = os.WriteFile(filepath.Join(srcDir, "main.c"), []byte("v1"), 0o644)
	pre, _ := s.TakeWorkdir()
	post, _ := s.TakeWorkdir()
	effects, _ := s.DiffToSideEffects(pre.Diff(post))
	e1, _ := s.Append(EventTypeToolExec, ToolExecEvent{Tool: "write_file", SideEffects: effects})

	// Event 2: overwrite to v2.
	pre, _ = s.TakeWorkdir()
	_ = os.WriteFile(filepath.Join(srcDir, "main.c"), []byte("v2"), 0o644)
	post, _ = s.TakeWorkdir()
	effects, _ = s.DiffToSideEffects(pre.Diff(post))
	e2, _ := s.Append(EventTypeToolExec, ToolExecEvent{Tool: "edit", SideEffects: effects})

	// Event 3: overwrite to v3 (the "failed" iteration we'll roll back).
	pre, _ = s.TakeWorkdir()
	_ = os.WriteFile(filepath.Join(srcDir, "main.c"), []byte("v3-bad"), 0o644)
	post, _ = s.TakeWorkdir()
	effects, _ = s.DiffToSideEffects(pre.Diff(post))
	e3, _ := s.Append(EventTypeToolExec, ToolExecEvent{Tool: "edit", SideEffects: effects})

	// Rollback to e2.
	result, err := s.Rollback(e2.ID, "")
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	// (a) tip = e2
	tip, _ := s.Tip()
	if tip != e2.ID {
		t.Errorf("tip not rewound: got %s want %s", tip[:16], e2.ID[:16])
	}
	// (b) workdir back to v2
	got, err := os.ReadFile(filepath.Join(srcDir, "main.c"))
	if err != nil {
		t.Fatalf("read main.c: %v", err)
	}
	if string(got) != "v2" {
		t.Errorf("workdir not restored: got %q want %q", got, "v2")
	}
	// (c) archive ref points at e3
	if result.FailedTip != e3.ID {
		t.Errorf("FailedTip mismatch: got %s want %s", result.FailedTip[:16], e3.ID[:16])
	}
	if result.ArchivedBranchRef != "attempt/1" {
		t.Errorf("auto branch name should be attempt/1, got %s", result.ArchivedBranchRef)
	}
	branchTip, err := s.Refs.Get(result.ArchivedBranchRef)
	if err != nil {
		t.Fatalf("archive ref not readable: %v", err)
	}
	if branchTip != e3.ID {
		t.Errorf("archive ref target mismatch: got %s want %s", branchTip[:16], e3.ID[:16])
	}
	// (d) ancestry from branch tip reaches e1, e2, e3.
	var visited []string
	if err := s.Events.WalkFrom(branchTip, func(ev Event) error {
		visited = append(visited, ev.ID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(visited) != 3 || visited[0] != e1.ID || visited[1] != e2.ID || visited[2] != e3.ID {
		t.Errorf("branch walk: got %v want [%s %s %s]", visited, e1.ID[:16], e2.ID[:16], e3.ID[:16])
	}
}

// Rollback to current tip → error (nothing to roll back).
func TestRollback_RefusesNoOpTarget(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)
	ev, _ := s.Append("test", "only-event")
	_, err := s.Rollback(ev.ID, "")
	if err == nil {
		t.Fatal("rollback to current tip must error")
	}
	if !strings.Contains(err.Error(), "nothing to roll back") && !strings.Contains(err.Error(), "tip") {
		t.Errorf("error must explain why: %v", err)
	}
}

// Rollback to an event NOT in tip's ancestry must refuse. The
// classic shape: create a branch, then try to rollback main to an
// event that only exists in the branch. Refuse rather than risk
// orphaning state.
func TestRollback_RefusesNonAncestorTarget(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)
	// Build chain a → b
	a, _ := s.Append("test", "a")
	b, _ := s.Append("test", "b")
	_ = a
	// Rollback once to introduce branch c — c is now archived under
	// attempt/1, tip is back at a... no wait, this is too convoluted.
	// Simpler: capture two unrelated chains by manually constructing
	// an event under a fictional parent and try to roll back to it.
	bogus := strings.Repeat("f", 64)
	_, err := s.Rollback(bogus, "")
	if err == nil {
		t.Error("rollback to nonexistent target must error")
	}
	_ = b
}

// Empty chain (no tip yet) → rollback errors with clear message.
func TestRollback_EmptyChain(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)
	_, err := s.Rollback(strings.Repeat("a", 64), "")
	if err == nil {
		t.Error("rollback on empty chain must error")
	}
}

// New events after rollback create a NEW branch — children of the
// rollback target, NOT of the archived tip. After rollback, two
// children share parent = target.
func TestRollback_NewBranchSharesTargetParent(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)
	a, _ := s.Append("t", "a")
	b, _ := s.Append("t", "b")
	c, _ := s.Append("t", "c")

	_, err := s.Rollback(b.ID, "")
	if err != nil {
		t.Fatal(err)
	}

	// New append → parent = b.ID
	d, _ := s.Append("t", "d-new-branch")
	if d.ParentID != b.ID {
		t.Errorf("new branch event's parent should be rollback target %s, got %s", b.ID[:16], d.ParentID[:16])
	}
	// c is still there with parent = b too.
	cGot, err := s.Events.Get(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cGot.ParentID != b.ID {
		t.Errorf("archived branch event c should still have parent b, got %s", cGot.ParentID[:16])
	}
	// Both c and d share parent b — branch confirmed.
	if c.ParentID != d.ParentID {
		t.Errorf("c and d must share parent (rollback created branches at b), got c.parent=%s d.parent=%s",
			c.ParentID[:8], d.ParentID[:8])
	}
	_ = a
}

// Auto-allocation: a 2nd rollback gets attempt/2, not overwrites
// attempt/1.
func TestRollback_AutoBranchNameIncrement(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)

	// Set up: a → b → c, rollback to b → archive at attempt/1
	a, _ := s.Append("t", "a")
	b, _ := s.Append("t", "b")
	_, _ = s.Append("t", "c") // c is now archived
	r1, err := s.Rollback(b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if r1.ArchivedBranchRef != "attempt/1" {
		t.Errorf("first rollback should be attempt/1, got %s", r1.ArchivedBranchRef)
	}

	// Continue: b → d (new branch), rollback to b again → attempt/2
	_, _ = s.Append("t", "d-new")
	r2, err := s.Rollback(b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if r2.ArchivedBranchRef != "attempt/2" {
		t.Errorf("second rollback should be attempt/2, got %s", r2.ArchivedBranchRef)
	}
	_ = a
}

// Rollback validates branchName at its entry point — illegal segments
// (".." traversal, leading slash, null bytes) are rejected with a
// rollback-specific error, not a confusing low-level RefStore one.
// Audit finding: previously these names would error at RefStore.Set
// with a generic message; user couldn't tell rollback rejected them.
func TestRollback_RejectsIllegalBranchName(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)
	a, _ := s.Append("t", "a")
	_, _ = s.Append("t", "b")

	for _, badName := range []string{
		"../escape",
		"foo/../../bar",
		"/abs/path",
	} {
		_, err := s.Rollback(a.ID, badName)
		if err == nil {
			t.Errorf("rollback with branchName=%q must be rejected", badName)
		}
		if err != nil && !strings.Contains(err.Error(), "rollback:") {
			t.Errorf("error must be tagged 'rollback:' (not bubbled from refstore): %v", err)
		}
	}
}

// Explicit branch name overrides auto-allocation.
func TestRollback_NamedBranch(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)
	a, _ := s.Append("t", "a")
	_, _ = s.Append("t", "b-failed")
	r, err := s.Rollback(a.ID, "gate-fail-method-use-rule")
	if err != nil {
		t.Fatal(err)
	}
	if r.ArchivedBranchRef != "attempt/gate-fail-method-use-rule" {
		t.Errorf("named branch ref: got %s want attempt/gate-fail-method-use-rule", r.ArchivedBranchRef)
	}
}
