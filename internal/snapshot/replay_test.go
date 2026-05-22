package snapshot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Phase 3 e2e: write files into a source workdir capturing
// pre/post-diff side_effects, then replay into a clean target and
// verify the file tree is byte-identical.
func TestReplay_RoundTrip_FileWritesAndDeletes(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)

	// Simulate a sequence of tool calls.
	// Step 1: write main.c (file ADDED).
	preSnap, _ := s.TakeWorkdir()
	_ = os.WriteFile(filepath.Join(srcDir, "main.c"), []byte("v1"), 0o644)
	postSnap, _ := s.TakeWorkdir()
	effects, _ := s.DiffToSideEffects(preSnap.Diff(postSnap))
	args, _ := json.Marshal(map[string]string{"path": "main.c", "content": "v1"})
	if _, err := s.Append(EventTypeToolExec, ToolExecEvent{
		Tool: "write_file", Args: args, Result: "wrote 2 bytes", SideEffects: effects,
	}); err != nil {
		t.Fatal(err)
	}

	// Step 2: modify main.c (file MODIFIED).
	preSnap, _ = s.TakeWorkdir()
	_ = os.WriteFile(filepath.Join(srcDir, "main.c"), []byte("v2"), 0o644)
	postSnap, _ = s.TakeWorkdir()
	effects, _ = s.DiffToSideEffects(preSnap.Diff(postSnap))
	args, _ = json.Marshal(map[string]string{"path": "main.c"})
	if _, err := s.Append(EventTypeToolExec, ToolExecEvent{
		Tool: "edit", Args: args, Result: "wrote 2 bytes", SideEffects: effects,
	}); err != nil {
		t.Fatal(err)
	}

	// Step 3: write nested K/graph.json (file in subdir).
	preSnap, _ = s.TakeWorkdir()
	_ = os.MkdirAll(filepath.Join(srcDir, "K"), 0o755)
	_ = os.WriteFile(filepath.Join(srcDir, "K", "graph.json"), []byte(`{"version":1}`), 0o644)
	postSnap, _ = s.TakeWorkdir()
	effects, _ = s.DiffToSideEffects(preSnap.Diff(postSnap))
	args, _ = json.Marshal(map[string]string{"path": "K/graph.json"})
	if _, err := s.Append(EventTypeToolExec, ToolExecEvent{
		Tool: "graph_create_object", Args: args, Result: "ok", SideEffects: effects,
	}); err != nil {
		t.Fatal(err)
	}

	// Step 4: delete main.c (file DELETED).
	preSnap, _ = s.TakeWorkdir()
	_ = os.Remove(filepath.Join(srcDir, "main.c"))
	postSnap, _ = s.TakeWorkdir()
	effects, _ = s.DiffToSideEffects(preSnap.Diff(postSnap))
	args, _ = json.Marshal(map[string]string{"path": "main.c"})
	if _, err := s.Append(EventTypeToolExec, ToolExecEvent{
		Tool: "delete", Args: args, Result: "removed", SideEffects: effects,
	}); err != nil {
		t.Fatal(err)
	}

	// Replay into a fresh target workdir.
	targetDir := t.TempDir()
	applied, err := s.Replay(ReplayOptions{TargetWorkdir: targetDir, CleanFirst: true})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if applied != 4 {
		t.Errorf("expected 4 events applied, got %d", applied)
	}

	// Verify target matches the FINAL source state:
	//   main.c → deleted
	//   K/graph.json → present with v1 content
	if _, err := os.Stat(filepath.Join(targetDir, "main.c")); !os.IsNotExist(err) {
		t.Errorf("main.c should have been deleted in target, got err=%v", err)
	}
	got, err := os.ReadFile(filepath.Join(targetDir, "K", "graph.json"))
	if err != nil {
		t.Fatalf("K/graph.json missing in target: %v", err)
	}
	if string(got) != `{"version":1}` {
		t.Errorf("K/graph.json content mismatch: got %q want %q", got, `{"version":1}`)
	}
}

// Replay refuses to clobber the source workdir — would destroy
// captured state. Hard-fail with a clear error.
func TestReplay_RefusesSourceWorkdirAsTarget(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)
	_, err := s.Replay(ReplayOptions{TargetWorkdir: srcDir})
	if err == nil {
		t.Fatal("expected error when target == source")
	}
	if !strings.Contains(err.Error(), "clobber") && !strings.Contains(err.Error(), "differ") {
		t.Errorf("error must explain why same-dir is rejected, got: %v", err)
	}
}

// Replay with empty TargetWorkdir is a usage error.
func TestReplay_EmptyTargetIsRejected(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)
	_, err := s.Replay(ReplayOptions{})
	if err == nil {
		t.Fatal("expected error when TargetWorkdir empty")
	}
}

// validReplayPath: reject absolute paths, .. traversal, empty paths.
// Critical security guard for replay path safety.
func TestReplay_PathSafetyGuards(t *testing.T) {
	for _, p := range []string{
		"",
		"/etc/passwd",
		"../escape",
		"foo/../../../bar",
	} {
		if err := validReplayPath(p); err == nil {
			t.Errorf("validReplayPath(%q) must reject, allowed", p)
		}
	}
	// Legit paths must pass.
	for _, p := range []string{
		"main.c",
		"K/graph.json",
		"src/foo/bar.c",
	} {
		if err := validReplayPath(p); err != nil {
			t.Errorf("validReplayPath(%q) must accept, rejected: %v", p, err)
		}
	}
}

// StopAt mid-chain: replay halts at the named event, target reflects
// the partial state. Enables "replay up to milestone X" for
// rollback workflows (Phase 5).
func TestReplay_StopAtMidChain(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)

	// 3 sequential file writes.
	var ids []string
	for i, body := range []string{"v1", "v2", "v3"} {
		_ = i
		preSnap, _ := s.TakeWorkdir()
		_ = os.WriteFile(filepath.Join(srcDir, "main.c"), []byte(body), 0o644)
		postSnap, _ := s.TakeWorkdir()
		effects, _ := s.DiffToSideEffects(preSnap.Diff(postSnap))
		ev, err := s.Append(EventTypeToolExec, ToolExecEvent{
			Tool: "write_file", SideEffects: effects,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, ev.ID)
	}

	// Replay up to the SECOND event — target must have v2, not v3.
	targetDir := t.TempDir()
	if _, err := s.Replay(ReplayOptions{
		TargetWorkdir: targetDir, CleanFirst: true, StopAt: ids[1],
	}); err != nil {
		t.Fatalf("Replay StopAt: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(targetDir, "main.c"))
	if string(got) != "v2" {
		t.Errorf("StopAt at event 2 should yield v2, got %q", got)
	}
}

// Read-only tool call (no diff) → ToolExecEvent with empty
// SideEffects. Replay just walks past it without touching the
// workdir.
func TestReplay_ReadOnlyToolCallProducesNoSideEffects(t *testing.T) {
	srcDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(srcDir, "main.c"), []byte("body"), 0o644)
	s := NewSnapshotter(srcDir)

	// pre-snapshot = post-snapshot (no mutation between).
	preSnap, _ := s.TakeWorkdir()
	postSnap, _ := s.TakeWorkdir()
	effects, _ := s.DiffToSideEffects(preSnap.Diff(postSnap))
	if len(effects) != 0 {
		t.Errorf("identity diff must yield no effects, got %v", effects)
	}
}

// StopAt with a sha that doesn't exist in the chain must hard-fail.
// Silently walking through to tip would leave the target in a state
// for a different point in history with no warning — the caller
// almost certainly expected "stop at this exact event".
func TestReplay_StopAtMissingErrors(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)
	// Record one real event so the chain is non-empty.
	_ = os.WriteFile(filepath.Join(srcDir, "main.c"), []byte("v1"), 0o644)
	preSnap, _ := s.TakeWorkdir()
	postSnap, _ := s.TakeWorkdir()
	effects, _ := s.DiffToSideEffects(preSnap.Diff(postSnap))
	_, _ = s.Append(EventTypeToolExec, ToolExecEvent{
		Tool: "noop", SideEffects: effects,
	})

	targetDir := t.TempDir()
	bogusSha := strings.Repeat("f", 64)
	_, err := s.Replay(ReplayOptions{
		TargetWorkdir: targetDir, CleanFirst: true, StopAt: bogusSha,
	})
	if err == nil {
		t.Fatal("expected error when StopAt is not in chain")
	}
	if !strings.Contains(err.Error(), "StopAt") || !strings.Contains(err.Error(), "not found") {
		t.Errorf("error must name StopAt and 'not found': %v", err)
	}
}

// Replay an archived branch by passing its tip as StopAt. After
// Rollback, the failed branch's events still live in the log but
// aren't on the linear chain from current tip. The old
// childOf-map walk would have missed them entirely; the new
// WalkFrom-based walk follows each tip's ancestry unambiguously.
//
// Setup: a → b → c (failed branch, will be archived); rollback to b;
// new branch b → d. Verify replay --to c reconstructs the archived
// branch's final state, NOT the new branch's.
func TestReplay_ArchivedBranchReachable(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)

	// a: write v1
	pre, _ := s.TakeWorkdir()
	_ = os.WriteFile(filepath.Join(srcDir, "main.c"), []byte("v1"), 0o644)
	post, _ := s.TakeWorkdir()
	effects, _ := s.DiffToSideEffects(pre.Diff(post))
	_, _ = s.Append(EventTypeToolExec, ToolExecEvent{Tool: "w", SideEffects: effects})

	// b: write v2
	pre, _ = s.TakeWorkdir()
	_ = os.WriteFile(filepath.Join(srcDir, "main.c"), []byte("v2"), 0o644)
	post, _ = s.TakeWorkdir()
	effects, _ = s.DiffToSideEffects(pre.Diff(post))
	b, _ := s.Append(EventTypeToolExec, ToolExecEvent{Tool: "w", SideEffects: effects})

	// c: write v3-failed (will become archived branch tip)
	pre, _ = s.TakeWorkdir()
	_ = os.WriteFile(filepath.Join(srcDir, "main.c"), []byte("v3-failed"), 0o644)
	post, _ = s.TakeWorkdir()
	effects, _ = s.DiffToSideEffects(pre.Diff(post))
	c, _ := s.Append(EventTypeToolExec, ToolExecEvent{Tool: "w", SideEffects: effects})

	// Rollback to b. Now: tip=b, attempt/1=c.
	_, err := s.Rollback(b.ID, "")
	if err != nil {
		t.Fatal(err)
	}

	// Append d on the new branch: write v4.
	pre, _ = s.TakeWorkdir()
	_ = os.WriteFile(filepath.Join(srcDir, "main.c"), []byte("v4-new"), 0o644)
	post, _ = s.TakeWorkdir()
	effects, _ = s.DiffToSideEffects(pre.Diff(post))
	_, _ = s.Append(EventTypeToolExec, ToolExecEvent{Tool: "w", SideEffects: effects})

	// Replay archived branch tip (c) → target should have v3-failed.
	archivedTargetDir := t.TempDir()
	if _, err := s.Replay(ReplayOptions{
		TargetWorkdir: archivedTargetDir, CleanFirst: true, StopAt: c.ID,
	}); err != nil {
		t.Fatalf("replay archived branch: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(archivedTargetDir, "main.c"))
	if string(got) != "v3-failed" {
		t.Errorf("archived branch replay should yield v3-failed, got %q", got)
	}

	// Replay current tip (no StopAt) → target should have v4-new.
	newTargetDir := t.TempDir()
	if _, err := s.Replay(ReplayOptions{
		TargetWorkdir: newTargetDir, CleanFirst: true,
	}); err != nil {
		t.Fatalf("replay current tip: %v", err)
	}
	got, _ = os.ReadFile(filepath.Join(newTargetDir, "main.c"))
	if string(got) != "v4-new" {
		t.Errorf("current tip replay should yield v4-new, got %q", got)
	}
}

// Unknown side_effect Kind is a hard error — guards against future
// schema additions silently corrupting replay output. The error
// message must point at the forward-compat strategy.
func TestReplay_UnknownSideEffectKindIsHardError(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)

	rawEffect, _ := json.Marshal(map[string]string{"weird": "yes"})
	se := SideEffect{Kind: "future.unknown.kind", Payload: rawEffect}
	if _, err := s.Append(EventTypeToolExec, ToolExecEvent{
		Tool: "ghost", SideEffects: []SideEffect{se},
	}); err != nil {
		t.Fatal(err)
	}

	targetDir := t.TempDir()
	_, err := s.Replay(ReplayOptions{TargetWorkdir: targetDir, CleanFirst: true})
	if err == nil {
		t.Fatal("expected hard error on unknown kind")
	}
	if !strings.Contains(err.Error(), "unknown side_effect kind") && !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("error must explain forward-compat: %v", err)
	}
}
