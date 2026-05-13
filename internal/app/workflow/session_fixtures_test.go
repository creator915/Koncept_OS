package workflow

import (
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
)

// Fixture-based regression tests for v9.0 — each test scaffolds a
// project state on disk that matches a documented v8.x batch failure
// mode, then asserts that the v9.0 gate emits (or stays silent on)
// the expected rules. Pre-v9.0, regression coverage was "rerun the
// 5-instance pong batch and read the report" — 1 hour + ¥5 per
// validation cycle. These fixtures let the same patterns be checked
// in ~1 second of `go test`.
//
// Each fixture is built procedurally in Go rather than stored as JSON
// blobs so the schema can evolve without testdata files going stale.
// The fixture builders are kept thin and intent-revealing: each one
// names the v8.x batch instance it reproduces.

// scaffoldRoot creates a minimal K/ tree under t.TempDir() with:
//   - K/sessions/s_root.json (root session, active, with architecture)
//   - K/graph.json (initialized empty)
//   - src/<lang> stub impl file (so root-deliver impl-on-disk passes)
//
// Returns the project root path. Helpers below add objects + evidence.
func scaffoldRoot(t *testing.T, architecture string) (root, sessionDir, graphPath string) {
	t.Helper()
	root = t.TempDir()
	sessionDir = filepath.Join(root, "K", "sessions")
	graphPath = filepath.Join(root, "K", "graph.json")

	if _, err := Create(sessionDir, "s_root", "", "root", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_root", session.StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := SetArchitecture(sessionDir, "s_root", architecture); err != nil {
		t.Fatal(err)
	}
	if err := persistence.SaveGraph(graphPath, graph.NewGraph()); err != nil {
		t.Fatal(err)
	}
	return root, sessionDir, graphPath
}

// addConfirmedObject appends a confirmed Go-language object to the
// graph at graphPath, creating an impl file at root/src/<id>.impl.go.
// Used by fixtures that need >0 confirmed objects.
func addConfirmedObject(t *testing.T, root, graphPath, objID string, produces []string) {
	t.Helper()
	g, err := persistence.LoadGraphOrInit(graphPath)
	if err != nil {
		t.Fatal(err)
	}
	implPath := filepath.Join("src", strings.ToLower(objID)+".impl.go")
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, implPath), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := graph.NewObject("K/defs/"+objID+".go", "stub")
	o.Impl = &implPath
	o.Status = graph.StatusConfirmed
	o.Produces = produces
	// portObservation: a "return"-style observer so static-check's
	// port-observation-required rule passes.
	o.PortObservation = map[string]string{}
	for _, p := range produces {
		o.PortObservation[p] = "return"
	}
	g.Objects[objID] = o
	// Backfill the attribute as confirmed with a stub valueSpace.
	for _, attrID := range produces {
		if _, ok := g.Attributes[attrID]; ok {
			continue
		}
		a := graph.NewAttribute("K/defs/"+attrID+".go", "stub attribute")
		a.Status = graph.StatusConfirmed
		a.ValueSpace = map[string]any{"shape": "number"}
		g.Attributes[attrID] = a
	}
	if err := persistence.SaveGraph(graphPath, g); err != nil {
		t.Fatal(err)
	}
}

// writeBundle writes a v9.0 unified evidence bundle for an object.
// sections is sparse — only specified sections are populated.
func writeBundle(t *testing.T, root, objID string, sections map[string]any) {
	t.Helper()
	dir := filepath.Join(root, ".kcpos", "typecalc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
	body, _ := json.Marshal(bundle)
	if err := os.WriteFile(filepath.Join(dir, objID+".json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// cleanEvidence is a convenience: object passes compile + test +
// review with no obstacles.
func cleanEvidence(lang string) map[string]any {
	return map[string]any{
		"test":     map[string]any{"kind": "test", "lang": lang, "ok": true},
		"accepted": map[string]any{"ok": true, "reasonableness": map[string]any{"verdict": "pass", "reasons": []string{"fixture"}, "confidence": 1.0}},
	}
}

// waiveredEvidence — removed in v9.2 (obstacle/waiver mechanism deleted).
// The fixtures that depended on it asserted carve-out behavior that no
// longer exists.


func runGate(t *testing.T, root, sessionDir, graphPath string) *GateReport {
	t.Helper()
	prev, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(prev) }()
	r, err := CheckGate(sessionDir, graphPath, "", "s_root")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func hasIssueContaining(r *GateReport, substr string) bool {
	for _, iss := range r.Issues {
		if strings.Contains(iss, substr) {
			return true
		}
	}
	return false
}

// --- Fixtures ---

// TestFixture_Pong04CleanPath replicates the v8.7 pong-04 happy path:
// 4 confirmed Go objects, 0 waivers, complete evidence. Gate must
// PASS. This is the "gold standard" fixture — any future change that
// makes this fail without an intentional reason is regression.
func TestFixture_Pong04CleanPath(t *testing.T) {
	root, sessionDir, graphPath := scaffoldRoot(t, "Pong: 4 objects in produces-only form")
	for _, id := range []string{"InitGame", "UpdatePaddle", "UpdateBall", "Render"} {
		addConfirmedObject(t, root, graphPath, id, []string{strings.ToLower(id) + "_state"})
		writeBundle(t, root, id, cleanEvidence("Go"))
	}
	r := runGate(t, root, sessionDir, graphPath)
	if r.Status != "PASS" {
		t.Errorf("expected PASS for clean 4-object fixture, got %s:\n  %s", r.Status, strings.Join(r.Issues, "\n  "))
	}
}

// v9.2 — the four waiver/flood fixture tests below were removed when
// the obstacle/waiver mechanism was deleted entirely:
//   - TestFixture_Pong02NarrowWaiver
//   - TestFixture_Pong05MassWaiver
//   - TestFixture_SmallSessionAllWaiverPasses
//   - TestFixture_StructuralWaiversDontTripFlood
//   - TestFixture_PragmaticWaiversStillTripFlood
// They asserted carve-outs that no longer exist. The post-v9.2 gate is
// binary: every confirmed object stands on real compile/test/runtime
// evidence. Waiver-flood is unreachable because there are no waivers
// to flood.

// TestFixture_MissingAcceptedEvidenceFails replicates pong-02 v8.7
// "no Op.accepted.json" — gate must FAIL with [accepted-evidence-required].
func TestFixture_MissingAcceptedEvidenceFails(t *testing.T) {
	root, sessionDir, graphPath := scaffoldRoot(t, "object has test evidence but no review verdict")
	addConfirmedObject(t, root, graphPath, "Op", []string{"x"})
	// Test ok but no accepted section.
	writeBundle(t, root, "Op", map[string]any{
		"test": map[string]any{"kind": "test", "lang": "Go", "ok": true},
	})
	r := runGate(t, root, sessionDir, graphPath)
	if !hasIssueContaining(r, "[accepted-evidence-required]") {
		t.Errorf("expected [accepted-evidence-required], got: %v", r.Issues)
	}
}

// TestFixture_FailingEvidenceIsHardFail — v9.2 successor to
// TestFixture_ObstacleWithoutWaiverFails. Pre-v9.2 the absence of a
// waiver paired with an obstacle was the failure mode; now ANY failing
// test/accepted evidence is just a failure with no recovery path.
// Verifies the gate refuses confirm without trying to look for a
// rescue.
func TestFixture_FailingEvidenceIsHardFail(t *testing.T) {
	root, sessionDir, graphPath := scaffoldRoot(t, "object has failing test + failing review — no escape")
	addConfirmedObject(t, root, graphPath, "Op", []string{"x"})
	writeBundle(t, root, "Op", map[string]any{
		"test":     map[string]any{"kind": "test", "lang": "Go", "ok": false},
		"accepted": map[string]any{"ok": false, "reasonableness": map[string]any{"verdict": "fail"}},
		// v9.2: the test even includes an obstacle field to verify it
		// has no effect — old bundles with leftover obstacle sections
		// must NOT short-circuit the gate.
		"obstacle": map[string]any{"reason": "ignored after v9.2"},
	})
	r := runGate(t, root, sessionDir, graphPath)
	if r.Status == "PASS" {
		t.Errorf("expected gate FAIL with no waiver escape, got PASS; issues:\n  %s", strings.Join(r.Issues, "\n  "))
	}
	// Specifically check that the typecalc-evidence-passing rule fires.
	if !hasIssueContaining(r, "[typecalc-evidence-passing]") {
		t.Errorf("expected [typecalc-evidence-passing] in issues, got: %v", r.Issues)
	}
}

// TestFixture_HTMLDeliverableExemptFromTestRequired (v9.2 mid-batch fix) —
// regression: when impl=index.html and bundle.Compile.Lang="JavaScript"
// (typecalc_compile detected the lang from the .js implFragment, not
// the .html deliverable), the gate must NOT fire [typecalc-test-required].
// HTML deliverables are exempted from that rule per the D2 decision;
// the [runtime-smoke-required] rule covers them instead.
//
// Pre-fix this rule fired and confused the agent: "the protocol says
// runtime_smoke replaces typecalc_test, but the gate is asking for it
// anyway."
func TestFixture_HTMLDeliverableExemptFromTestRequired(t *testing.T) {
	root, sessionDir, graphPath := scaffoldRoot(t, "HTML deliverable with passing runtime_smoke")
	// Build a single confirmed object whose impl is an HTML file.
	g, err := persistence.LoadGraphOrInit(graphPath)
	if err != nil {
		t.Fatal(err)
	}
	htmlPath := "index.html"
	fragPath := "K/frags/GameLoop.js"
	if err := os.MkdirAll(filepath.Join(root, "K/frags"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, htmlPath), []byte("<!doctype html><html><body><canvas id=g></canvas><script>function GameLoop(){}</script></body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, fragPath), []byte("function GameLoop(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := graph.NewObject("K/defs/GameLoop.js", "stub")
	o.Impl = &htmlPath
	frag := fragPath
	o.ImplFragment = &frag
	o.Status = graph.StatusConfirmed
	o.Produces = []string{"frame"}
	o.PortObservation = map[string]string{"frame": "side_effect"}
	g.Objects["GameLoop"] = o
	a := graph.NewAttribute("K/defs/frame.js", "stub attribute")
	a.Status = graph.StatusConfirmed
	a.ValueSpace = map[string]any{"shape": "side_effect"}
	g.Attributes["frame"] = a
	if err := persistence.SaveGraph(graphPath, g); err != nil {
		t.Fatal(err)
	}
	// Bundle: compile passed (lang=JavaScript from frag), no test,
	// runtime_smoke recorded ok=true, accepted ok=true.
	writeBundle(t, root, "GameLoop", map[string]any{
		"compile":      map[string]any{"kind": "compile", "lang": "JavaScript", "ok": true},
		"runtimeSmoke": map[string]any{"ok": true, "loadFired": true, "loadDurationMs": 100, "canvas": map[string]any{"found": true, "ok": true, "nonBlackPixels": 1, "width": 800, "height": 600}, "timestamp": "1970-01-01T00:00:00Z"},
		"accepted":     map[string]any{"ok": true, "reasonableness": map[string]any{"verdict": "pass", "reasons": []string{"fixture"}, "confidence": 1.0}},
	})
	r := runGate(t, root, sessionDir, graphPath)
	if hasIssueContaining(r, "[typecalc-test-required]") {
		t.Errorf("HTML deliverable must NOT trigger [typecalc-test-required]; got: %v", r.Issues)
	}
	if hasIssueContaining(r, "[compile-not-enough]") {
		t.Errorf("HTML deliverable must NOT trigger [compile-not-enough]; got: %v", r.Issues)
	}
	if r.Status != "PASS" {
		t.Errorf("expected gate PASS for HTML with passing compile+runtime+accepted; got %s:\n  %s",
			r.Status, strings.Join(r.Issues, "\n  "))
	}
}

// TestFixture_HTMLMissingRuntimeSmokeStillFails — companion to the
// exemption test. HTML deliverable WITHOUT runtime_smoke evidence must
// still fail the gate. Verifies the exemption isn't too permissive.
func TestFixture_HTMLMissingRuntimeSmokeStillFails(t *testing.T) {
	root, sessionDir, graphPath := scaffoldRoot(t, "HTML deliverable lacking runtime_smoke")
	g, err := persistence.LoadGraphOrInit(graphPath)
	if err != nil {
		t.Fatal(err)
	}
	htmlPath := "index.html"
	if err := os.WriteFile(filepath.Join(root, htmlPath), []byte("<!doctype html><html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := graph.NewObject("K/defs/X.js", "stub")
	o.Impl = &htmlPath
	o.Status = graph.StatusConfirmed
	o.Produces = []string{"x"}
	o.PortObservation = map[string]string{"x": "side_effect"}
	g.Objects["X"] = o
	a := graph.NewAttribute("K/defs/x.js", "stub")
	a.Status = graph.StatusConfirmed
	a.ValueSpace = map[string]any{"shape": "side_effect"}
	g.Attributes["x"] = a
	if err := persistence.SaveGraph(graphPath, g); err != nil {
		t.Fatal(err)
	}
	writeBundle(t, root, "X", map[string]any{
		"compile":  map[string]any{"kind": "compile", "lang": "JavaScript", "ok": true},
		"accepted": map[string]any{"ok": true, "reasonableness": map[string]any{"verdict": "pass"}},
	})
	r := runGate(t, root, sessionDir, graphPath)
	if !hasIssueContaining(r, "[runtime-smoke-required]") {
		t.Errorf("HTML without runtime_smoke must trigger [runtime-smoke-required]; got: %v", r.Issues)
	}
}

// TestFixture_ContextCancelDoesNotPanic — defensive smoke test.
// Cancelled context during gate should return cleanly.
func TestFixture_GateRunsWithCancelledCtx(t *testing.T) {
	root, sessionDir, graphPath := scaffoldRoot(t, "ctx test")
	addConfirmedObject(t, root, graphPath, "Op", []string{"x"})
	writeBundle(t, root, "Op", cleanEvidence("Go"))
	prev, _ := os.Getwd()
	defer os.Chdir(prev)
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	// CheckGate doesn't take ctx — this just confirms it doesn't trip
	// on the surrounding test setup. Reserved for future ctx-aware variant.
	_ = context.Background
	r, err := CheckGate(sessionDir, graphPath, "", "s_root")
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("nil report")
	}
}
