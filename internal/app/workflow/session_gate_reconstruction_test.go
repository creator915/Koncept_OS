package workflow

import (
	"path/filepath"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// 2026-05-21 — Reconstruction-mode evidence equivalence at the gate.
//
// PB-30 batch #6 figlet reached Outer.Checkpointed with 3/3 objects
// status=confirmed (every object's bundle had a passing Characterization
// from equiv_oracle.go), then gate emitted [compile-not-enough] on all
// three because the requiresTestEvidence whitelist excludes C and the
// gate rule didn't recognise Characterization as a substitute for
// kind=test evidence. The fix carves a reconstruction-verified case
// into the switch — same structural pattern as the HTML deliverable
// branch — keyed on LockedCount > 0 in the bundle's Characterization.

// reconScaffold lays down a confirmed object whose evidence shape is:
//   - kind=compile (no test runner ran — reconstruction mode skips it)
//   - characterization with LockedCount > 0
//   - reasonableness verdict pass (matches equiv_oracle's flow)
// The lang knob lets us prove the same carve-out fires for C (not in
// requiresTestEvidence) AND Go (in requiresTestEvidence — same path
// must work because runEquivalenceOracle replaces kind=test for either).
func reconScaffold(t *testing.T, lang string, implName string, lockedCount int) (*GateReport, error) {
	t.Helper()
	root := t.TempDir()
	sessionDir := filepath.Join(root, "K", "sessions")
	graphPath := filepath.Join(root, "K", "graph.json")

	if _, err := Create(sessionDir, "s_root", "", "root", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_root", session.StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := SetArchitecture(sessionDir, "s_root", "Reconstruct executable from black-box probe."); err != nil {
		t.Fatal(err)
	}

	relImpl := "src/" + implName
	implPath := filepath.Join(root, relImpl)
	if err := mkdirAll(filepath.Dir(implPath)); err != nil {
		t.Fatal(err)
	}
	implBody := "int main(){ return 0; }\n"
	if err := writeFile(implPath, implBody); err != nil {
		t.Fatal(err)
	}
	g := newConfirmedGraph(t, relImpl)
	if err := saveRawGraph(graphPath, g); err != nil {
		t.Fatal(err)
	}

	evidenceDir := filepath.Join(root, ".kcpos", "typecalc")
	// kind=compile only — reconstruction mode never wrote a Test section.
	sections := map[string]any{
		"compile":  map[string]any{"kind": "compile", "lang": lang, "ok": true},
		"accepted": map[string]any{"ok": true, "reasonableness": map[string]any{"verdict": "pass", "confidence": 0.95}},
		"characterization": map[string]any{
			"suiteId":     "equiv-Op",
			"lang":        lang,
			"codeHash":    core.HashSource(implBody),
			"lockedCount": lockedCount,
		},
	}
	writeEvidenceBundle(t, evidenceDir, "Op", sections)

	cwdRestore := mustChdir(t, root)
	defer cwdRestore()
	return CheckGate(sessionDir, graphPath, "", "s_root")
}

// TestGate_ReconstructionVerified_BypassesCompileNotEnough: C-impl
// confirmed via equiv_oracle (LockedCount > 0) must PASS the gate even
// though kind=compile and C is outside requiresTestEvidence. This is
// the figlet/cmatrix/tty-clock PB-30 regression.
func TestGate_ReconstructionVerified_BypassesCompileNotEnough(t *testing.T) {
	r, err := reconScaffold(t, "C", "main.c", 12)
	if err != nil {
		t.Fatal(err)
	}
	if hasIssueWithPrefix(r.Issues, "[compile-not-enough]") {
		t.Fatalf("[compile-not-enough] must NOT fire when Characterization with LockedCount>0 is present (behavioral-equivalence oracle IS the test of record), got: %v", r.Issues)
	}
	if r.Status != "PASS" {
		t.Fatalf("expected PASS for a reconstruction-verified C object, got %s: %v", r.Status, r.Issues)
	}
}

// TestGate_ReconstructionVerified_BypassesTypecalcTestRequired: same
// rule, but with lang=Go (which IS in requiresTestEvidence). The
// equiv_oracle path replaces kind=test for any lang in reconstruction
// mode, so the [typecalc-test-required] rule must also be bypassed
// when Characterization is locked.
func TestGate_ReconstructionVerified_BypassesTypecalcTestRequired(t *testing.T) {
	r, err := reconScaffold(t, "Go", "main.go", 12)
	if err != nil {
		t.Fatal(err)
	}
	if hasIssueWithPrefix(r.Issues, "[typecalc-test-required]") {
		t.Fatalf("[typecalc-test-required] must NOT fire when Characterization with LockedCount>0 is present, got: %v", r.Issues)
	}
	if r.Status != "PASS" {
		t.Fatalf("expected PASS for a reconstruction-verified Go object, got %s: %v", r.Status, r.Issues)
	}
}

// TestGate_ReconstructionVerified_GreenfieldStillRequiresTest: sentinel
// — without a Characterization section, a Go object with only kind=compile
// must STILL fail [typecalc-test-required]. The bypass must NOT loosen
// greenfield gating.
func TestGate_ReconstructionVerified_GreenfieldStillRequiresTest(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "K", "sessions")
	graphPath := filepath.Join(root, "K", "graph.json")
	if _, err := Create(sessionDir, "s_root", "", "root", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_root", session.StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := SetArchitecture(sessionDir, "s_root", "Greenfield Go op."); err != nil {
		t.Fatal(err)
	}
	relImpl := "src/main.go"
	implPath := filepath.Join(root, relImpl)
	if err := mkdirAll(filepath.Dir(implPath)); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(implPath, "package main\n"); err != nil {
		t.Fatal(err)
	}
	g := newConfirmedGraph(t, relImpl)
	if err := saveRawGraph(graphPath, g); err != nil {
		t.Fatal(err)
	}
	evidenceDir := filepath.Join(root, ".kcpos", "typecalc")
	sections := map[string]any{
		"compile":  map[string]any{"kind": "compile", "lang": "Go", "ok": true},
		"accepted": map[string]any{"ok": true, "reasonableness": map[string]any{"verdict": "pass", "confidence": 0.95}},
		// NO characterization
	}
	writeEvidenceBundle(t, evidenceDir, "Op", sections)
	cwdRestore := mustChdir(t, root)
	defer cwdRestore()
	r, err := CheckGate(sessionDir, graphPath, "", "s_root")
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssueWithPrefix(r.Issues, "[typecalc-test-required]") {
		t.Fatalf("greenfield Go object with kind=compile must still trigger [typecalc-test-required]; got: %v", r.Issues)
	}
}

// TestGate_ReconstructionVerified_ZeroLockedDoesNotBypass: when
// Characterization is present but LockedCount=0 (equivalence oracle
// failed every battery item), the bypass must NOT apply — that lock
// locks nothing. The existing [method-use-rule] ZERO-behavior check
// catches this; the new bypass must defer to it.
func TestGate_ReconstructionVerified_ZeroLockedDoesNotBypass(t *testing.T) {
	r, err := reconScaffold(t, "C", "main.c", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Must FAIL — either [method-use-rule] ZERO-behavior or one of the
	// test-shape rules fires (we don't care which, as long as it isn't
	// silently passing).
	if r.Status == "PASS" {
		t.Fatalf("LockedCount=0 must NOT bypass gate (lock that locks nothing); got PASS with issues: %v", r.Issues)
	}
}
