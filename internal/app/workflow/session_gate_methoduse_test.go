package workflow

import (
	"path/filepath"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// implContent is the on-disk impl body the [method-use-rule] hash
// comparison runs against. Its hash is the "current artifact" hash.
const muImplContent = "package main\n"

// gateScaffold lays down a session + a fully-confirmed graph object
// with passing test+accepted evidence, so CheckGate reaches the final
// [method-use-rule] block (every earlier rule already satisfied). The
// only knob the individual tests change is the bundle's
// `characterization` section.
func gateScaffold(t *testing.T, characterization map[string]any) (*GateReport, error) {
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
	if _, err := SetArchitecture(sessionDir, "s_root", "Sub-modules: Op."); err != nil {
		t.Fatal(err)
	}

	implPath := filepath.Join(root, "src", "loader.go")
	if err := mkdirAll(filepath.Dir(implPath)); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(implPath, muImplContent); err != nil {
		t.Fatal(err)
	}
	g := newConfirmedGraph(t, "src/loader.go")
	if err := saveRawGraph(graphPath, g); err != nil {
		t.Fatal(err)
	}
	evidenceDir := filepath.Join(root, ".kcpos", "typecalc")
	sections := map[string]any{
		"test":     map[string]any{"kind": "test", "lang": "Go", "ok": true},
		"accepted": map[string]any{"ok": true, "reasonableness": map[string]any{"verdict": "pass", "confidence": 0.95}},
	}
	if characterization != nil {
		sections["characterization"] = characterization
	}
	writeEvidenceBundle(t, evidenceDir, "Op", sections)

	cwdRestore := mustChdir(t, root)
	defer cwdRestore()
	return CheckGate(sessionDir, graphPath, "", "s_root")
}

// TestGate_MethodUseRule_FiresOnArtifactDrift: a characterized object
// whose locked hash no longer matches the current artifact bytes must
// fail the gate. This is the core Feathers Method Use Rule guarantee —
// before this test it was implemented but never demonstrated firing.
func TestGate_MethodUseRule_FiresOnArtifactDrift(t *testing.T) {
	r, err := gateScaffold(t, map[string]any{
		"suiteId":     "chsuite-Op-deadbeef",
		"codeHash":    "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		"lockedCount": 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "FAIL" {
		t.Fatalf("expected FAIL when the locked hash != current artifact hash, got %s (%v)", r.Status, r.Issues)
	}
	if !hasIssueWithPrefix(r.Issues, "[method-use-rule]") {
		t.Fatalf("expected [method-use-rule] issue on artifact drift, got: %v", r.Issues)
	}
	found := false
	for _, iss := range r.Issues {
		if substr(iss, "artifact changed since characterization") {
			found = true
		}
	}
	if !found {
		t.Fatalf("drift issue must explain the cause, got: %v", r.Issues)
	}
}

// TestGate_MethodUseRule_FiresWhenLockIsEmpty: a characterization that
// locked ZERO behavior is not a behavior-preservation net and must fail
// the gate (honest: a lock that locks nothing is theater).
func TestGate_MethodUseRule_FiresWhenLockIsEmpty(t *testing.T) {
	r, err := gateScaffold(t, map[string]any{
		"suiteId":     "chsuite-Op-" + core.HashSource(muImplContent)[:12],
		"codeHash":    core.HashSource(muImplContent), // hash MATCHES — only the empty lock should fire
		"lockedCount": 0,
		"unlockedCount": 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "FAIL" {
		t.Fatalf("expected FAIL when the lock characterizes zero behavior, got %s (%v)", r.Status, r.Issues)
	}
	found := false
	for _, iss := range r.Issues {
		if substr(iss, "[method-use-rule]") && substr(iss, "ZERO behavior") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected [method-use-rule] ZERO-behavior issue, got: %v", r.Issues)
	}
}

// TestGate_MethodUseRule_InertWhenLockMatches: when the lock still
// matches the current artifact and locks real behavior, the rule must
// NOT fire and the object must pass the gate exactly as a greenfield
// object would. Proves the rule is a guard, not a tax.
func TestGate_MethodUseRule_InertWhenLockMatches(t *testing.T) {
	r, err := gateScaffold(t, map[string]any{
		"suiteId":     "chsuite-Op-" + core.HashSource(muImplContent)[:12],
		"codeHash":    core.HashSource(muImplContent),
		"lockedCount": 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if hasIssueWithPrefix(r.Issues, "[method-use-rule]") {
		t.Fatalf("[method-use-rule] must be inert when the lock matches a non-empty characterization, got: %v", r.Issues)
	}
	if r.Status != "PASS" {
		t.Fatalf("a validly-characterized confirmed object must PASS the gate like greenfield, got %s (%v)", r.Status, r.Issues)
	}
}

// TestGate_MethodUseRule_GreenfieldUnaffected: an object with NO
// characterization section (every greenfield object) must never see
// the rule — the additive guarantee at the gate level.
func TestGate_MethodUseRule_GreenfieldUnaffected(t *testing.T) {
	r, err := gateScaffold(t, nil) // no characterization section at all
	if err != nil {
		t.Fatal(err)
	}
	if hasIssueWithPrefix(r.Issues, "[method-use-rule]") {
		t.Fatalf("greenfield object (no characterization) must never trigger [method-use-rule], got: %v", r.Issues)
	}
	if r.Status != "PASS" {
		t.Fatalf("greenfield confirmed object must PASS unchanged, got %s (%v)", r.Status, r.Issues)
	}
}
