package harness

import (
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc"
)

func TestRender_JavaScript_EmbedsCases(t *testing.T) {
	te := &typecalc.TestsEvidence{
		ObjectID: "Foo",
		Lang:     "JavaScript",
		Cases: []typecalc.TestCase{
			{
				Name: "happy path",
				Call: "Foo()",
				Expect: []typecalc.Expectation{
					{Port: "y", Type: "number"},
				},
			},
		},
	}
	src, ok := Render(RenderInputs{Tests: te, InputPorts: []string{"x"}, OutputPorts: []string{"y"}, ImplPath: "/tmp/src/foo.js", TracePath: "/tmp/.kcpos/typecalc-runtime/Foo.json", PortObservation: map[string]string{"x": "global", "y": "return.value"}})
	if !ok {
		t.Fatal("expected harness rendered")
	}
	if !strings.Contains(src, "node:test") {
		t.Fatalf("expected node:test import, got: %s", src[:200])
	}
	if !strings.Contains(src, "happy path") {
		t.Fatalf("case name not embedded")
	}
	if !strings.Contains(src, `"Foo"`) {
		t.Fatalf("objectId not embedded")
	}
	// trace-before-assert ordering: appendTrace MUST appear before any
	// assert.ok in the rendered file.
	idxAppend := strings.Index(src, "appendTrace(")
	idxAssert := strings.Index(src, "assert.ok(")
	if idxAppend < 0 || idxAssert < 0 || idxAppend > idxAssert {
		t.Fatalf("appendTrace must come before assert.ok in harness; got append=%d assert=%d", idxAppend, idxAssert)
	}
}

func TestRender_FallsBackForUnsupportedLang(t *testing.T) {
	te := &typecalc.TestsEvidence{
		ObjectID: "Foo",
		Lang:     "Rust",
		Cases:    []typecalc.TestCase{{Name: "x", Call: "foo()"}},
	}
	if _, ok := Render(RenderInputs{Tests: te, ImplPath: "/tmp/src/foo.rs", TracePath: "/tmp/.kcpos/typecalc-runtime/Foo.json"}); ok {
		t.Fatal("expected no harness for Rust (yet)")
	}
}

func TestRender_NoCasesNoHarness(t *testing.T) {
	te := &typecalc.TestsEvidence{
		ObjectID: "Foo",
		Lang:     "JavaScript",
		TestCode: "// raw legacy code",
	}
	if _, ok := Render(RenderInputs{Tests: te, ImplPath: "/tmp/src/foo.js", TracePath: "/tmp/.kcpos/typecalc-runtime/Foo.json"}); ok {
		t.Fatal("expected no harness when Cases is empty (use TestCode instead)")
	}
}
