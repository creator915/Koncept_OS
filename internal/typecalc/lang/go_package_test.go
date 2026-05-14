package lang

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// v9.6 regression tests for the two Go runner fixes uncovered by the
// 2026-05-14 walk batch:
//   1. stageGoPackage must copy sibling .go files so impl that
//      references types in K/defs/ compiles.
//   2. runGoTest must inject a trace helper file AND reject testCode
//      that omits appendTrace(...) — otherwise tests run but produce
//      no runtime trace and the chain stalls on runtime-trace-missing.

func skipIfNoGo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not available")
	}
}

// stageGoPackage with an impl that references a type defined in a
// sibling K/defs/ file should compile cleanly via `go vet ./...` —
// the pre-v9.6 single-file isolation would fail with "undefined: X".
func TestStageGoPackage_PullsSiblingDefs(t *testing.T) {
	skipIfNoGo(t)
	workdir := t.TempDir()
	defsDir := filepath.Join(workdir, "K", "defs")
	if err := os.MkdirAll(defsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Sibling type declaration in K/defs/.
	if err := os.WriteFile(filepath.Join(defsDir, "config.go"),
		[]byte("package main\n\ntype Config struct{ N int }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Impl in src/, referencing the sibling type.
	srcDir := filepath.Join(workdir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	implPath := filepath.Join(srcDir, "demo.go")
	implBody := "package main\n\nfunc Demo() Config { return Config{N: 7} }\n"
	if err := os.WriteFile(implPath, []byte(implBody), 0o644); err != nil {
		t.Fatal(err)
	}

	env := &core.RuleEnv{WorkDir: workdir, ImplPath: implPath}
	src := core.New(core.KindCode, implBody).WithState(core.StateUncompiled).WithLang(core.LangGo)
	out, err := runGoCompile(context.Background(), env, src)
	if err != nil {
		t.Fatalf("runGoCompile errored: %v", err)
	}
	if out.State != core.StateCompiled {
		t.Fatalf("expected Compiled state, got %s; payload=%v", out.Tag(), out.Payload)
	}
}

// runGoTest with testCode that DOES NOT call appendTrace must be
// rejected before running, so the chain doesn't waste a turn producing
// trace-less evidence that downstream review then complains about.
func TestRunGoTest_RejectsTestCodeWithoutAppendTrace(t *testing.T) {
	skipIfNoGo(t)
	env := &core.RuleEnv{
		WorkDir:  t.TempDir(),
		ImplPath: "code.go",
	}
	compiled := core.New(core.KindCode, "package main\n\nfunc Foo() int { return 1 }\n").
		WithState(core.StateCompiled).WithLang(core.LangGo)
	// Test code without appendTrace — should be rejected.
	badTest := `package main

import "testing"

func TestFoo(t *testing.T) {
	if Foo() != 1 { t.Error("nope") }
}`
	suite := core.New(core.KindTestSuite, badTest).WithLang(core.LangGo)
	result, err := runGoTest(context.Background(), env, compiled, suite)
	if err != nil {
		t.Fatalf("unexpected error (should be soft TestError): %v", err)
	}
	if result.State == core.StateTestedPass {
		t.Fatalf("expected rejection, got TestedPass")
	}
	if !strings.Contains(result.Payload, "appendTrace") {
		t.Errorf("error payload should reference appendTrace: %s", result.Payload)
	}
}

// v9.6.1 regression: K/defs/<PascalCase>.go is an object def STUB
// (created at graph_create_object time with a panic body). Staging it
// alongside the real impl in a Go scratch dir would redeclare the same
// function — the 2026-05-14 fx batch hit this. The PascalCase basename
// under K/defs/ must be skipped during the sibling sweep; snake_case
// attribute def files (which hold type decls the impl references) must
// still be copied.
func TestStageGoPackage_SkipsObjectDefStub(t *testing.T) {
	skipIfNoGo(t)
	workdir := t.TempDir()
	defsDir := filepath.Join(workdir, "K", "defs")
	if err := os.MkdirAll(defsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Attribute type decl — must be staged.
	if err := os.WriteFile(filepath.Join(defsDir, "bar_data.go"),
		[]byte("package main\n\ntype BarData struct{ N int }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Object def stub — would redeclare Foo if staged.
	if err := os.WriteFile(filepath.Join(defsDir, "Foo.go"),
		[]byte("package main\n\nfunc Foo() BarData { panic(\"stub\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcDir := filepath.Join(workdir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	implPath := filepath.Join(srcDir, "foo.go")
	implBody := "package main\n\nfunc Foo() BarData { return BarData{N: 1} }\n"
	if err := os.WriteFile(implPath, []byte(implBody), 0o644); err != nil {
		t.Fatal(err)
	}
	env := &core.RuleEnv{WorkDir: workdir, ImplPath: implPath}
	src := core.New(core.KindCode, implBody).WithState(core.StateUncompiled).WithLang(core.LangGo)
	out, err := runGoCompile(context.Background(), env, src)
	if err != nil {
		t.Fatalf("runGoCompile errored: %v", err)
	}
	if out.State != core.StateCompiled {
		t.Fatalf("expected Compiled, got %s\npayload=%v", out.Tag(), out.Payload)
	}
}

// End-to-end: write impl + sibling def + testCode that calls
// appendTrace, run runGoTest, verify the trace bundle is on disk with
// the recorded call.
func TestRunGoTest_TraceHelperWritesBundle(t *testing.T) {
	skipIfNoGo(t)
	workdir := t.TempDir()
	defsDir := filepath.Join(workdir, "K", "defs")
	if err := os.MkdirAll(defsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	implPath := filepath.Join(defsDir, "add.go")
	implBody := "package main\n\nfunc Add(a, b int) int { return a + b }\n"
	if err := os.WriteFile(implPath, []byte(implBody), 0o644); err != nil {
		t.Fatal(err)
	}
	tracePath := filepath.Join(workdir, ".kcpos", "typecalc", "Add.json")
	env := &core.RuleEnv{
		WorkDir:   workdir,
		ImplPath:  implPath,
		TracePath: tracePath,
		ObjectID:  "Add",
	}
	compiled := core.New(core.KindCode, implBody).WithState(core.StateCompiled).WithLang(core.LangGo)
	goodTest := `package main

import "testing"

func TestAdd_Two(t *testing.T) {
	in := map[string]interface{}{"a": 2, "b": 3}
	got := Add(2, 3)
	out := map[string]interface{}{"sum": got}
	appendTrace(in, out)
	if got != 5 { t.Errorf("want 5 got %d", got) }
}`
	suite := core.New(core.KindTestSuite, goodTest).WithLang(core.LangGo)
	result, err := runGoTest(context.Background(), env, compiled, suite)
	if err != nil {
		t.Fatalf("runGoTest errored: %v", err)
	}
	if result.State != core.StateTestedPass {
		t.Fatalf("expected TestedPass, got %s. payload=%v", result.Tag(), result.Payload)
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("trace bundle not written at %s: %v", tracePath, err)
	}
	var bundle map[string]any
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("trace bundle not valid JSON: %v\n%s", err, data)
	}
	rt, _ := bundle["runtimeTrace"].(map[string]any)
	calls, _ := rt["calls"].([]any)
	if len(calls) != 1 {
		t.Errorf("expected 1 traced call, got %d: %s", len(calls), data)
	}
}
