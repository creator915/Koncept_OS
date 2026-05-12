package typecalc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/graph"
)

// useTempEvidenceDir reroutes EvidenceDir into a temp directory for the
// duration of one test. Returns a cleanup that restores the original
// path. Tests that touch evidence files MUST call this to avoid
// stomping on a real .kcpos directory.
func useTempEvidenceDir(t *testing.T) func() {
	t.Helper()
	prev := EvidenceDir
	dir, err := os.MkdirTemp("", "kcpos-typecalc-")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	// EvidenceDir is a const — we can't reassign it. Instead, chdir into
	// the temp dir so all relative paths resolve under it.
	prevWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if err := os.MkdirAll(EvidenceDir, 0o755); err != nil {
		t.Fatalf("mkdir evidence: %v", err)
	}
	return func() {
		_ = os.Chdir(prevWd)
		_ = os.RemoveAll(dir)
		_ = prev // silence unused warning; const isn't actually swapped
	}
}

func newTestGraph() *graph.Graph {
	g := graph.NewGraph()
	g.Attributes["data_in"] = graph.NewAttribute("defs/data_in.go", "input data")
	g.Attributes["data_out"] = graph.NewAttribute("defs/data_out.go", "output data")
	implPath := "src/Process.impl.go"
	obj := graph.NewObject("defs/Process.go", "transform input to output")
	obj.Impl = &implPath
	obj.Consumes = []string{"data_in"}
	obj.Produces = []string{"data_out"}
	// D2: every produced port needs a portObservation extractor.
	obj.PortObservation = map[string]string{"data_out": "return"}
	g.Objects["Process"] = obj
	return g
}

func TestStaticCheck_FlagsEmptyEffects(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	// Strip every effect — should fire effects-empty.
	g.Objects["Process"].Produces = nil
	g.Objects["Process"].Mutates = nil

	issues := StaticCheck(".", g, "Process")
	if !hasIssue(issues, "effects-empty") {
		t.Fatalf("expected effects-empty, got %v", issues)
	}
}

func TestStaticCheck_FlagsMissingImpl(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	g.Objects["Process"].Impl = nil

	issues := StaticCheck(".", g, "Process")
	if !hasIssue(issues, "impl-missing") {
		t.Fatalf("expected impl-missing, got %v", issues)
	}
}

func TestStaticCheck_FlagsImplOnDisk(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	// Impl path set but file does not exist.
	issues := StaticCheck(".", g, "Process")
	if !hasIssue(issues, "impl-on-disk") {
		t.Fatalf("expected impl-on-disk, got %v", issues)
	}
}

func TestStaticCheck_FlagsValueSpaceEmpty(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	// Lay impl down on disk so impl-on-disk does not fire.
	if err := os.MkdirAll("src", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("src/Process.impl.go", []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	issues := StaticCheck(".", g, "Process")
	if !hasIssue(issues, "value-space-empty") {
		t.Fatalf("expected value-space-empty, got %v", issues)
	}
}

func TestStaticCheck_FlagsSpecMissing(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	if err := os.MkdirAll("src", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("src/Process.impl.go", []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	issues := StaticCheck(".", g, "Process")
	if !hasIssue(issues, "spec-missing") {
		t.Fatalf("expected spec-missing, got %v", issues)
	}
}

func TestStaticCheck_DetectsStaleSpec(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	if err := os.MkdirAll("src", 0o755); err != nil {
		t.Fatal(err)
	}
	implContent := []byte("package main\n// version 2")
	if err := os.WriteFile("src/Process.impl.go", implContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// Write a spec whose SourceHash is for a *different* impl content.
	if err := WriteSpec(&SpecEvidence{
		ObjectID:    "Process",
		Description: "old description",
		SourceHash:  HashSource("package main\n// version 1"),
	}); err != nil {
		t.Fatal(err)
	}

	issues := StaticCheck(".", g, "Process")
	if !hasIssue(issues, "spec-stale") {
		t.Fatalf("expected spec-stale, got %v", issues)
	}
}

func TestStaticCheck_PassesWhenAllOK(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	// Backfill valueSpace on produced attribute.
	g.Attributes["data_out"].ValueSpace = map[string]any{"shape": "string"}
	if err := os.MkdirAll("src", 0o755); err != nil {
		t.Fatal(err)
	}
	implContent := []byte("package main\n")
	if err := os.WriteFile("src/Process.impl.go", implContent, 0o644); err != nil {
		t.Fatal(err)
	}
	// Lay down compile evidence so base-evidence-missing does not fire.
	if err := RecordEvidence("Process", "compile", "Go", true); err != nil {
		t.Fatal(err)
	}
	// Lay down a fresh spec.
	if err := WriteSpec(&SpecEvidence{
		ObjectID:    "Process",
		Description: "transforms input data into output data",
		SourceHash:  HashSource(string(implContent)),
	}); err != nil {
		t.Fatal(err)
	}

	issues := StaticCheck(".", g, "Process")
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues, got %d: %v", len(issues), issues)
	}
}

func hasIssue(issues []StaticIssue, code string) bool {
	for _, i := range issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

// Belt-and-suspenders: every issue should serialize cleanly so the
// agent's tool result remains parseable.
func TestStaticIssue_StringNonEmpty(t *testing.T) {
	is := StaticIssue{Code: "x", Where: "y", Message: "z"}
	if is.Code == "" || is.Where == "" || is.Message == "" {
		t.Fatalf("missing fields: %+v", is)
	}
	// formatting check — not a stable contract, just that it's non-empty
	formatted := is.Code + ":" + is.Where + ":" + is.Message
	if !strings.Contains(formatted, ":") {
		t.Fatal("format unexpected")
	}
}

// v9.0.1 F regression: pong-01 batch hit a portObservation key that
// didn't match any attribute id (camelCase JS key vs snake_case graph
// id). The new port-observation-orphan-key rule must catch this AND
// suggest the closest matching output.
func TestStaticCheck_FlagsOrphanPortObservationKey(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	g.Attributes["game_status"] = graph.NewAttribute("defs/game_status.go", "snake_case attribute id")
	g.Attributes["game_status"].ValueSpace = map[string]any{"shape": "string"}
	g.Objects["Process"].Produces = []string{"game_status"}
	// Wrong: camelCase key instead of attribute id.
	g.Objects["Process"].PortObservation = map[string]string{"gameStatus": "return.gameStatus"}

	issues := StaticCheck(".", g, "Process")
	if !hasIssue(issues, "port-observation-orphan-key") {
		t.Fatalf("expected port-observation-orphan-key, got %v", issues)
	}
	// The suggestion must include the snake_case attribute id so the
	// agent gets a one-step remediation, not a vague "key mismatch".
	var orphanMsg string
	for _, i := range issues {
		if i.Code == "port-observation-orphan-key" {
			orphanMsg = i.Message
			break
		}
	}
	if !strings.Contains(orphanMsg, "\"game_status\"") {
		t.Fatalf("expected suggestion to include 'game_status', got: %s", orphanMsg)
	}
	if !strings.Contains(orphanMsg, "did you mean") {
		t.Fatalf("expected suggestion phrasing 'did you mean', got: %s", orphanMsg)
	}
}

// Keys that match an output via case/underscore normalization (e.g.
// "GameStatus" → "game_status") should be flagged with a suggestion.
// Keys that don't match anything close get a flag without suggestion.
func TestStaticCheck_OrphanKey_NoFalseNegative(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	// data_out is the only legit output. Add a totally unrelated key.
	g.Objects["Process"].PortObservation = map[string]string{
		"data_out":      "return",   // good
		"completelyOff": "return.x", // orphan, no close match
	}

	issues := StaticCheck(".", g, "Process")
	count := 0
	for _, i := range issues {
		if i.Code == "port-observation-orphan-key" {
			count++
			if strings.Contains(i.Message, "did you mean") {
				t.Fatalf("expected no suggestion for unrelated key 'completelyOff', got: %s", i.Message)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 orphan-key issue (for completelyOff), got %d: %v", count, issues)
	}
}

// v9.0.2 bug-D end-to-end fixture: exercises the EXACT pong-01 v9.0
// failure shape (camelCase JS key vs snake_case attribute id) through
// a typecalc_review-shaped issue collection so we know the error
// message will surface to the chain / agent with the actionable
// snake_case suggestion. This is the regression test that proves
// v9.0.2 catches what v9.0 batch burned 70 min on.
func TestBugDFixture_OrphanKeyMessageReachesAgent(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	// Replicate pong-01 v9.0 InitGame configuration:
	//   - attribute id: "game_status" (snake_case, per graph convention)
	//   - portObservation key: "gameStatus" (camelCase, JS habit)
	//   - extractor pointing at the JS-side name: "return.gameStatus"
	g := graph.NewGraph()
	g.Attributes["ball"] = graph.NewAttribute("defs/ball.go", "ball state")
	g.Attributes["paddle"] = graph.NewAttribute("defs/paddle.go", "paddle state")
	g.Attributes["game_status"] = graph.NewAttribute("defs/game_status.go", "game status string")
	for _, a := range []string{"ball", "paddle", "game_status"} {
		g.Attributes[a].ValueSpace = map[string]any{"shape": "object"}
	}
	implPath := "src/InitGame.impl.html"
	obj := graph.NewObject("defs/InitGame.go", "initialise the game state")
	obj.Impl = &implPath
	obj.Produces = []string{"ball", "paddle", "game_status"}
	obj.PortObservation = map[string]string{
		"ball":        "return.ball",
		"paddle":      "return.paddle",
		"gameStatus":  "return.gameStatus", // <-- THE BUG: key is gameStatus, should be game_status
	}
	g.Objects["InitGame"] = obj

	if err := os.MkdirAll("src", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("src/InitGame.impl.html", []byte("<script>function InitGame(){return{ball:{},paddle:{},gameStatus:'playing'}}</script>"), 0o644); err != nil {
		t.Fatal(err)
	}

	issues := StaticCheck(".", g, "InitGame")

	// Must fire orphan-key for the camelCase key.
	var orphan *StaticIssue
	var missing []string
	for i := range issues {
		switch issues[i].Code {
		case "port-observation-orphan-key":
			orphan = &issues[i]
		case "port-observation-required":
			missing = append(missing, issues[i].Message)
		}
	}
	if orphan == nil {
		t.Fatalf("v9.0.2 must fire port-observation-orphan-key for camelCase key 'gameStatus'; got issues: %v", issues)
	}

	// And the message must contain the actionable snake_case suggestion
	// — this is the bit that lets agents self-recover in seconds
	// instead of 70 minutes.
	msg := orphan.Message
	wantPhrases := []string{
		`"gameStatus"`,         // identifies the orphan key
		`"game_status"`,        // suggests the correct attribute id
		"did you mean",         // human-readable phrasing
		"snake_case",           // hints at the naming convention
	}
	for _, p := range wantPhrases {
		if !strings.Contains(msg, p) {
			t.Errorf("bug-D message missing %q. Full message:\n%s", p, msg)
		}
	}

	// Side check: port-observation-required also fires for the missing
	// snake_case extractor on game_status. The combination of both
	// signals tells the agent EXACTLY what went wrong: "you wrote the
	// wrong key" AND "the right key has no extractor". Either alone
	// would be ambiguous.
	if len(missing) == 0 {
		t.Error("expected port-observation-required to also fire for the missing 'game_status' extractor — combined signal is what makes bug-D unambiguous")
	}
}

// v9.0.2 §4.3 fix: spec-stale staleness storm. Single-file HTML
// projects share one impl file across N graph objects. Pre-v9.0.2,
// editing one function's body bumped sha256(file) → every object's
// spec.SourceHash was now stale → spec-stale fired for ALL objects →
// describe + synth + test cascade on every object. v9.0.2 introduces
// per-object SymbolHash (sha256 of the relevant function body
// fragment). Stale check prefers SymbolHash, so editing function A
// invalidates A's spec but leaves B / C / D's specs intact.
func TestStaticCheck_SymbolHashIsolatesSpecStale(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	// One HTML file, two graph objects sharing it.
	html := `<script>
function Foo() { return 1; }
function Bar() { return 2; }
</script>`
	if err := os.MkdirAll("src", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("src/index.html", []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
	implPath := "src/index.html"

	g := graph.NewGraph()
	g.Attributes["foo_out"] = graph.NewAttribute("defs/foo_out.go", "")
	g.Attributes["foo_out"].ValueSpace = map[string]any{"shape": "number"}
	g.Attributes["bar_out"] = graph.NewAttribute("defs/bar_out.go", "")
	g.Attributes["bar_out"].ValueSpace = map[string]any{"shape": "number"}

	fooObj := graph.NewObject("defs/Foo.go", "compute foo")
	fooObj.Impl = &implPath
	fooObj.Produces = []string{"foo_out"}
	fooObj.PortObservation = map[string]string{"foo_out": "return"}
	g.Objects["Foo"] = fooObj

	barObj := graph.NewObject("defs/Bar.go", "compute bar")
	barObj.Impl = &implPath
	barObj.Produces = []string{"bar_out"}
	barObj.PortObservation = map[string]string{"bar_out": "return"}
	g.Objects["Bar"] = barObj

	// Snapshot fragment hashes at "spec written" time.
	fooFragV1, _ := SymbolFragmentHash(html, implPath, "Foo")
	barFragV1, _ := SymbolFragmentHash(html, implPath, "Bar")
	if fooFragV1 == "" || barFragV1 == "" {
		t.Fatalf("fragment extraction failed (foo=%q bar=%q)", fooFragV1, barFragV1)
	}
	if fooFragV1 == barFragV1 {
		t.Fatalf("Foo and Bar should hash to DIFFERENT fragments; both = %q", fooFragV1)
	}
	fileHashV1 := HashSource(html)

	// Persist spec for both objects against v1 content (each gets
	// their own fragment hash, but they share the file hash).
	for _, o := range []struct{ id, frag string }{{"Foo", fooFragV1}, {"Bar", barFragV1}} {
		if err := WriteSpec(&SpecEvidence{
			ObjectID:    o.id,
			Description: o.id + " description",
			SourceHash:  fileHashV1,
			SymbolHash:  o.frag,
		}); err != nil {
			t.Fatalf("write spec %s: %v", o.id, err)
		}
		if err := RecordEvidenceWithSymbol(o.id, "compile", "JavaScript", true, "", fileHashV1, o.frag); err != nil {
			t.Fatalf("record evidence %s: %v", o.id, err)
		}
	}

	// Sanity: neither is stale against v1.
	for _, id := range []string{"Foo", "Bar"} {
		issues := StaticCheck(".", g, id)
		if hasIssue(issues, "spec-stale") {
			t.Errorf("%s should not be spec-stale against unchanged impl; got: %v", id, issues)
		}
		if hasIssue(issues, "evidence-stale") {
			t.Errorf("%s should not be evidence-stale against unchanged impl; got: %v", id, issues)
		}
	}

	// Edit ONLY Foo's body. File hash changes for both objects, but
	// only Foo's fragment hash should differ.
	htmlV2 := `<script>
function Foo() { return 999; }
function Bar() { return 2; }
</script>`
	if err := os.WriteFile(implPath, []byte(htmlV2), 0o644); err != nil {
		t.Fatal(err)
	}

	fooFragV2, _ := SymbolFragmentHash(htmlV2, implPath, "Foo")
	barFragV2, _ := SymbolFragmentHash(htmlV2, implPath, "Bar")
	if fooFragV2 == fooFragV1 {
		t.Fatalf("Foo's fragment hash should have changed after edit")
	}
	if barFragV2 != barFragV1 {
		t.Fatalf("Bar's fragment hash should NOT change when only Foo's body was edited (got %s, want %s)", barFragV2, barFragV1)
	}
	// And the file hash DID change — which would have tripped pre-v9.0.2.
	if HashSource(htmlV2) == fileHashV1 {
		t.Fatal("file hash didn't change — test setup broken")
	}

	// After edit: Foo is stale, Bar is NOT.
	fooIssues := StaticCheck(".", g, "Foo")
	if !hasIssue(fooIssues, "spec-stale") {
		t.Errorf("Foo should be spec-stale after its body changed; got: %v", fooIssues)
	}
	barIssues := StaticCheck(".", g, "Bar")
	if hasIssue(barIssues, "spec-stale") {
		t.Errorf("Bar should NOT be spec-stale just because Foo's body changed; got: %v", barIssues)
	}
	if hasIssue(barIssues, "evidence-stale") {
		t.Errorf("Bar should NOT be evidence-stale either; got: %v", barIssues)
	}
}

// v9.0.5.2 defs-must-throw: a JS def file with a non-throw body must
// fire the rule. This is what stops agents from writing
// `function Foo(){return 0;}` in defs/ and using it as fake impl.
func TestDefsMustThrow_FlagsNonThrowBody(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	if err := os.MkdirAll("defs", 0o755); err != nil {
		t.Fatal(err)
	}
	// Bad def: returns 0 instead of throwing.
	g.Objects["Process"].Def = "defs/Process.js"
	if err := os.WriteFile("defs/Process.js", []byte("function Process(x) { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := StaticCheck(".", g, "Process")
	if !hasIssue(issues, "defs-must-throw") {
		t.Fatalf("expected defs-must-throw, got: %v", issues)
	}
}

func TestDefsMustThrow_AcceptsThrowStub(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	if err := os.MkdirAll("defs", 0o755); err != nil {
		t.Fatal(err)
	}
	g.Objects["Process"].Def = "defs/Process.js"
	stub := `/**
 * @param {*} x
 * @returns {*}
 */
function Process(x) { throw new Error("Process: contract-only; implement in K/frags/Process.js"); }
`
	if err := os.WriteFile("defs/Process.js", []byte(stub), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := StaticCheck(".", g, "Process")
	if hasIssue(issues, "defs-must-throw") {
		t.Fatalf("throw-stub def should NOT trip defs-must-throw; got: %v", issues)
	}
}

func TestDefsMustThrow_TsDefNotChecked(t *testing.T) {
	// TS defs use declaration syntax; this rule only applies to JS.
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	g := newTestGraph()
	if err := os.MkdirAll("defs", 0o755); err != nil {
		t.Fatal(err)
	}
	g.Objects["Process"].Def = "defs/Process.ts"
	if err := os.WriteFile("defs/Process.ts", []byte("export function Process(x: any): any;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := StaticCheck(".", g, "Process")
	if hasIssue(issues, "defs-must-throw") {
		t.Errorf("defs-must-throw should not apply to TypeScript defs; got: %v", issues)
	}
}

// v9.0.5.3 frags-non-trivial: a fragment with trivial body (return
// literal / empty / pass-through) must fire the rule.
func TestFragsNonTrivial_FlagsTrivialStub(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	fragPath := "K/frags/Process.js"
	g.Objects["Process"].ImplFragment = &fragPath
	if err := os.MkdirAll("K/frags", 0o755); err != nil {
		t.Fatal(err)
	}
	// Trivial body.
	if err := os.WriteFile(fragPath, []byte("function Process(x) { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := StaticCheck(".", g, "Process")
	if !hasIssue(issues, "frags-non-trivial") {
		t.Fatalf("expected frags-non-trivial, got: %v", issues)
	}
}

func TestFragsNonTrivial_AcceptsRealImpl(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	fragPath := "K/frags/Process.js"
	g.Objects["Process"].ImplFragment = &fragPath
	if err := os.MkdirAll("K/frags", 0o755); err != nil {
		t.Fatal(err)
	}
	real := `function Process(x) {
  if (x.value < 0) return { result: 0 };
  const out = { result: x.value * 2 };
  return out;
}
`
	if err := os.WriteFile(fragPath, []byte(real), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := StaticCheck(".", g, "Process")
	if hasIssue(issues, "frags-non-trivial") {
		t.Fatalf("real impl should NOT trip frags-non-trivial; got: %v", issues)
	}
}

func TestFragsNonTrivial_FlagsEmptyBody(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	g := newTestGraph()
	fragPath := "K/frags/Process.js"
	g.Objects["Process"].ImplFragment = &fragPath
	if err := os.MkdirAll("K/frags", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fragPath, []byte("function Process(x) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := StaticCheck(".", g, "Process")
	if !hasIssue(issues, "frags-non-trivial") {
		t.Fatalf("empty-body fragment should trip frags-non-trivial; got: %v", issues)
	}
}

// v9.0.6.1 defs-entity-1to1: function in def whose name ≠ id /
// ImplSymbol must be flagged.
func TestDefsEntity1to1_FlagsExtraFunction(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	g := newTestGraph()
	if err := os.MkdirAll("defs", 0o755); err != nil {
		t.Fatal(err)
	}
	g.Objects["Process"].Def = "defs/Process.js"
	body := `function Process(x) { throw new Error("c"); }
function helperFn(x) { throw new Error("c"); }
`
	if err := os.WriteFile("defs/Process.js", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := StaticCheck(".", g, "Process")
	if !hasIssue(issues, "defs-entity-1to1") {
		t.Fatalf("expected defs-entity-1to1 for helperFn; got %v", issues)
	}
}

func TestDefsEntity1to1_AcceptsImplSymbol(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	g := newTestGraph()
	if err := os.MkdirAll("defs", 0o755); err != nil {
		t.Fatal(err)
	}
	g.Objects["Process"].Def = "defs/Process.js"
	g.Objects["Process"].ImplSymbol = "processData"
	body := `function processData(x) { throw new Error("c"); }
`
	if err := os.WriteFile("defs/Process.js", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := StaticCheck(".", g, "Process")
	if hasIssue(issues, "defs-entity-1to1") {
		t.Fatalf("ImplSymbol-named function should be allowed; got %v", issues)
	}
}

// v9.0.6.3 frags-content-matches-def: extra function in fragment must
// fire the rule.
func TestFragsContentMatchesDef_FlagsExtraInFrag(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	g := newTestGraph()
	if err := os.MkdirAll("defs", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("K/frags", 0o755); err != nil {
		t.Fatal(err)
	}
	g.Objects["Process"].Def = "defs/Process.js"
	fragPath := "K/frags/Process.js"
	g.Objects["Process"].ImplFragment = &fragPath
	def := `function Process(x) { throw new Error("c"); }
`
	if err := os.WriteFile("defs/Process.js", []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}
	frag := `function Process(x) {
  for (let i = 0; i < x; i++) { /* work */ }
  return x;
}
function ghost(x) {
  return x + 1;
}
`
	if err := os.WriteFile(fragPath, []byte(frag), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := StaticCheck(".", g, "Process")
	if !hasIssue(issues, "frags-content-matches-def") {
		t.Fatalf("expected frags-content-matches-def for ghost; got %v", issues)
	}
}

func TestFragsContentMatchesDef_FlagsMissingInFrag(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	g := newTestGraph()
	if err := os.MkdirAll("defs", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("K/frags", 0o755); err != nil {
		t.Fatal(err)
	}
	g.Objects["Process"].Def = "defs/Process.js"
	fragPath := "K/frags/Process.js"
	g.Objects["Process"].ImplFragment = &fragPath
	// Def declares both Process and Process_helper (would also fail
	// defs-entity-1to1 in production; but we're isolating
	// frags-content-matches-def here so we'll use ImplSymbol to allow
	// the helper).
	g.Objects["Process"].ImplSymbol = "Process_helper"
	def := `function Process(x) { throw new Error("c"); }
function Process_helper(y) { throw new Error("c"); }
`
	if err := os.WriteFile("defs/Process.js", []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}
	// Frag forgets Process_helper.
	frag := `function Process(x) {
  if (x > 0) return x;
  return 0;
}
`
	if err := os.WriteFile(fragPath, []byte(frag), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := StaticCheck(".", g, "Process")
	found := false
	for _, iss := range issues {
		if iss.Code == "frags-content-matches-def" && strings.Contains(iss.Message, "missing") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected frags-content-matches-def missing-fn; got %v", issues)
	}
}

// Sanity check: a well-formed portObservation (every key is an output)
// does NOT trip the orphan-key rule.
func TestStaticCheck_OrphanKey_CleanPasses(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	// keys match produces ∪ mutates
	g.Objects["Process"].PortObservation = map[string]string{"data_out": "return"}

	issues := StaticCheck(".", g, "Process")
	if hasIssue(issues, "port-observation-orphan-key") {
		t.Fatalf("clean portObservation should not trip orphan-key; got: %v", issues)
	}
}

// regression: relative impl paths should resolve against cwd.
func TestStaticCheck_RelativeImplResolvesAgainstCwd(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	g.Attributes["data_out"].ValueSpace = map[string]any{"shape": "string"}
	if err := os.MkdirAll("src", 0o755); err != nil {
		t.Fatal(err)
	}
	implContent := []byte("package main\n")
	if err := os.WriteFile("src/Process.impl.go", implContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RecordEvidence("Process", "compile", "Go", true); err != nil {
		t.Fatal(err)
	}
	if err := WriteSpec(&SpecEvidence{
		ObjectID:    "Process",
		Description: "ok",
		SourceHash:  HashSource(string(implContent)),
	}); err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	issues := StaticCheck(wd, g, "Process")
	for _, is := range issues {
		if is.Code == "impl-on-disk" {
			t.Fatalf("expected impl to be found (cwd-relative), got: %v", is)
		}
	}
	// also not stale
	if hasIssue(issues, "spec-stale") {
		t.Fatal("spec should not be stale")
	}
	// ensure absolute path used inside resolver agrees with the temp cwd.
	abs := filepath.Join(wd, "src/Process.impl.go")
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("setup wrong: %v", err)
	}
}
