package sessiontools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/graph"
)

// runInTempDir creates a tempdir, chdirs into it, returns a cleanup
// that chdir's back. Used so each test gets an isolated K/ tree.
func runInTempDir(t *testing.T) func() {
	t.Helper()
	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("K", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("K/frags", 0o755); err != nil {
		t.Fatal(err)
	}
	return func() { _ = os.Chdir(prev) }
}

// writeGraph helper persists g to K/graph.json so session_build can
// LoadOrInit it.
func writeGraph(t *testing.T, g *graph.Graph) {
	t.Helper()
	if err := graph.Save(graph.DefaultPath, g); err != nil {
		t.Fatal(err)
	}
}

// TestSessionBuild_HappyPath: 3 objects with implFragment, shared
// impl=index.html. session_build must produce a deliverable with all
// 3 fragments in topo order.
func TestSessionBuild_HappyPath(t *testing.T) {
	defer runInTempDir(t)()

	g := graph.NewGraph()
	g.Attributes["state"] = graph.NewAttribute("defs/state.ts", "state")
	g.Attributes["frame"] = graph.NewAttribute("defs/frame.ts", "frame")

	implPath := "index.html"
	for _, id := range []string{"InitGame", "UpdateFrame", "Render"} {
		fragPath := "K/frags/" + id + ".js"
		obj := graph.NewObject("defs/"+id+".ts", id+" intent")
		obj.Impl = &implPath
		obj.ImplFragment = &fragPath
		g.Objects[id] = obj
	}
	// InitGame produces state, UpdateFrame consumes state + produces frame, Render consumes frame.
	// Required topo order: InitGame → UpdateFrame → Render.
	g.Objects["InitGame"].Produces = []string{"state"}
	g.Objects["UpdateFrame"].Consumes = []string{"state"}
	g.Objects["UpdateFrame"].Produces = []string{"frame"}
	g.Objects["Render"].Consumes = []string{"frame"}
	writeGraph(t, g)

	mustWrite(t, "K/frags/InitGame.js", "function InitGame() { return 'A'; }")
	mustWrite(t, "K/frags/UpdateFrame.js", "function UpdateFrame() { return 'B'; }")
	mustWrite(t, "K/frags/Render.js", "function Render() { return 'C'; }")

	out, err := runSessionBuild()
	if err != nil {
		t.Fatalf("runSessionBuild: %v", err)
	}
	body := mustRead(t, "index.html")

	// All three fragments present.
	for _, want := range []string{"function InitGame", "function UpdateFrame", "function Render"} {
		if !strings.Contains(body, want) {
			t.Errorf("deliverable missing %q. Body:\n%s", want, body)
		}
	}
	// Topo order: InitGame appears before UpdateFrame appears before Render.
	idxInit := strings.Index(body, "function InitGame")
	idxUpdate := strings.Index(body, "function UpdateFrame")
	idxRender := strings.Index(body, "function Render")
	if !(idxInit < idxUpdate && idxUpdate < idxRender) {
		t.Errorf("topo order wrong: InitGame=%d UpdateFrame=%d Render=%d", idxInit, idxUpdate, idxRender)
	}
	// Output report mentions topo order.
	if !strings.Contains(out, "InitGame → UpdateFrame → Render") {
		t.Errorf("report missing topo string. Got: %s", out)
	}
	// Block markers present so re-runs can replace cleanly.
	if !strings.Contains(body, kcposBlockOpen) || !strings.Contains(body, kcposBlockClose) {
		t.Error("block markers missing from assembled deliverable")
	}
}

// TestSessionBuild_Idempotent: running twice with no fragment changes
// produces byte-identical output.
func TestSessionBuild_Idempotent(t *testing.T) {
	defer runInTempDir(t)()

	g := graph.NewGraph()
	implPath := "index.html"
	fragPath := "K/frags/Foo.js"
	obj := graph.NewObject("defs/Foo.ts", "")
	obj.Impl = &implPath
	obj.ImplFragment = &fragPath
	obj.Produces = []string{"x"}
	g.Attributes["x"] = graph.NewAttribute("defs/x.ts", "")
	g.Objects["Foo"] = obj
	writeGraph(t, g)
	mustWrite(t, "K/frags/Foo.js", "function Foo() {}")

	if _, err := runSessionBuild(); err != nil {
		t.Fatalf("first build: %v", err)
	}
	first := mustRead(t, "index.html")
	if _, err := runSessionBuild(); err != nil {
		t.Fatalf("second build: %v", err)
	}
	second := mustRead(t, "index.html")
	if first != second {
		t.Errorf("session_build not idempotent. first=%d bytes, second=%d bytes", len(first), len(second))
	}
}

// TestSessionBuild_ReplacePriorBlock: when a kcpos block already
// exists in the deliverable, the new run replaces ONLY that block
// (preserving surrounding user-written HTML).
func TestSessionBuild_ReplacePriorBlock(t *testing.T) {
	defer runInTempDir(t)()

	g := graph.NewGraph()
	implPath := "index.html"
	fragPath := "K/frags/Foo.js"
	obj := graph.NewObject("defs/Foo.ts", "")
	obj.Impl = &implPath
	obj.ImplFragment = &fragPath
	g.Objects["Foo"] = obj
	writeGraph(t, g)
	// v9.0.6 — top-level function names must be modeled. Use `Foo` to
	// match the graph object id. The V1/V2 difference is in the body.
	mustWrite(t, "K/frags/Foo.js", "function Foo() { return 1; /* V1 */ }")

	if _, err := runSessionBuild(); err != nil {
		t.Fatal(err)
	}
	// Add agent-authored HTML around the kcpos block.
	current := mustRead(t, "index.html")
	withSurround := strings.Replace(current,
		kcposBlockOpen,
		"<header>kcpos demo</header>\n  "+kcposBlockOpen,
		1)
	mustWrite(t, "index.html", withSurround)

	// Now bump the fragment body and rebuild.
	mustWrite(t, "K/frags/Foo.js", "function Foo() { return 2; /* V2 */ }")
	if _, err := runSessionBuild(); err != nil {
		t.Fatal(err)
	}
	body := mustRead(t, "index.html")
	if !strings.Contains(body, "<header>kcpos demo</header>") {
		t.Error("surrounding user HTML was clobbered on rebuild")
	}
	if !strings.Contains(body, "/* V2 */") {
		t.Error("V2 fragment body not present after rebuild")
	}
	if strings.Contains(body, "/* V1 */") {
		t.Error("V1 fragment still present — prior block not replaced")
	}
}

// TestSessionBuild_NoFragmentsIsHarmless: a multi-file project (no
// implFragment set anywhere) should report "nothing to assemble"
// without error.
func TestSessionBuild_NoFragmentsIsHarmless(t *testing.T) {
	defer runInTempDir(t)()

	g := graph.NewGraph()
	implPath := "src/Foo.impl.ts"
	obj := graph.NewObject("defs/Foo.ts", "")
	obj.Impl = &implPath // no ImplFragment
	g.Objects["Foo"] = obj
	writeGraph(t, g)

	out, err := runSessionBuild()
	if err != nil {
		t.Fatalf("expected no error for multi-file project, got: %v", err)
	}
	if !strings.Contains(out, "no objects with implFragment") {
		t.Errorf("expected explanatory message; got: %s", out)
	}
}

// TestSessionBuild_MultipleDeliverablesIsError: every fragment-using
// object must share the same impl path.
func TestSessionBuild_MultipleDeliverablesIsError(t *testing.T) {
	defer runInTempDir(t)()

	g := graph.NewGraph()
	implA := "index.html"
	implB := "preview.html"
	fragA := "K/frags/A.js"
	fragB := "K/frags/B.js"
	a := graph.NewObject("defs/A.ts", "")
	a.Impl = &implA
	a.ImplFragment = &fragA
	b := graph.NewObject("defs/B.ts", "")
	b.Impl = &implB
	b.ImplFragment = &fragB
	g.Objects["A"] = a
	g.Objects["B"] = b
	writeGraph(t, g)
	mustWrite(t, "K/frags/A.js", "")
	mustWrite(t, "K/frags/B.js", "")

	_, err := runSessionBuild()
	if err == nil {
		t.Fatal("expected error for multi-deliverable graph")
	}
	if !strings.Contains(err.Error(), "multiple deliverables") {
		t.Errorf("expected 'multiple deliverables' in error; got: %v", err)
	}
}

// v9.0.6.4: any fragment containing a top-level `function Foo` where
// Foo is NOT a graph object id or ImplSymbol must cause session_build
// to refuse. This is the hard gate against unverified code shipping.
func TestSessionBuild_RefusesUnmodeledFunction(t *testing.T) {
	defer runInTempDir(t)()

	g := graph.NewGraph()
	implPath := "index.html"
	fragPath := "K/frags/A.js"
	g.Attributes["x"] = graph.NewAttribute("defs/x.ts", "")
	a := graph.NewObject("defs/A.ts", "")
	a.Impl = &implPath
	a.ImplFragment = &fragPath
	a.Produces = []string{"x"}
	g.Objects["A"] = a
	writeGraph(t, g)

	// Frag declares A (modeled) AND ghost (NOT modeled).
	mustWrite(t, fragPath, `function A(input) {
  if (input > 0) return input;
  return 0;
}
function ghost(x) {
  return x + 1;
}
`)
	_, err := runSessionBuild()
	if err == nil {
		t.Fatal("expected session_build to refuse on unmodeled function")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the unmodeled function 'ghost'; got: %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "not modeled") {
		t.Errorf("error should say 'not modeled'; got: %v", err)
	}
}

// Closures, arrow functions, methods on objects are NOT top-level
// function declarations and should pass through fine.
func TestSessionBuild_AllowsClosuresAndArrows(t *testing.T) {
	defer runInTempDir(t)()

	g := graph.NewGraph()
	implPath := "index.html"
	fragPath := "K/frags/A.js"
	a := graph.NewObject("defs/A.ts", "")
	a.Impl = &implPath
	a.ImplFragment = &fragPath
	g.Objects["A"] = a
	writeGraph(t, g)

	// `helper` is an arrow (scoped) — fine. `_inner` is inside A's body — fine.
	mustWrite(t, fragPath, `function A(input) {
  const helper = (n) => n * 2;
  function _inner(x) { return helper(x); }
  return _inner(input);
}
`)
	out, err := runSessionBuild()
	if err != nil {
		t.Fatalf("closures/arrows should not trip the unmodeled-function gate; got: %v\nout: %s", err, out)
	}
}

// Object's ImplSymbol declaration is allowed alongside the id.
func TestSessionBuild_AllowsImplSymbol(t *testing.T) {
	defer runInTempDir(t)()

	g := graph.NewGraph()
	implPath := "index.html"
	fragPath := "K/frags/A.js"
	a := graph.NewObject("defs/A.ts", "")
	a.Impl = &implPath
	a.ImplFragment = &fragPath
	a.ImplSymbol = "processA"
	g.Objects["A"] = a
	writeGraph(t, g)

	mustWrite(t, fragPath, `function processA(input) {
  for (let i = 0; i < input; i++) {}
  return input;
}
`)
	if _, err := runSessionBuild(); err != nil {
		t.Errorf("ImplSymbol-named function should pass; got: %v", err)
	}
}

// TestSessionBuild_MissingFragmentFileIsError: declared implFragment
// but file not on disk should fail explicitly.
func TestSessionBuild_MissingFragmentFileIsError(t *testing.T) {
	defer runInTempDir(t)()

	g := graph.NewGraph()
	implPath := "index.html"
	fragPath := "K/frags/Missing.js"
	obj := graph.NewObject("defs/Missing.ts", "")
	obj.Impl = &implPath
	obj.ImplFragment = &fragPath
	g.Objects["Missing"] = obj
	writeGraph(t, g)
	// note: NOT writing K/frags/Missing.js

	_, err := runSessionBuild()
	if err == nil {
		t.Fatal("expected error for missing fragment file")
	}
	if !strings.Contains(err.Error(), "fragment file(s) missing") {
		t.Errorf("expected 'fragment file(s) missing' in error; got: %v", err)
	}
}

// TestSessionBuild_CycleDetected: produces/consumes cycle between
// fragment-using objects must be reported, not crashed.
func TestSessionBuild_CycleDetected(t *testing.T) {
	defer runInTempDir(t)()

	g := graph.NewGraph()
	implPath := "index.html"
	g.Attributes["x"] = graph.NewAttribute("defs/x.ts", "")
	g.Attributes["y"] = graph.NewAttribute("defs/y.ts", "")
	// A produces x, consumes y; B produces y, consumes x. Cycle.
	for _, c := range []struct {
		id       string
		produces []string
		consumes []string
	}{
		{"A", []string{"x"}, []string{"y"}},
		{"B", []string{"y"}, []string{"x"}},
	} {
		fragPath := "K/frags/" + c.id + ".js"
		obj := graph.NewObject("defs/"+c.id+".ts", "")
		obj.Impl = &implPath
		obj.ImplFragment = &fragPath
		obj.Produces = c.produces
		obj.Consumes = c.consumes
		g.Objects[c.id] = obj
		mustWrite(t, fragPath, "")
	}
	writeGraph(t, g)

	_, err := runSessionBuild()
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected cycle in error; got: %v", err)
	}
}

// ---- small file helpers ----

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
