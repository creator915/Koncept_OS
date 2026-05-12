package session

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/graph"
)

// 5.1 gate enhancements: 4 new checks. Each test validates ONE rule fires
// in isolation. Setup is shared via a confirmedRootFixture builder that
// puts the graph + session in a state that PASSES every other gate so the
// failure is unambiguously the rule under test.

func confirmedRootFixture(t *testing.T) (root, sessionDir, graphPath string) {
	t.Helper()
	root = t.TempDir()
	sessionDir = filepath.Join(root, "K", "sessions")
	graphPath = filepath.Join(root, "K", "graph.json")

	if _, err := Create(sessionDir, "s_root", "", "root", Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_root", StatusActive); err != nil {
		t.Fatal(err)
	}

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

	// Drop typecalc evidence (kind=test, ok=true, lang=Go) so 5.1c +
	// typecalc-test-required pass. v9.0: written as the unified bundle.
	evDir := filepath.Join(root, ".kcpos", "typecalc")
	writeEvidenceBundle(t, evDir, "Op", map[string]any{
		"test":     map[string]any{"kind": "test", "lang": "Go", "ok": true},
		"accepted": map[string]any{"ok": true, "reasonableness": map[string]any{"verdict": "pass", "reasons": []string{"fixture"}, "confidence": 1.0}},
	})
	// Fix 4: architecture-non-empty gate also needs satisfaction so
	// individual gate-test cases test only the rule under each test.
	if _, err := SetArchitecture(sessionDir, "s_root", "stub architecture for tests"); err != nil {
		t.Fatal(err)
	}
	return root, sessionDir, graphPath
}

func TestGate_TypecalcEvidencePassing_Fails_OnOkFalse(t *testing.T) {
	root, sessionDir, graphPath := confirmedRootFixture(t)
	// Overwrite evidence with ok=false
	evDir := filepath.Join(root, ".kcpos", "typecalc")
	writeEvidenceBundle(t, evDir, "Op", map[string]any{
		"test":     map[string]any{"kind": "test", "lang": "Go", "ok": false},
		"accepted": map[string]any{"ok": true, "reasonableness": map[string]any{"verdict": "pass", "reasons": []string{"fixture"}, "confidence": 1.0}},
	})
	cwdRestore := mustChdir(t, root)
	defer cwdRestore()

	r, err := CheckGate(sessionDir, graphPath, "", "s_root")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, issue := range r.Issues {
		if strings.Contains(issue, "typecalc-evidence-passing") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected [typecalc-evidence-passing] issue, got: %v", r.Issues)
	}
}

func TestGate_TypecalcTestRequired_Fails_OnCompileOnly(t *testing.T) {
	root, sessionDir, graphPath := confirmedRootFixture(t)
	// Replace test evidence with compile-only — Go has a runner so this
	// should be rejected.
	evDir := filepath.Join(root, ".kcpos", "typecalc")
	// Overwrite without an accepted section so the rule-under-test
	// (typecalc-test-required) fires before accepted-evidence-required.
	writeEvidenceBundle(t, evDir, "Op", map[string]any{
		"compile":  map[string]any{"kind": "compile", "lang": "Go", "ok": true},
		"accepted": map[string]any{"ok": true, "reasonableness": map[string]any{"verdict": "pass", "reasons": []string{"fixture"}, "confidence": 1.0}},
	})
	cwdRestore := mustChdir(t, root)
	defer cwdRestore()

	r, _ := CheckGate(sessionDir, graphPath, "", "s_root")
	found := false
	for _, issue := range r.Issues {
		if strings.Contains(issue, "typecalc-test-required") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected [typecalc-test-required] for Go compile-only, got: %v", r.Issues)
	}
}

func TestGate_TypecalcTestRequired_Allows_CompileOnly_ForUntestableLang(t *testing.T) {
	root, sessionDir, graphPath := confirmedRootFixture(t)
	// Java has no in-tree test runner — compile-only evidence is fine.
	evDir := filepath.Join(root, ".kcpos", "typecalc")
	writeEvidenceBundle(t, evDir, "Op", map[string]any{
		"compile":  map[string]any{"kind": "compile", "lang": "Java", "ok": true},
		"accepted": map[string]any{"ok": true, "reasonableness": map[string]any{"verdict": "pass", "reasons": []string{"fixture"}, "confidence": 1.0}},
	})
	cwdRestore := mustChdir(t, root)
	defer cwdRestore()

	r, _ := CheckGate(sessionDir, graphPath, "", "s_root")
	for _, issue := range r.Issues {
		if strings.Contains(issue, "typecalc-test-required") {
			t.Fatalf("Java compile-only should be accepted, got: %v", r.Issues)
		}
	}
}

func TestGate_ProducesOrMutatesNonEmpty_Fails_WhenBothEmpty(t *testing.T) {
	root, sessionDir, graphPath := confirmedRootFixture(t)
	g, err := graph.LoadOrInit(graphPath)
	if err != nil {
		t.Fatal(err)
	}
	g.Objects["Op"].Produces = []string{}
	g.Objects["Op"].Mutates = []string{}
	if err := graph.Save(graphPath, g); err != nil {
		t.Fatal(err)
	}
	cwdRestore := mustChdir(t, root)
	defer cwdRestore()

	r, _ := CheckGate(sessionDir, graphPath, "", "s_root")
	found := false
	for _, issue := range r.Issues {
		if strings.Contains(issue, "produces-or-mutates-non-empty") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected [produces-or-mutates-non-empty], got: %v", r.Issues)
	}
}

func TestGate_ProducesOrMutatesNonEmpty_Passes_WhenOnlyMutates(t *testing.T) {
	root, sessionDir, graphPath := confirmedRootFixture(t)
	g, err := graph.LoadOrInit(graphPath)
	if err != nil {
		t.Fatal(err)
	}
	g.Objects["Op"].Produces = []string{}
	g.Objects["Op"].Mutates = []string{"a"}
	if err := graph.Save(graphPath, g); err != nil {
		t.Fatal(err)
	}
	cwdRestore := mustChdir(t, root)
	defer cwdRestore()

	r, _ := CheckGate(sessionDir, graphPath, "", "s_root")
	for _, issue := range r.Issues {
		if strings.Contains(issue, "produces-or-mutates-non-empty") {
			t.Fatalf("only-mutates should pass, got: %v", r.Issues)
		}
	}
}

func TestGate_AttrsBackfilled_Fails_WhenProducedAttrDeclared(t *testing.T) {
	root, sessionDir, graphPath := confirmedRootFixture(t)
	g, err := graph.LoadOrInit(graphPath)
	if err != nil {
		t.Fatal(err)
	}
	// Demote attribute back to declared — confirmed object produces it.
	g.Attributes["a"].Status = graph.StatusDeclared
	if err := graph.Save(graphPath, g); err != nil {
		t.Fatal(err)
	}
	cwdRestore := mustChdir(t, root)
	defer cwdRestore()

	r, _ := CheckGate(sessionDir, graphPath, "", "s_root")
	found := false
	for _, issue := range r.Issues {
		if strings.Contains(issue, "attrs-backfilled") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected [attrs-backfilled], got: %v", r.Issues)
	}
}

func TestGate_OutputsTestsNonEmpty_Fails_WhenTestableConfirmedAndEmpty(t *testing.T) {
	// Post-Fix-2: rule only fires if at least one confirmed object is in
	// a testable language (Go / TS / JS / Python). Provide such a graph
	// + evidence (kind=compile only, so tests stays empty after aggregate).
	root, sessionDir, graphPath := confirmedRootFixture(t)
	// Add a child + finish it so the children-finished rule passes.
	if _, err := Create(sessionDir, "s_child", "s_root", "child", Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_child", StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_child", StatusFinished); err != nil {
		t.Fatal(err)
	}
	// Lay down compile-only evidence (kind=compile) for Op so:
	//  - anyConfirmedTestable returns true (lang=Go is testable)
	//  - aggregate's tests stays empty (Fix 2 only counts kind=test)
	evDir := filepath.Join(root, ".kcpos", "typecalc")
	writeEvidenceBundle(t, evDir, "Op", map[string]any{
		"compile":  map[string]any{"kind": "compile", "lang": "Go", "ok": true},
		"accepted": map[string]any{"ok": true, "reasonableness": map[string]any{"verdict": "pass", "reasons": []string{"fixture"}, "confidence": 1.0}},
	})
	cwdRestore := mustChdir(t, root)
	defer cwdRestore()

	r, _ := CheckGate(sessionDir, graphPath, "", "s_root")
	found := false
	for _, issue := range r.Issues {
		if strings.Contains(issue, "outputs-tests-non-empty") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected [outputs-tests-non-empty] (testable + empty tests), got: %v", r.Issues)
	}
}

func TestGate_OutputsTestsNonEmpty_Skips_WhenAllUntestable(t *testing.T) {
	// All confirmed objects are in untestable languages (Java) — the rule
	// must NOT fire.
	root, sessionDir, graphPath := confirmedRootFixture(t)
	if _, err := Create(sessionDir, "s_child", "s_root", "child", Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_child", StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_child", StatusFinished); err != nil {
		t.Fatal(err)
	}
	evDir := filepath.Join(root, ".kcpos", "typecalc")
	writeEvidenceBundle(t, evDir, "Op", map[string]any{
		"compile":  map[string]any{"kind": "compile", "lang": "Java", "ok": true},
		"accepted": map[string]any{"ok": true, "reasonableness": map[string]any{"verdict": "pass", "reasons": []string{"fixture"}, "confidence": 1.0}},
	})
	cwdRestore := mustChdir(t, root)
	defer cwdRestore()

	r, _ := CheckGate(sessionDir, graphPath, "", "s_root")
	for _, issue := range r.Issues {
		if strings.Contains(issue, "outputs-tests-non-empty") {
			t.Fatalf("rule should not fire when all confirmed objects are untestable: %v", r.Issues)
		}
	}
}
