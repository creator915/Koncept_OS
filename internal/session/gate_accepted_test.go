package session

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestGate_AcceptedEvidenceRequired_FailsWhenMissing verifies the new
// [accepted-evidence-required] rule fires when an object is confirmed
// but has no accepted evidence file.
func TestGate_AcceptedEvidenceRequired_FailsWhenMissing(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "K", "sessions")
	graphPath := filepath.Join(root, "K", "graph.json")

	if _, err := Create(sessionDir, "s_root", "", "root", Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_root", StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := SetArchitecture(sessionDir, "s_root", "Sub-modules: Op."); err != nil {
		t.Fatal(err)
	}

	// Lay down impl + base evidence — same shape as the existing
	// TestGate_RootDeliverPassesWhenAllConfirmed fixture.
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
	evidenceDir := filepath.Join(root, ".kcpos", "typecalc")
	writeEvidenceBundle(t, evidenceDir, "Op", map[string]any{
		"test": map[string]any{"kind": "test", "lang": "Go", "ok": true},
		// NOT writing accepted section — the gate must FAIL.
	})

	cwdRestore := mustChdir(t, root)
	defer cwdRestore()

	r, err := CheckGate(sessionDir, graphPath, "", "s_root")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "FAIL" {
		t.Fatalf("expected FAIL with missing accepted evidence, got %s", r.Status)
	}
	if !hasIssueWithPrefix(r.Issues, "[accepted-evidence-required]") {
		t.Fatalf("expected [accepted-evidence-required] issue, got: %v", r.Issues)
	}
}

// TestGate_AcceptedEvidenceRequired_FailsOnVerdictFail verifies that a
// recorded review verdict of `fail` ALSO fails the gate, not just a
// missing record.
func TestGate_AcceptedEvidenceRequired_FailsOnVerdictFail(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "K", "sessions")
	graphPath := filepath.Join(root, "K", "graph.json")

	if _, err := Create(sessionDir, "s_root", "", "root", Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_root", StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := SetArchitecture(sessionDir, "s_root", "x"); err != nil {
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
	evidenceDir := filepath.Join(root, ".kcpos", "typecalc")
	writeEvidenceBundle(t, evidenceDir, "Op", map[string]any{
		"test": map[string]any{"kind": "test", "lang": "Go", "ok": true},
		"accepted": map[string]any{
			"ok":             false,
			"reasonableness": map[string]any{"verdict": "fail", "reasons": []string{"intent says transform input but impl returns input unchanged"}, "confidence": 0.9},
		},
	})

	cwdRestore := mustChdir(t, root)
	defer cwdRestore()

	r, err := CheckGate(sessionDir, graphPath, "", "s_root")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "FAIL" {
		t.Fatalf("expected FAIL with verdict=fail evidence, got %s", r.Status)
	}
	// Issue must surface the concrete reason so the agent can act.
	found := false
	for _, iss := range r.Issues {
		if strings.HasPrefix(iss, "[accepted-evidence-required]") &&
			strings.Contains(iss, "intent says transform input but impl returns input unchanged") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected reason to be quoted in issue, got: %v", r.Issues)
	}
}

func hasIssueWithPrefix(issues []string, prefix string) bool {
	for _, i := range issues {
		if strings.HasPrefix(i, prefix) {
			return true
		}
	}
	return false
}
