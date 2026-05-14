package harness

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

func TestRender_JavaScript_EmbedsCases(t *testing.T) {
	te := &core.TestsEvidence{
		ObjectID: "Foo",
		Lang:     "JavaScript",
		Cases: []core.TestCase{
			{
				Name: "happy path",
				Call: "Foo()",
				Expect: []core.Expectation{
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
	te := &core.TestsEvidence{
		ObjectID: "Foo",
		Lang:     "Rust",
		Cases:    []core.TestCase{{Name: "x", Call: "foo()"}},
	}
	if _, ok := Render(RenderInputs{Tests: te, ImplPath: "/tmp/src/foo.rs", TracePath: "/tmp/.kcpos/typecalc-runtime/Foo.json"}); ok {
		t.Fatal("expected no harness for Rust (yet)")
	}
}

func TestRender_NoCasesNoHarness(t *testing.T) {
	te := &core.TestsEvidence{
		ObjectID: "Foo",
		Lang:     "JavaScript",
		TestCode: "// raw legacy code",
	}
	if _, ok := Render(RenderInputs{Tests: te, ImplPath: "/tmp/src/foo.js", TracePath: "/tmp/.kcpos/typecalc-runtime/Foo.json"}); ok {
		t.Fatal("expected no harness when Cases is empty (use TestCode instead)")
	}
}

func TestRender_Python_EmbedsCases(t *testing.T) {
	te := &core.TestsEvidence{
		ObjectID: "Strlen",
		Lang:     "Python",
		Cases: []core.TestCase{
			{
				Name: "empty",
				Call: "IMPL.strlen('')",
				Expect: []core.Expectation{
					{Port: "result", Equals: json.RawMessage("0")},
				},
			},
		},
	}
	src, ok := Render(RenderInputs{
		Tests:           te,
		InputPorts:      []string{"s"},
		OutputPorts:     []string{"result"},
		ImplPath:        "/tmp/src/code.py",
		TracePath:       "/tmp/.kcpos/typecalc/Strlen.json",
		PortObservation: map[string]string{"s": "global", "result": "return"},
		ImplSymbol:      "strlen",
	})
	if !ok {
		t.Fatal("expected Python harness rendered")
	}
	if !strings.Contains(src, "import unittest") {
		t.Fatalf("expected unittest import, got: %.200s", src)
	}
	if !strings.Contains(src, "Strlen") {
		t.Fatalf("objectId not embedded")
	}
	// trace-before-assert ordering: append_trace MUST appear before
	// self.assertTrue in _make_test.
	idxAppend := strings.Index(src, "append_trace(")
	idxAssert := strings.Index(src, "self.assertTrue(")
	if idxAppend < 0 || idxAssert < 0 || idxAppend > idxAssert {
		t.Fatalf("append_trace must come before self.assertTrue; got append=%d assert=%d", idxAppend, idxAssert)
	}
}

// TestRender_Python_EndToEnd renders, writes to a scratch dir alongside
// a real impl, and runs `python3 -m unittest` to confirm the harness
// actually works (cases pass, trace file is written). This catches
// integration bugs that pure rendering tests miss (Python syntax
// errors, importlib failures, JSON parse blowups).
func TestRender_Python_EndToEnd(t *testing.T) {
	python := lookPython(t)
	if python == "" {
		t.Skip("python3 not available")
	}

	dir := t.TempDir()
	implPath := filepath.Join(dir, "code.py")
	tracePath := filepath.Join(dir, ".kcpos", "typecalc", "Strlen.json")
	if err := os.WriteFile(implPath, []byte("def strlen(s):\n    n = 0\n    for _ in s:\n        n += 1\n    return n\n"), 0644); err != nil {
		t.Fatal(err)
	}

	te := &core.TestsEvidence{
		ObjectID: "Strlen",
		Lang:     "Python",
		Cases: []core.TestCase{
			{Name: "empty", Call: "IMPL.strlen('')", Expect: []core.Expectation{{Port: "result", Equals: json.RawMessage("0")}}},
			{Name: "three", Call: "IMPL.strlen('abc')", Expect: []core.Expectation{{Port: "result", Equals: json.RawMessage("3")}}},
		},
	}
	src, ok := Render(RenderInputs{
		Tests:           te,
		OutputPorts:     []string{"result"},
		ImplPath:        implPath,
		TracePath:       tracePath,
		PortObservation: map[string]string{"result": "return"},
		ImplSymbol:      "strlen",
	})
	if !ok {
		t.Fatal("expected Python harness rendered")
	}
	testPath := filepath.Join(dir, "test_code.py")
	if err := os.WriteFile(testPath, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(python, "-m", "unittest", "test_code", "-v")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("unittest failed: %v\n--- output ---\n%s", err, out)
	}
	if !strings.Contains(string(out), "OK") {
		t.Fatalf("expected unittest OK in output, got:\n%s", out)
	}
	// Trace file must exist and contain both calls.
	traceBytes, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("trace file not written: %v", err)
	}
	var bundle map[string]any
	if err := json.Unmarshal(traceBytes, &bundle); err != nil {
		t.Fatalf("trace not valid JSON: %v\n%s", err, traceBytes)
	}
	rt, _ := bundle["runtimeTrace"].(map[string]any)
	calls, _ := rt["calls"].([]any)
	if len(calls) != 2 {
		t.Fatalf("expected 2 traced calls (one per case), got %d: %s", len(calls), traceBytes)
	}
}

func lookPython(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}
