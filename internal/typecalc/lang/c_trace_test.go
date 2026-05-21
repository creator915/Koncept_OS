package lang

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// 2026-05-21 — C trace pipeline tests. Mirrors the v9.6 Go suite at
// go_package_test.go (TestRunGoTest_RejectsTestCodeWithoutAppendTrace +
// TestRunGoTest_TraceHelperWritesBundle).
//
// Pre-fix runCTest just glued impl + suite and ran the binary, leaving
// no runtime-trace bundle behind. PB-30 batch #4 cmatrix/figlet/tty-clock
// all reached confirm_object's review stage, which then fired Obstacle
// on "no runtime trace". The fix adds renderCTraceHelper + persistCTrace
// to give C the same shape Go has.

func TestRunCTest_RejectsTestCodeWithoutAppendTrace(t *testing.T) {
	if !commandExists("gcc") {
		t.Skip("gcc not on PATH — toolchain absent, skipping")
	}
	env := &core.RuleEnv{WorkDir: t.TempDir(), ImplPath: "code.c", ObjectID: "Foo"}
	compiled := core.New(core.KindCode, "int Foo(){ return 1; }\n").
		WithState(core.StateCompiled).WithLang(core.LangC)
	// Suite missing appendTrace — must be soft-rejected with stage-named
	// TestError so the agent sees a clear next step (regenerate tests).
	badSuite := core.New(core.KindTestSuite,
		"int Foo();\nint main(){ if(Foo()!=1) return 1; return 0; }\n").WithLang(core.LangC)
	out, err := runCTest(context.Background(), env, compiled, badSuite)
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if out.State == core.StateTestedPass {
		t.Fatal("expected rejection of testCode lacking appendTrace, got TestedPass")
	}
	if !strings.Contains(out.Payload, "appendTrace") {
		t.Errorf("error payload should reference appendTrace; got: %s", out.Payload)
	}
}

func TestRunCTest_HelperWritesBundle(t *testing.T) {
	if !commandExists("gcc") {
		t.Skip("gcc not on PATH — toolchain absent, skipping")
	}
	// chdir to a temp project root so BundlePath's relative
	// ".kcpos/typecalc/<id>.json" resolves under the test's workdir
	// (matches production: kcpos run-routed chdirs into the session
	// workdir before invoking the chain).
	workdir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(workdir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	implPath := filepath.Join(workdir, "add.c")
	implBody := "int Add(int a,int b){return a+b;}\n"
	if err := os.WriteFile(implPath, []byte(implBody), 0o644); err != nil {
		t.Fatal(err)
	}
	env := &core.RuleEnv{
		WorkDir:  workdir,
		ImplPath: implPath,
		ObjectID: "Add",
	}
	compiled := core.New(core.KindCode, implBody).
		WithState(core.StateCompiled).WithLang(core.LangC)
	suite := core.New(core.KindTestSuite, `int Add(int,int);
int main(){
    appendTrace("{\"a\":2,\"b\":3}", "{\"sum\":5}");
    if (Add(2,3) != 5) return 1;
    return 0;
}
`).WithLang(core.LangC)

	out, err := runCTest(context.Background(), env, compiled, suite)
	if err != nil {
		t.Fatalf("runCTest errored: %v", err)
	}
	if out.State != core.StateTestedPass {
		t.Fatalf("expected TestedPass, got %s payload=%s", out.State, out.Payload)
	}

	bundle, ok := core.ReadBundle("Add")
	if !ok || bundle == nil {
		t.Fatal("bundle not written to .kcpos/typecalc/Add.json")
	}
	if bundle.RuntimeTrace == nil {
		t.Fatal("bundle.RuntimeTrace is nil — persistCTrace did not record the trace")
	}
	if got := len(bundle.RuntimeTrace.Calls); got != 1 {
		t.Fatalf("expected 1 traced call, got %d", got)
	}
	call := bundle.RuntimeTrace.Calls[0]
	if string(call.Inputs["a"]) != "2" || string(call.Inputs["b"]) != "3" {
		t.Errorf("inputs not parsed correctly: %+v", call.Inputs)
	}
	if string(call.Outputs["sum"]) != "5" {
		t.Errorf("outputs not parsed correctly: %+v", call.Outputs)
	}
}

func TestRunCTest_HelperWritesPartialTraceOnAssertFail(t *testing.T) {
	// Even when the test binary exits non-zero, any trace lines that were
	// emitted before the failure must be persisted — matches Go's
	// fflush-after-call helper. The agent's brownfield characterize step
	// relies on partial traces from crashing runs.
	if !commandExists("gcc") {
		t.Skip("gcc not on PATH — toolchain absent, skipping")
	}
	workdir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(workdir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	implPath := filepath.Join(workdir, "add.c")
	implBody := "int Add(int a,int b){return a+b;}\n"
	_ = os.WriteFile(implPath, []byte(implBody), 0o644)
	env := &core.RuleEnv{WorkDir: workdir, ImplPath: implPath, ObjectID: "AddBad"}
	compiled := core.New(core.KindCode, implBody).
		WithState(core.StateCompiled).WithLang(core.LangC)
	// Test FAILS (assertion wrong) but appendTrace runs first.
	suite := core.New(core.KindTestSuite, `int Add(int,int);
int main(){
    appendTrace("{\"a\":2,\"b\":3}", "{\"sum\":5}");
    if (Add(2,3) != 999) return 1;  /* will fail */
    return 0;
}
`).WithLang(core.LangC)

	out, _ := runCTest(context.Background(), env, compiled, suite)
	if out.Kind != core.KindTestError {
		t.Fatalf("failing test must yield TestError, got %s", out.Kind)
	}
	bundle, ok := core.ReadBundle("AddBad")
	if !ok || bundle == nil || bundle.RuntimeTrace == nil {
		t.Fatal("partial trace must be persisted even on test failure")
	}
	if got := len(bundle.RuntimeTrace.Calls); got != 1 {
		t.Errorf("expected partial trace (1 call) on test failure, got %d", got)
	}
}

func TestRenderCTraceHelper_BakedInPath(t *testing.T) {
	// Sanity: the rendered helper source must embed the path as a
	// quoted C string and define appendTrace + open the file once.
	src := renderCTraceHelper("/tmp/trace.jsonl")
	if !strings.Contains(src, `"/tmp/trace.jsonl"`) {
		t.Errorf("rendered helper must bake in the trace path as a C string: %s", src)
	}
	if !strings.Contains(src, "void appendTrace(") {
		t.Errorf("rendered helper must define appendTrace")
	}
	if !strings.Contains(src, "fflush(kcpos_trace_fp)") {
		t.Errorf("rendered helper must flush after each call so partial traces survive crashes")
	}
}

func TestRenderCTraceHelper_EscapesQuotesAndBackslashes(t *testing.T) {
	// Defensive: scratch dir paths use safe alphabet, but the cString
	// escape function is the only line between us and source-code
	// injection if upstream ever passes user paths through. Verify the
	// minimal escape works.
	src := renderCTraceHelper(`/weird "quoted" \path`)
	if !strings.Contains(src, `"/weird \"quoted\" \\path"`) {
		t.Errorf("cString must escape backslash + double-quote; got: %s", src)
	}
}
