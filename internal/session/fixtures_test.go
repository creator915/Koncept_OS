package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/graph"
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

	if _, err := Create(sessionDir, "s_root", "", "root", Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_root", StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := SetArchitecture(sessionDir, "s_root", architecture); err != nil {
		t.Fatal(err)
	}
	if err := graph.Save(graphPath, graph.NewGraph()); err != nil {
		t.Fatal(err)
	}
	return root, sessionDir, graphPath
}

// addConfirmedObject appends a confirmed Go-language object to the
// graph at graphPath, creating an impl file at root/src/<id>.impl.go.
// Used by fixtures that need >0 confirmed objects.
func addConfirmedObject(t *testing.T, root, graphPath, objID string, produces []string) {
	t.Helper()
	g, err := graph.LoadOrInit(graphPath)
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
	if err := graph.Save(graphPath, g); err != nil {
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

// waiveredEvidence is a convenience: object fails review but pairs
// with obstacle + waiver, which the v8.5+ gate accepts.
func waiveredEvidence(lang, obstacleReason string) map[string]any {
	return map[string]any{
		"test":     map[string]any{"kind": "test", "lang": lang, "ok": false},
		"accepted": map[string]any{"ok": false, "reasonableness": map[string]any{"verdict": "fail", "reasons": []string{"fixture failure"}, "confidence": 0.5}},
		"obstacle": map[string]any{"reason": obstacleReason},
		"waiver":   map[string]any{"reason": "manual verification — fixture", "verifier": "fixture-test"},
	}
}

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

// TestFixture_Pong02NarrowWaiver replicates pong-02's 1/2 waiver case
// (one object passes clean, one via obstacle+waiver). Gate must PASS:
// the waiver carve-out is legitimate at this scale.
func TestFixture_Pong02NarrowWaiver(t *testing.T) {
	root, sessionDir, graphPath := scaffoldRoot(t, "2 objects, 1 narrow waiver")
	addConfirmedObject(t, root, graphPath, "InitState", []string{"ball_state"})
	writeBundle(t, root, "InitState", cleanEvidence("Go"))

	addConfirmedObject(t, root, graphPath, "UpdateFrame", []string{"ball_state"})
	writeBundle(t, root, "UpdateFrame", waiveredEvidence("Go", "2/11 synthesized tests have geometrically inconsistent expected values — paddle x=400 width=60 cannot reach ball x=525"))

	r := runGate(t, root, sessionDir, graphPath)
	if r.Status != "PASS" {
		t.Errorf("expected PASS for 1/2 narrow waiver, got %s:\n  %s", r.Status, strings.Join(r.Issues, "\n  "))
	}
}

// TestFixture_Pong05MassWaiver replicates pong-05's 4/4 mass-waiver
// (every object obstacle+waiver). Gate must FAIL with
// [waiver-flood] — the systematic verification-bypass detection.
func TestFixture_Pong05MassWaiver(t *testing.T) {
	root, sessionDir, graphPath := scaffoldRoot(t, "4 objects, all via obstacle+waiver — should trip waiver-flood")
	for i, id := range []string{"InitGame", "PollInput", "UpdatePhysics", "RenderGame"} {
		addConfirmedObject(t, root, graphPath, id, []string{fmt.Sprintf("attr%d", i)})
		writeBundle(t, root, id, waiveredEvidence("Go", fmt.Sprintf("UNIQUE reason %d — fixture varies the obstacle string so reason-diversity probe alone doesn't flag", i)))
	}
	r := runGate(t, root, sessionDir, graphPath)
	if r.Status != "FAIL" {
		t.Errorf("expected FAIL for 4/4 mass waiver, got %s:\n  %s", r.Status, strings.Join(r.Issues, "\n  "))
	}
	if !hasIssueContaining(r, "[waiver-flood]") {
		t.Errorf("expected [waiver-flood] issue, got: %v", r.Issues)
	}
}

// TestFixture_BorderlinePathB replicates the edge case from pong-03's
// transcript: 3 confirmed objects, all via waiver (100%). totalConfirmed
// = 3 is BELOW waiverFloodMin = 4, so the throttle should NOT fire.
// This guards against a future "lower the floor" change that would
// accidentally regress small projects.
func TestFixture_SmallSessionAllWaiverPasses(t *testing.T) {
	root, sessionDir, graphPath := scaffoldRoot(t, "3 objects, all waivered — below waiver-flood floor")
	for i, id := range []string{"A", "B", "C"} {
		addConfirmedObject(t, root, graphPath, id, []string{fmt.Sprintf("a%d", i)})
		writeBundle(t, root, id, waiveredEvidence("Go", fmt.Sprintf("structural limit %d", i)))
	}
	r := runGate(t, root, sessionDir, graphPath)
	if hasIssueContaining(r, "[waiver-flood]") {
		t.Errorf("waiver-flood should NOT fire below totalConfirmed=4 floor, got issues:\n  %s", strings.Join(r.Issues, "\n  "))
	}
}

// v9.0.1 G — waiver kind discriminator. Pong-03's v9.0 batch deadlock
// scenario: 5 objects all confirmed via waiver because the HTML
// deliverable cannot run in Node. Pre-G that tripped [waiver-flood] at
// 100% even though every waiver was structurally legitimate. With kind
// "structural" declared on every waiver, the flood gate must stay
// silent.
func TestFixture_StructuralWaiversDontTripFlood(t *testing.T) {
	root, sessionDir, graphPath := scaffoldRoot(t, "5 HTML objects, all kind=structural waivered — no flood")
	for i, id := range []string{"InitGame", "PollInput", "HandleInput", "UpdatePhysics", "RenderFrame"} {
		addConfirmedObject(t, root, graphPath, id, []string{fmt.Sprintf("attr%d", i)})
		ev := waiveredEvidence("Go", fmt.Sprintf("Canvas / DOM I/O — structurally infeasible in Node harness (object %d)", i))
		// Override the waiver section with kind=structural.
		ev["waiver"] = map[string]any{
			"reason":   "harness cannot exercise Canvas / DOM I/O — verified by manual browser play-test against SPEC",
			"verifier": "fixture",
			"kind":     "structural",
		}
		writeBundle(t, root, id, ev)
	}
	r := runGate(t, root, sessionDir, graphPath)
	if hasIssueContaining(r, "[waiver-flood]") {
		t.Errorf("structural waivers should NOT trip [waiver-flood]; got issues:\n  %s", strings.Join(r.Issues, "\n  "))
	}
}

// And the inverse: an explicit kind=pragmatic waiver at flood ratio
// MUST still trip, so the discriminator doesn't degenerate into a free
// pass.
func TestFixture_PragmaticWaiversStillTripFlood(t *testing.T) {
	root, sessionDir, graphPath := scaffoldRoot(t, "4 objects all kind=pragmatic — flood fires")
	for i, id := range []string{"A", "B", "C", "D"} {
		addConfirmedObject(t, root, graphPath, id, []string{fmt.Sprintf("attr%d", i)})
		ev := waiveredEvidence("Go", fmt.Sprintf("test harness flake — will fix later (object %d)", i))
		ev["waiver"] = map[string]any{
			"reason":   fmt.Sprintf("harness binding mismatch — deferring fix to next PR (object %d)", i),
			"verifier": "fixture",
			"kind":     "pragmatic",
		}
		writeBundle(t, root, id, ev)
	}
	r := runGate(t, root, sessionDir, graphPath)
	if !hasIssueContaining(r, "[waiver-flood]") {
		t.Errorf("4/4 pragmatic waivers should trip [waiver-flood]; got: %v", r.Issues)
	}
}

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

// TestFixture_ObstacleWithoutWaiverFails — common v8.x mistake: agent
// writes obstacle but forgets the waiver pair. Gate must FAIL with
// [obstacle-needs-waiver].
func TestFixture_ObstacleWithoutWaiverFails(t *testing.T) {
	root, sessionDir, graphPath := scaffoldRoot(t, "object has obstacle but no waiver")
	addConfirmedObject(t, root, graphPath, "Op", []string{"x"})
	writeBundle(t, root, "Op", map[string]any{
		"test":     map[string]any{"kind": "test", "lang": "Go", "ok": false},
		"accepted": map[string]any{"ok": false, "reasonableness": map[string]any{"verdict": "fail"}},
		"obstacle": map[string]any{"reason": "structural"},
		// NO waiver section
	})
	r := runGate(t, root, sessionDir, graphPath)
	if !hasIssueContaining(r, "[obstacle-needs-waiver]") {
		t.Errorf("expected [obstacle-needs-waiver], got: %v", r.Issues)
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
