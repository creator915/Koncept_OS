package lang

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// P1.2.2: Rust (cargo test → single-file `rustc --test` equivalent) and
// C ("编译并运行测试二进制") test runners, plus the doc's key invariant:
// a TestError is a structured characterization artifact for brownfield
// triage — NOT an LLM "rewrite your code" instruction. The chain-level
// brownfield entry is separately locked by
// chains.TestChain_HTMLBranchSmokeFailureRetries.

func runTest(t *testing.T, impl, suite string, lang core.Lang) *core.TypedValue {
	t.Helper()
	c := core.New(core.KindCode, impl).WithLang(lang).WithState(core.StateCompiled)
	s := core.New(core.KindTestSuite, suite)
	out, err := TestRunInvoker(context.Background(), &core.RuleEnv{}, c, s)
	if err != nil {
		t.Fatalf("TestRunInvoker error: %v", err)
	}
	return out
}

func TestRun_C_Pass_SkipIfNoGcc(t *testing.T) {
	if !commandExists("gcc") {
		t.Skip("gcc not on PATH — toolchain absent, skipping")
	}
	out := runTest(t,
		"int add(int a,int b){return a+b;}\n",
		"int add(int,int);\nint main(){ if(add(2,3)!=5) return 1; return 0; }\n",
		core.LangC)
	if out.State != core.StateTestedPass {
		t.Fatalf("passing C suite must yield Tested<Pass>, got %s/%s: %s", out.Kind, out.State, out.Payload)
	}
}

func TestRun_C_Fail_SkipIfNoGcc(t *testing.T) {
	if !commandExists("gcc") {
		t.Skip("gcc not on PATH — toolchain absent, skipping")
	}
	out := runTest(t,
		"int add(int a,int b){return a+b;}\n",
		"int add(int,int);\nint main(){ if(add(2,3)!=6) return 1; return 0; }\n",
		core.LangC)
	if out.Kind != core.KindTestError {
		t.Fatalf("failing C suite must yield TestError, got %s", out.Kind)
	}
}

func TestRun_Rust_Pass_SkipIfNoToolchain(t *testing.T) {
	if !commandExists("rustc") && !commandExists("cargo") {
		t.Skip("no rustc/cargo on PATH — toolchain absent, skipping (fail-open path untested)")
	}
	out := runTest(t,
		"pub fn add(a:i32,b:i32)->i32{a+b}\n",
		"#[test]\nfn t_add(){ assert_eq!(add(2,3),5); }\n",
		core.LangRust)
	if out.State != core.StateTestedPass {
		t.Fatalf("passing Rust suite must yield Tested<Pass>, got %s/%s", out.Kind, out.State)
	}
}

func TestRun_Rust_Fail_SkipIfNoToolchain(t *testing.T) {
	if !commandExists("rustc") && !commandExists("cargo") {
		t.Skip("no rustc/cargo on PATH — toolchain absent, skipping")
	}
	out := runTest(t,
		"pub fn add(a:i32,b:i32)->i32{a+b}\n",
		"#[test]\nfn t_add(){ assert_eq!(add(2,3),6); }\n",
		core.LangRust)
	if out.Kind != core.KindTestError {
		t.Fatalf("failing Rust suite must yield TestError, got %s", out.Kind)
	}
}

// The doc's invariant: TestError 不打回 LLM——it is a structured
// (testCase, expected, actual) characterization artifact for brownfield
// "is it the code or the test" triage, distinct from
// KindClarificationReq (the only kind that bubbles a question back).
func TestRun_TestError_IsBrownfieldArtifact_NotLLMRewrite(t *testing.T) {
	if !commandExists("gcc") {
		t.Skip("gcc not on PATH — need a real failing run, skipping")
	}
	out := runTest(t,
		"int add(int a,int b){return a+b;}\n",
		"int add(int,int);\nint main(){ if(add(2,3)!=6) return 1; return 0; }\n",
		core.LangC)
	if out.Kind != core.KindTestError {
		t.Fatalf("expected TestError, got %s", out.Kind)
	}
	if out.Kind == core.KindClarificationReq {
		t.Fatal("TestError must NOT be a back-to-LLM clarification/rewrite request")
	}
	var d core.TestErrorDetail
	if err := json.Unmarshal([]byte(out.Payload), &d); err != nil {
		t.Fatalf("TestError payload must be structured TestErrorDetail: %v", err)
	}
	if d.Actual == "" {
		t.Error("TestError must carry the observed Actual — the brownfield characterization datum, not a rewrite directive")
	}
}
