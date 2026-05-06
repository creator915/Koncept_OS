package graph

import (
	"os"
	"strings"
	"testing"
)

// helper: assert that the report contains exactly the given (rule, severity)
// pairs, in any order. Extra issues fail the test.
func assertIssues(t *testing.T, r *ValidationReport, want map[string]Severity) {
	t.Helper()
	got := map[string]Severity{}
	for _, i := range r.Issues {
		key := string(i.Severity) + ":" + i.Rule + ":" + i.Where
		got[key] = i.Severity
	}
	for key, sev := range want {
		if got[key] != sev {
			t.Errorf("missing expected issue %s (severity %s); report:\n%s", key, sev, r.String())
		}
	}
	for key := range got {
		if _, ok := want[key]; !ok {
			t.Errorf("unexpected issue %s; report:\n%s", key, r.String())
		}
	}
}

// helper: assert at least one issue matches rule + severity.
func assertHasIssue(t *testing.T, r *ValidationReport, rule string, sev Severity) {
	t.Helper()
	for _, i := range r.Issues {
		if i.Rule == rule && i.Severity == sev {
			return
		}
	}
	t.Errorf("expected at least one [%s] %s issue; report:\n%s", sev, rule, r.String())
}

func TestValidate_EmptyGraphPasses(t *testing.T) {
	g := NewGraph()
	r := g.Validate("")
	if r.HasErrors() {
		t.Fatalf("empty graph should have no errors; report:\n%s", r.String())
	}
}

func TestValidate_DanglingConsumeReference(t *testing.T) {
	g := NewGraph()
	if err := g.AddObject("Op", NewObject("defs/Op.ts", "an op")); err != nil {
		t.Fatal(err)
	}
	// Manually inject a consume of a non-existent attribute (bypassing LinkConsume's check).
	g.Objects["Op"].Consumes = []string{"missing"}

	r := g.Validate("")
	assertHasIssue(t, r, "reference-integrity", Error)
}

func TestValidate_NamingNamespaceConflict(t *testing.T) {
	g := NewGraph()
	g.Attributes["foo"] = NewAttribute("defs/foo.ts", "x")
	g.Objects["foo"] = NewObject("defs/foo.ts", "x")
	r := g.Validate("")
	assertHasIssue(t, r, "naming-uniqueness", Error)
}

func TestValidate_RefinesCycle(t *testing.T) {
	g := NewGraph()
	if err := g.AddAttribute("a", NewAttribute("defs/a.ts", "a")); err != nil {
		t.Fatal(err)
	}
	if err := g.AddAttribute("b", NewAttribute("defs/b.ts", "b")); err != nil {
		t.Fatal(err)
	}
	// a refines b, b refines a → cycle
	if err := g.LinkRefine("a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := g.LinkRefine("b", "a"); err != nil {
		t.Fatal(err)
	}
	r := g.Validate("")
	assertHasIssue(t, r, "refines-dag", Error)
}

func TestValidate_ProduceConsumeBalance_Direct(t *testing.T) {
	g := NewGraph()
	g.AddAttribute("x", NewAttribute("defs/x.ts", "x"))
	g.AddObject("Producer", NewObject("defs/p.ts", "p"))
	g.AddObject("Consumer", NewObject("defs/c.ts", "c"))
	g.LinkProduce("Producer", "x")
	g.LinkConsume("Consumer", "x")
	r := g.Validate("")
	for _, i := range r.Issues {
		if i.Rule == "produce-consume-balance" && i.Severity == Error {
			t.Errorf("balanced direct flow should not error: %s", i.Message)
		}
	}
}

func TestValidate_ProduceConsumeBalance_ViaSubtype(t *testing.T) {
	g := NewGraph()
	g.AddAttribute("temperature", NewAttribute("defs/t.ts", "general temp"))
	g.AddAttribute("temperature_celsius", NewAttribute("defs/tc.ts", "celsius"))
	g.LinkRefine("temperature_celsius", "temperature")

	g.AddObject("Sensor", NewObject("defs/s.ts", "produces celsius"))
	g.AddObject("Display", NewObject("defs/d.ts", "consumes general temp"))
	g.LinkProduce("Sensor", "temperature_celsius")
	g.LinkConsume("Display", "temperature")

	r := g.Validate("")
	for _, i := range r.Issues {
		if i.Rule == "produce-consume-balance" && i.Severity == Error {
			t.Errorf("subtype substitution should satisfy consumer: %s", i.Message)
		}
	}
}

func TestValidate_ProduceConsumeBalance_NoProducer(t *testing.T) {
	g := NewGraph()
	g.AddAttribute("orphan_input", NewAttribute("defs/oi.ts", "x"))
	g.AddObject("Consumer", NewObject("defs/c.ts", "c"))
	g.LinkConsume("Consumer", "orphan_input")
	r := g.Validate("")
	assertHasIssue(t, r, "produce-consume-balance", Error)
}

func TestValidate_TemporalCausality_Past(t *testing.T) {
	g := NewGraph()
	g.AddAttribute("v", NewAttribute("defs/v.ts", "velocity"))
	o := NewObject("defs/u.ts", "update")
	o.Temporal = &Temporal{
		FrameVar: "e",
		Consumes: []FrameRef{{Attribute: "v", Frame: "e.succ()"}}, // depth 1
		Produces: []FrameRef{{Attribute: "v", Frame: "e"}},        // depth 0 — past
	}
	g.AddObject("Op", o)
	r := g.Validate("")
	assertHasIssue(t, r, "temporal-consistency", Error)
}

func TestValidate_TemporalCausality_OK(t *testing.T) {
	g := NewGraph()
	g.AddAttribute("v", NewAttribute("defs/v.ts", "velocity"))
	o := NewObject("defs/u.ts", "update")
	o.Temporal = &Temporal{
		FrameVar: "e",
		Consumes: []FrameRef{{Attribute: "v", Frame: "e"}},
		Produces: []FrameRef{{Attribute: "v", Frame: "e.succ()"}},
	}
	g.AddObject("Op", o)
	r := g.Validate("")
	for _, i := range r.Issues {
		if i.Rule == "temporal-consistency" && i.Severity == Error {
			t.Errorf("e → e.succ() should be valid causality: %s", i.Message)
		}
	}
}

func TestValidate_TemporalSyntax_Bad(t *testing.T) {
	g := NewGraph()
	g.AddAttribute("v", NewAttribute("defs/v.ts", "v"))
	o := NewObject("defs/u.ts", "u")
	o.Temporal = &Temporal{
		FrameVar: "e",
		Consumes: []FrameRef{{Attribute: "v", Frame: "e.foo()"}},
		Produces: []FrameRef{{Attribute: "v", Frame: "e.succ()"}},
	}
	g.AddObject("Op", o)
	r := g.Validate("")
	assertHasIssue(t, r, "temporal-consistency", Error)
}

func TestParseFrameDepth(t *testing.T) {
	cases := []struct {
		expr     string
		frameVar string
		depth    int
		ok       bool
	}{
		{"e", "e", 0, true},
		{"e.succ()", "e", 1, true},
		{"e.succ().succ()", "e", 2, true},
		{"e.succ().succ().succ()", "e", 3, true},
		{"  e.succ()  ", "e", 1, true},
		{"e.foo()", "e", 0, false},
		{"f", "e", 0, false},
		{"e.succ", "e", 0, false},
		{"e.succ()x", "e", 0, false},
	}
	for _, c := range cases {
		d, ok := parseFrameDepth(c.expr, c.frameVar)
		if d != c.depth || ok != c.ok {
			t.Errorf("parseFrameDepth(%q, %q) = (%d, %v), want (%d, %v)", c.expr, c.frameVar, d, ok, c.depth, c.ok)
		}
	}
}

func TestValidate_OrphanWarning(t *testing.T) {
	g := NewGraph()
	g.AddAttribute("y", NewAttribute("defs/y.ts", "terminal output"))
	g.AddObject("Producer", NewObject("defs/p.ts", "p"))
	g.LinkProduce("Producer", "y")
	r := g.Validate("")
	assertHasIssue(t, r, "orphan-attribute", Warn)
}

func TestValidate_MetadataMissing(t *testing.T) {
	g := NewGraph()
	g.AddAttribute("x", NewAttribute("", "")) // empty def & intent
	r := g.Validate("")
	// Expect 2 warnings: empty def + empty intent for attribute "x"
	got := 0
	for _, i := range r.Issues {
		if i.Rule == "metadata-completeness" && i.Severity == Warn && i.Where == "x" {
			got++
		}
	}
	if got != 2 {
		t.Errorf("expected 2 metadata-completeness warns for 'x', got %d; report:\n%s", got, r.String())
	}
}

func TestValidate_DefMissing(t *testing.T) {
	// def points at a phantom file path → def-existence WARN.
	g := NewGraph()
	g.AddAttribute("a", NewAttribute("defs/phantom_attr.ts", "intent"))
	g.AddObject("Op", NewObject("defs/PhantomOp.ts", "intent"))
	r := g.Validate(t.TempDir()) // empty dir, defs don't exist
	gotAttr, gotObj := false, false
	for _, i := range r.Issues {
		if i.Rule == "def-existence" && i.Severity == Warn {
			if i.Where == "a" {
				gotAttr = true
			}
			if i.Where == "Op" {
				gotObj = true
			}
		}
	}
	if !gotAttr || !gotObj {
		t.Errorf("expected def-existence WARN for both attribute and object; report:\n%s", r.String())
	}
}

func TestValidate_DefPresent_NoWarn(t *testing.T) {
	dir := t.TempDir()
	defPath := dir + "/defs/a.ts"
	if err := os.MkdirAll(dir+"/defs", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defPath, []byte("export type A = number\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	g := NewGraph()
	g.AddAttribute("a", NewAttribute("defs/a.ts", "intent"))
	r := g.Validate(dir)
	for _, i := range r.Issues {
		if i.Rule == "def-existence" && i.Where == "a" {
			t.Errorf("def-existence should not warn when file exists: %s", i.Message)
		}
	}
}

func TestValidate_ImplMissing(t *testing.T) {
	g := NewGraph()
	g.AddAttribute("a", NewAttribute("defs/a.ts", "a"))
	g.AddObject("Op", NewObject("defs/op.ts", "op"))
	g.LinkProduce("Op", "a")
	missing := "src/does_not_exist.ts"
	g.Objects["Op"].Impl = &missing
	r := g.Validate(t.TempDir()) // a clean dir, file definitely doesn't exist
	assertHasIssue(t, r, "impl-existence", Warn)
}

func TestValidate_ReportFormat(t *testing.T) {
	g := NewGraph()
	g.AddObject("Op", NewObject("defs/op.ts", "op"))
	g.Objects["Op"].Consumes = []string{"missing"}
	r := g.Validate("")
	out := r.String()
	if !strings.Contains(out, "FAIL") {
		t.Errorf("expected FAIL verdict in report:\n%s", out)
	}
	if !strings.Contains(out, "reference-integrity") {
		t.Errorf("expected rule name in report:\n%s", out)
	}
}
