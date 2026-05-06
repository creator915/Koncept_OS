package session

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/creator915/Koncept_OS/internal/graph"
)

// setupCaptureFixture builds a temp dir, creates an active session, sets it
// as focus, and returns paths suitable for graph save/load + session ops.
func setupCaptureFixture(t *testing.T, sessionID string) (sessionDir, graphPath string) {
	t.Helper()
	root := t.TempDir()
	sessionDir = filepath.Join(root, "K", "sessions")
	graphPath = filepath.Join(root, "K", "graph.json")

	if _, err := Create(sessionDir, sessionID, "", "test session", Input{}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := SetStatus(sessionDir, sessionID, StatusActive); err != nil {
		t.Fatalf("SetStatus active: %v", err)
	}
	if err := SetFocus(sessionDir, sessionID); err != nil {
		t.Fatalf("SetFocus: %v", err)
	}
	return sessionDir, graphPath
}

func TestCaptureDiff_AddedAttribute(t *testing.T) {
	sessionDir, graphPath := setupCaptureFixture(t, "s_x")

	g := graph.NewGraph()
	if err := graph.Save(graphPath, g); err != nil {
		t.Fatal(err)
	}
	before := g.Clone()
	if err := g.AddAttribute("a", graph.NewAttribute("defs/a.ts", "intent")); err != nil {
		t.Fatal(err)
	}
	if err := graph.Save(graphPath, g); err != nil {
		t.Fatal(err)
	}
	if err := CaptureDiff(sessionDir, before, g); err != nil {
		t.Fatalf("CaptureDiff: %v", err)
	}

	s, _ := Load(sessionDir, "s_x")
	if _, ok := s.Output.GraphDiff.Added.Attributes["a"]; !ok {
		t.Errorf("expected 'a' in graphDiff.added.attributes; diff=%+v", s.Output.GraphDiff.Added)
	}
}

func TestCaptureDiff_NoFocus_IsNoOp(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "K", "sessions")

	before := graph.NewGraph()
	after := before.Clone()
	after.AddAttribute("a", graph.NewAttribute("defs/a.ts", "intent"))

	// no SetFocus call → CaptureDiff should silently skip
	if err := CaptureDiff(sessionDir, before, after); err != nil {
		t.Errorf("CaptureDiff with no focus should not error, got %v", err)
	}
}

func TestCaptureDiff_NotActive_IsNoOp(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "K", "sessions")
	if _, err := Create(sessionDir, "s_x", "", "task", Input{}); err != nil {
		t.Fatal(err)
	}
	if err := SetFocus(sessionDir, "s_x"); err != nil {
		t.Fatal(err)
	}
	// Status is still waiting (not active), capture should skip.

	before := graph.NewGraph()
	after := before.Clone()
	after.AddAttribute("a", graph.NewAttribute("defs/a.ts", "intent"))
	if err := CaptureDiff(sessionDir, before, after); err != nil {
		t.Errorf("CaptureDiff: %v", err)
	}
	s, _ := Load(sessionDir, "s_x")
	if len(s.Output.GraphDiff.Added.Attributes) != 0 {
		t.Errorf("waiting session should not capture, got %+v", s.Output.GraphDiff.Added.Attributes)
	}
}

func TestCaptureDiff_AddThenModify_RefreshesAddedSnapshot(t *testing.T) {
	sessionDir, graphPath := setupCaptureFixture(t, "s_x")
	_ = graphPath

	// Step 1: add attribute "a" (status: declared)
	g := graph.NewGraph()
	before1 := g.Clone()
	g.AddAttribute("a", graph.NewAttribute("defs/a.ts", "v1"))
	if err := CaptureDiff(sessionDir, before1, g); err != nil {
		t.Fatal(err)
	}

	// Step 2: modify the same attribute (intent -> v2)
	before2 := g.Clone()
	g.MergeAttribute("a", map[string]any{"intent": "v2"})
	if err := CaptureDiff(sessionDir, before2, g); err != nil {
		t.Fatal(err)
	}

	s, _ := Load(sessionDir, "s_x")
	if _, ok := s.Output.GraphDiff.Added.Attributes["a"]; !ok {
		t.Fatalf("'a' should still be in added (it was added in this session); diff=%+v", s.Output.GraphDiff)
	}
	if _, ok := s.Output.GraphDiff.Modified.Attributes["a"]; ok {
		t.Errorf("'a' should NOT be in modified (it was added this session, refreshed in place)")
	}
}

func TestCaptureDiff_ModifiedAttribute_KeepsOriginalBefore(t *testing.T) {
	sessionDir, _ := setupCaptureFixture(t, "s_x")

	// Pre-existing attribute (added before our session focus)
	g := graph.NewGraph()
	g.AddAttribute("a", graph.NewAttribute("defs/a.ts", "original"))

	// Step 1: modify a (intent -> v1)
	before1 := g.Clone()
	g.MergeAttribute("a", map[string]any{"intent": "v1"})
	if err := CaptureDiff(sessionDir, before1, g); err != nil {
		t.Fatal(err)
	}

	// Step 2: modify again (intent -> v2). Modified.before should still be "original".
	before2 := g.Clone()
	g.MergeAttribute("a", map[string]any{"intent": "v2"})
	if err := CaptureDiff(sessionDir, before2, g); err != nil {
		t.Fatal(err)
	}

	s, _ := Load(sessionDir, "s_x")
	mod, ok := s.Output.GraphDiff.Modified.Attributes["a"]
	if !ok {
		t.Fatalf("'a' should be in modified")
	}
	var beforeA, afterA graph.Attribute
	if err := json.Unmarshal(mod.Before, &beforeA); err != nil {
		t.Fatalf("decode before: %v · raw=%s", err, string(mod.Before))
	}
	if err := json.Unmarshal(mod.After, &afterA); err != nil {
		t.Fatalf("decode after: %v", err)
	}
	if beforeA.Intent != "original" {
		t.Errorf("before should preserve original intent, got %q", beforeA.Intent)
	}
	if afterA.Intent != "v2" {
		t.Errorf("after should have latest intent v2, got %q", afterA.Intent)
	}
}

func TestRollback_UndoesAdds(t *testing.T) {
	sessionDir, graphPath := setupCaptureFixture(t, "s_x")

	// Initial state: empty graph saved.
	if err := graph.Save(graphPath, graph.NewGraph()); err != nil {
		t.Fatal(err)
	}

	// Mutate via mutateGraph-equivalent flow: load, snapshot, mutate, save, capture.
	g, _ := graph.LoadOrInit(graphPath)
	before := g.Clone()
	g.AddAttribute("a", graph.NewAttribute("defs/a.ts", "intent"))
	g.AddObject("Op", graph.NewObject("defs/Op.ts", "intent"))
	graph.Save(graphPath, g)
	CaptureDiff(sessionDir, before, g)

	// Rollback should undo both additions.
	if _, err := Rollback(sessionDir, graphPath, "s_x"); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	g2, _ := graph.LoadOrInit(graphPath)
	if _, ok := g2.Attributes["a"]; ok {
		t.Errorf("'a' should be gone after rollback")
	}
	if _, ok := g2.Objects["Op"]; ok {
		t.Errorf("'Op' should be gone after rollback")
	}
	if Exists(sessionDir, "s_x") {
		t.Errorf("session JSON should be removed after rollback")
	}
}

func TestRollback_RestoresModifiedBeforeState(t *testing.T) {
	sessionDir, graphPath := setupCaptureFixture(t, "s_x")

	// Pre-existing attribute outside the session
	g := graph.NewGraph()
	g.AddAttribute("a", graph.NewAttribute("defs/a.ts", "original"))
	graph.Save(graphPath, g)

	// Inside the session, modify the attribute
	g, _ = graph.LoadOrInit(graphPath)
	before := g.Clone()
	g.MergeAttribute("a", map[string]any{"intent": "modified-by-session"})
	graph.Save(graphPath, g)
	CaptureDiff(sessionDir, before, g)

	// Rollback should restore "original" intent
	if _, err := Rollback(sessionDir, graphPath, "s_x"); err != nil {
		t.Fatal(err)
	}
	g2, _ := graph.LoadOrInit(graphPath)
	if g2.Attributes["a"].Intent != "original" {
		t.Errorf("intent should be restored to original, got %q", g2.Attributes["a"].Intent)
	}
}

func TestRollback_ChildrenFirst(t *testing.T) {
	sessionDir, graphPath := setupCaptureFixture(t, "s_root")

	// Create a child session under s_root and activate it
	mustCreateChild := func(id, parent string) {
		if _, err := Create(sessionDir, id, parent, "", Input{}); err != nil {
			t.Fatal(err)
		}
		if _, err := SetStatus(sessionDir, id, StatusActive); err != nil {
			t.Fatal(err)
		}
	}
	mustCreateChild("s_child", "s_root")

	// Focus on root, add A
	SetFocus(sessionDir, "s_root")
	g := graph.NewGraph()
	before := g.Clone()
	g.AddAttribute("a", graph.NewAttribute("defs/a.ts", "from-root"))
	graph.Save(graphPath, g)
	CaptureDiff(sessionDir, before, g)

	// Switch focus to child, modify A (changes intent)
	SetFocus(sessionDir, "s_child")
	g2, _ := graph.LoadOrInit(graphPath)
	before2 := g2.Clone()
	g2.MergeAttribute("a", map[string]any{"intent": "from-child"})
	graph.Save(graphPath, g2)
	CaptureDiff(sessionDir, before2, g2)

	// Rollback root: child first (restores A intent to "from-root"), then root (removes A entirely).
	if _, err := Rollback(sessionDir, graphPath, "s_root"); err != nil {
		t.Fatal(err)
	}
	g3, _ := graph.LoadOrInit(graphPath)
	if _, ok := g3.Attributes["a"]; ok {
		t.Errorf("'a' should be gone after rolling back the parent that added it")
	}
	if Exists(sessionDir, "s_root") || Exists(sessionDir, "s_child") {
		t.Errorf("both sessions should be deleted")
	}
}

func TestRollback_ClearsFocus(t *testing.T) {
	sessionDir, graphPath := setupCaptureFixture(t, "s_x")
	if _, err := Rollback(sessionDir, graphPath, "s_x"); err != nil {
		t.Fatal(err)
	}
	cur, _ := GetFocus(sessionDir)
	if cur != "" {
		t.Errorf("focus should be cleared after rolling back the focused session, got %q", cur)
	}
}

func TestSetStatus_FinishedClearsFocus(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "K", "sessions")
	if _, err := Create(sessionDir, "s_x", "", "task", Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_x", StatusActive); err != nil {
		t.Fatal(err)
	}
	if err := SetFocus(sessionDir, "s_x"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_x", StatusFinished); err != nil {
		t.Fatal(err)
	}
	cur, _ := GetFocus(sessionDir)
	if cur != "" {
		t.Errorf("focus should be cleared after finishing the focused session, got %q", cur)
	}
}

