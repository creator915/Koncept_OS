package workflow

import (
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
)

// setupCaptureFixture builds a temp dir, creates an active session, sets it
// as focus, and returns paths suitable for graph save/load + session ops.
func setupCaptureFixture(t *testing.T, sessionID string) (sessionDir, graphPath string) {
	t.Helper()
	root := t.TempDir()
	sessionDir = filepath.Join(root, "K", "sessions")
	graphPath = filepath.Join(root, "K", "graph.json")

	if _, err := Create(sessionDir, sessionID, "", "test session", session.Input{}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := SetStatus(sessionDir, sessionID, session.StatusActive); err != nil {
		t.Fatalf("SetStatus active: %v", err)
	}
	if err := persistence.SetFocus(sessionDir, sessionID); err != nil {
		t.Fatalf("persistence.SetFocus: %v", err)
	}
	return sessionDir, graphPath
}

func TestCaptureDiff_AddedAttribute(t *testing.T) {
	sessionDir, graphPath := setupCaptureFixture(t, "s_x")

	g := graph.NewGraph()
	if err := persistence.SaveGraph(graphPath, g); err != nil {
		t.Fatal(err)
	}
	before := g.Clone()
	if err := g.AddAttribute("a", graph.NewAttribute("defs/a.ts", "intent")); err != nil {
		t.Fatal(err)
	}
	if err := persistence.SaveGraph(graphPath, g); err != nil {
		t.Fatal(err)
	}
	if err := CaptureDiff(sessionDir, before, g); err != nil {
		t.Fatalf("CaptureDiff: %v", err)
	}

	s, _ := persistence.LoadSession(sessionDir, "s_x")
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

	// no persistence.SetFocus call → CaptureDiff should silently skip
	if err := CaptureDiff(sessionDir, before, after); err != nil {
		t.Errorf("CaptureDiff with no focus should not error, got %v", err)
	}
}

func TestCaptureDiff_NotActive_IsNoOp(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "K", "sessions")
	if _, err := Create(sessionDir, "s_x", "", "task", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if err := persistence.SetFocus(sessionDir, "s_x"); err != nil {
		t.Fatal(err)
	}
	// session.Status is still waiting (not active), capture should skip.

	before := graph.NewGraph()
	after := before.Clone()
	after.AddAttribute("a", graph.NewAttribute("defs/a.ts", "intent"))
	if err := CaptureDiff(sessionDir, before, after); err != nil {
		t.Errorf("CaptureDiff: %v", err)
	}
	s, _ := persistence.LoadSession(sessionDir, "s_x")
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

	s, _ := persistence.LoadSession(sessionDir, "s_x")
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

	s, _ := persistence.LoadSession(sessionDir, "s_x")
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
	if err := persistence.SaveGraph(graphPath, graph.NewGraph()); err != nil {
		t.Fatal(err)
	}

	// Mutate via mutateGraph-equivalent flow: load, snapshot, mutate, save, capture.
	g, _ := persistence.LoadGraphOrInit(graphPath)
	before := g.Clone()
	g.AddAttribute("a", graph.NewAttribute("defs/a.ts", "intent"))
	g.AddObject("Op", graph.NewObject("defs/Op.ts", "intent"))
	persistence.SaveGraph(graphPath, g)
	CaptureDiff(sessionDir, before, g)

	// Rollback should undo both additions.
	if _, err := Rollback(sessionDir, graphPath, "s_x"); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	g2, _ := persistence.LoadGraphOrInit(graphPath)
	if _, ok := g2.Attributes["a"]; ok {
		t.Errorf("'a' should be gone after rollback")
	}
	if _, ok := g2.Objects["Op"]; ok {
		t.Errorf("'Op' should be gone after rollback")
	}
	if persistence.ExistsSession(sessionDir, "s_x") {
		t.Errorf("session JSON should be removed after rollback")
	}
}

func TestRollback_RestoresModifiedBeforeState(t *testing.T) {
	sessionDir, graphPath := setupCaptureFixture(t, "s_x")

	// Pre-existing attribute outside the session
	g := graph.NewGraph()
	g.AddAttribute("a", graph.NewAttribute("defs/a.ts", "original"))
	persistence.SaveGraph(graphPath, g)

	// Inside the session, modify the attribute
	g, _ = persistence.LoadGraphOrInit(graphPath)
	before := g.Clone()
	g.MergeAttribute("a", map[string]any{"intent": "modified-by-session"})
	persistence.SaveGraph(graphPath, g)
	CaptureDiff(sessionDir, before, g)

	// Rollback should restore "original" intent
	if _, err := Rollback(sessionDir, graphPath, "s_x"); err != nil {
		t.Fatal(err)
	}
	g2, _ := persistence.LoadGraphOrInit(graphPath)
	if g2.Attributes["a"].Intent != "original" {
		t.Errorf("intent should be restored to original, got %q", g2.Attributes["a"].Intent)
	}
}

func TestRollback_ChildrenFirst(t *testing.T) {
	sessionDir, graphPath := setupCaptureFixture(t, "s_root")

	// Create a child session under s_root and activate it
	mustCreateChild := func(id, parent string) {
		if _, err := Create(sessionDir, id, parent, "", session.Input{}); err != nil {
			t.Fatal(err)
		}
		if _, err := SetStatus(sessionDir, id, session.StatusActive); err != nil {
			t.Fatal(err)
		}
	}
	mustCreateChild("s_child", "s_root")

	// Focus on root, add A
	persistence.SetFocus(sessionDir, "s_root")
	g := graph.NewGraph()
	before := g.Clone()
	g.AddAttribute("a", graph.NewAttribute("defs/a.ts", "from-root"))
	persistence.SaveGraph(graphPath, g)
	CaptureDiff(sessionDir, before, g)

	// Switch focus to child, modify A (changes intent)
	persistence.SetFocus(sessionDir, "s_child")
	g2, _ := persistence.LoadGraphOrInit(graphPath)
	before2 := g2.Clone()
	g2.MergeAttribute("a", map[string]any{"intent": "from-child"})
	persistence.SaveGraph(graphPath, g2)
	CaptureDiff(sessionDir, before2, g2)

	// Rollback root: child first (restores A intent to "from-root"), then root (removes A entirely).
	if _, err := Rollback(sessionDir, graphPath, "s_root"); err != nil {
		t.Fatal(err)
	}
	g3, _ := persistence.LoadGraphOrInit(graphPath)
	if _, ok := g3.Attributes["a"]; ok {
		t.Errorf("'a' should be gone after rolling back the parent that added it")
	}
	if persistence.ExistsSession(sessionDir, "s_root") || persistence.ExistsSession(sessionDir, "s_child") {
		t.Errorf("both sessions should be deleted")
	}
}

func TestGate_RootDeliverFailsOnUnconfirmedObjects(t *testing.T) {
	// A root session with one object in graph that's still 'declared' should fail
	// the §root-deliver rule, even if its graphDiff.added is empty (i.e. focus
	// was never set when the object was created).
	root := t.TempDir()
	sessionDir := filepath.Join(root, "K", "sessions")
	graphPath := filepath.Join(root, "K", "graph.json")

	// Create root session, but keep status active (so gate_check is even relevant)
	if _, err := Create(sessionDir, "s_root", "", "root", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_root", session.StatusActive); err != nil {
		t.Fatal(err)
	}

	// Build a graph with one declared object — note: NOT going through CaptureDiff,
	// so the session's graphDiff is empty. This simulates the "focus set late" bug.
	g := newRootDeliverFixtureGraph(t)
	if err := saveRawGraph(graphPath, g); err != nil {
		t.Fatal(err)
	}

	r, err := CheckGate(sessionDir, graphPath, "", "s_root")
	if err != nil {
		t.Fatalf("CheckGate: %v", err)
	}
	if r.Status != "FAIL" {
		t.Errorf("expected root with declared objects to FAIL gate, got %s · %v", r.Status, r.Issues)
	}
	// Verify the rule that fired is §root-deliver
	foundRule := false
	for _, iss := range r.Issues {
		if contains([]string{"root-deliver"}, ruleOf(iss)) {
			foundRule = true
			break
		}
		// fallback: substring check
		if substr(iss, "[root-deliver]") {
			foundRule = true
			break
		}
	}
	if !foundRule {
		t.Errorf("expected §root-deliver issue, got: %v", r.Issues)
	}
}

func TestGate_RootDeliverPassesWhenAllConfirmed(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "K", "sessions")
	graphPath := filepath.Join(root, "K", "graph.json")

	if _, err := Create(sessionDir, "s_root", "", "root", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_root", session.StatusActive); err != nil {
		t.Fatal(err)
	}

	// Build a confirmed graph with impl files that exist
	implPath := filepath.Join(root, "src", "loader.go")
	if err := mkdirAll(filepath.Dir(implPath)); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(implPath, "package main\n"); err != nil {
		t.Fatal(err)
	}

	g := newConfirmedGraph(t, "src/loader.go")
	if err := saveRawGraph(graphPath, g); err != nil {
		t.Fatal(err)
	}

	// typecalc evidence — every confirmed object on a root graph must
	// have a recorded compile/test plus an accepted reviewer verdict.
	// v9.0: both live in the unified bundle at .kcpos/typecalc/<id>.json.
	evidenceDir := filepath.Join(root, ".kcpos", "typecalc")
	writeEvidenceBundle(t, evidenceDir, "Op", map[string]any{
		"test":     map[string]any{"kind": "test", "lang": "Go", "ok": true},
		"accepted": map[string]any{"ok": true, "reasonableness": map[string]any{"verdict": "pass", "reasons": []string{"fixture"}, "confidence": 1.0}},
	})

	// Fix 4: architecture must be set for root finish gate.
	if _, err := SetArchitecture(sessionDir, "s_root", "Sub-modules: Op. Intermediate vars: a."); err != nil {
		t.Fatal(err)
	}

	// Run gate from cwd=root so relative impl path resolves
	cwdRestore := mustChdir(t, root)
	defer cwdRestore()

	r, err := CheckGate(sessionDir, graphPath, "", "s_root")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "PASS" {
		t.Errorf("confirmed root graph should PASS, got %s · %v", r.Status, r.Issues)
	}
}

func TestGate_NonRootSkipsGlobalCheck(t *testing.T) {
	// A non-root session (has Parent) should NOT get the §root-deliver rule
	// applied — even if the graph has unconfirmed objects.
	root := t.TempDir()
	sessionDir := filepath.Join(root, "K", "sessions")
	graphPath := filepath.Join(root, "K", "graph.json")

	if _, err := Create(sessionDir, "s_root", "", "root", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(sessionDir, "s_child", "s_root", "child", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_root", session.StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_child", session.StatusActive); err != nil {
		t.Fatal(err)
	}

	g := newRootDeliverFixtureGraph(t) // declared object, not confirmed
	if err := saveRawGraph(graphPath, g); err != nil {
		t.Fatal(err)
	}

	r, err := CheckGate(sessionDir, graphPath, "", "s_child")
	if err != nil {
		t.Fatal(err)
	}
	for _, iss := range r.Issues {
		if substr(iss, "[root-deliver]") {
			t.Errorf("non-root session should not trigger root-deliver: %s", iss)
		}
	}
}

func TestRollback_DeletesAddedDefAndImplFiles(t *testing.T) {
	// rollback should delete the impl + def files
	// the session created (those whose ids are in graphDiff.added).
	root := t.TempDir()
	sessionDir := filepath.Join(root, "K", "sessions")
	graphPath := filepath.Join(root, "K", "graph.json")

	// Need cwd=root so the rollback resolves relative paths there.
	restore := mustChdir(t, root)
	defer restore()

	// Pre-create the def + impl files agent will "create" via the session.
	if err := os.MkdirAll(filepath.Join(root, "defs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	defPath := filepath.Join(root, "defs", "Op.ts")
	implPath := filepath.Join(root, "src", "op.go")
	if err := os.WriteFile(defPath, []byte("export type Op = ...\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(implPath, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Active session, focused.
	if _, err := Create(sessionDir, "s_x", "", "task", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_x", session.StatusActive); err != nil {
		t.Fatal(err)
	}
	if err := persistence.SetFocus(sessionDir, "s_x"); err != nil {
		t.Fatal(err)
	}

	// Capture: agent created an attribute (def=defs/a.ts implicit)
	// and object Op (def=defs/Op.ts, impl=src/op.go) under this session.
	g := graph.NewGraph()
	if err := persistence.SaveGraph(graphPath, g); err != nil {
		t.Fatal(err)
	}
	g, _ = persistence.LoadGraphOrInit(graphPath)
	before := g.Clone()
	g.AddAttribute("a", graph.NewAttribute("defs/a.ts", "an attr"))
	implRel := "src/op.go"
	o := graph.NewObject("defs/Op.ts", "an op")
	o.Impl = &implRel
	o.Status = graph.StatusConfirmed
	g.AddObject("Op", o)
	persistence.SaveGraph(graphPath, g)
	CaptureDiff(sessionDir, before, g)

	// Sanity: files are present pre-rollback.
	for _, p := range []string{defPath, implPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("setup error: %s should exist before rollback", p)
		}
	}

	// Rollback s_x.
	if _, err := Rollback(sessionDir, graphPath, "s_x"); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	// def + impl for Op must be gone.
	if _, err := os.Stat(defPath); err == nil {
		t.Errorf("expected defs/Op.ts to be deleted on rollback, still exists")
	}
	if _, err := os.Stat(implPath); err == nil {
		t.Errorf("expected src/op.go to be deleted on rollback, still exists")
	}
	// def for attribute "a" — defs/a.ts was never created in this test, so
	// it shouldn't exist; checking that the rollback didn't error on it.
}

func TestRollback_PreservesFilesOutsideProject(t *testing.T) {
	// Safety: an absolute path or a `..` traversal in def/impl must not
	// trigger a delete outside the project root.
	root := t.TempDir()
	sessionDir := filepath.Join(root, "K", "sessions")
	graphPath := filepath.Join(root, "K", "graph.json")
	restore := mustChdir(t, root)
	defer restore()

	if _, err := Create(sessionDir, "s_x", "", "task", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_x", session.StatusActive); err != nil {
		t.Fatal(err)
	}
	if err := persistence.SetFocus(sessionDir, "s_x"); err != nil {
		t.Fatal(err)
	}

	// Inject an entry with a malicious path — the deleteAddedFiles helper
	// must skip it.
	outsideFile, err := os.CreateTemp("", "kcpos_outside_*.txt")
	if err != nil {
		t.Fatal(err)
	}
	outsidePath := outsideFile.Name()
	outsideFile.Close()
	defer os.Remove(outsidePath)

	g := graph.NewGraph()
	persistence.SaveGraph(graphPath, g)
	g, _ = persistence.LoadGraphOrInit(graphPath)
	before := g.Clone()
	o := graph.NewObject(outsidePath, "evil") // absolute path as def — must be ignored
	g.AddObject("Evil", o)
	persistence.SaveGraph(graphPath, g)
	CaptureDiff(sessionDir, before, g)

	if _, err := Rollback(sessionDir, graphPath, "s_x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outsidePath); err != nil {
		t.Errorf("rollback must NOT delete absolute-path file outside project; got: %v", err)
	}
}

func TestRollback_ClearsFocus(t *testing.T) {
	sessionDir, graphPath := setupCaptureFixture(t, "s_x")
	if _, err := Rollback(sessionDir, graphPath, "s_x"); err != nil {
		t.Fatal(err)
	}
	cur, _ := persistence.GetFocus(sessionDir)
	if cur != "" {
		t.Errorf("focus should be cleared after rolling back the focused session, got %q", cur)
	}
}

func TestSetStatus_FinishedClearsFocus(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "K", "sessions")
	if _, err := Create(sessionDir, "s_x", "", "task", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_x", session.StatusActive); err != nil {
		t.Fatal(err)
	}
	if err := persistence.SetFocus(sessionDir, "s_x"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_x", session.StatusFinished); err != nil {
		t.Fatal(err)
	}
	cur, _ := persistence.GetFocus(sessionDir)
	if cur != "" {
		t.Errorf("focus should be cleared after finishing the focused session, got %q", cur)
	}
}

// --- helpers used by the §root-deliver gate tests above ---

func newRootDeliverFixtureGraph(t *testing.T) *graph.Graph {
	t.Helper()
	g := graph.NewGraph()
	g.AddAttribute("a", graph.NewAttribute("defs/a.ts", "an attr"))
	g.AddObject("Op", graph.NewObject("defs/Op.ts", "an op"))
	g.LinkProduce("Op", "a")
	return g
}

func newConfirmedGraph(t *testing.T, implPath string) *graph.Graph {
	t.Helper()
	g := graph.NewGraph()
	a := graph.NewAttribute(implPath, "an attr")
	a.Status = graph.StatusConfirmed // attrs-backfilled (5.1b) gate requires this
	g.AddAttribute("a", a)
	o := graph.NewObject(implPath, "an op")
	o.Status = graph.StatusConfirmed
	impl := implPath
	o.Impl = &impl
	g.AddObject("Op", o)
	g.LinkProduce("Op", "a")
	return g
}

func saveRawGraph(path string, g *graph.Graph) error {
	return persistence.SaveGraph(path, g)
}

func mkdirAll(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// writeEvidenceBundle is the v9.0 fixture writer for tests that need a
// canonical-shape evidence bundle on disk. Sections is a sparse map:
// only the named sections are populated. Supported keys: "compile",
// "test" — value must be a map with at least kind/lang/ok; "accepted" —
// value must be a map with verdict/ok.
func writeEvidenceBundle(t *testing.T, evidenceDir, objID string, sections map[string]any) {
	t.Helper()
	if err := mkdirAll(evidenceDir); err != nil {
		t.Fatal(err)
	}
	bundle := map[string]any{
		"objectId":  objID,
		"version":   1,
		"updatedAt": "1970-01-01T00:00:00Z",
	}
	for k, v := range sections {
		bundle[k] = v
	}
	raw, _ := json.Marshal(bundle)
	if err := writeFile(filepath.Join(evidenceDir, objID+".json"), string(raw)); err != nil {
		t.Fatal(err)
	}
}

func mustChdir(t *testing.T, dir string) func() {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return func() { _ = os.Chdir(prev) }
}

func substr(s, needle string) bool {
	return indexInBytes([]byte(s), needle) >= 0
}

func ruleOf(_ string) string { return "" }

// indexInBytes is a tiny needle-in-haystack helper used by JSON content
// assertions and substring checks.
func indexInBytes(haystack []byte, needle string) int {
	if len(needle) == 0 {
		return 0
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

