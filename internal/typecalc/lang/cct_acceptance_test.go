package lang

import (
	"context"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// P1.2.GATE end-to-end for the Code-Compile-Test Handler: one artifact
// flows compile → test on the SAME source. Three paths the doc mandates:
//   1. valid code  → Compiled → Tested<Pass>
//   2. syntax error → CompileError carrying the full compiler output
//   3. logic wrong  → Compiled, then TestError (brownfield artifact),
//      NOT a back-to-LLM rewrite directive
// (Confirmed-writes-sub-graph is the separate P1.2.3 services test.)

func TestCCT_EndToEnd_ValidCompilesAndTests(t *testing.T) {
	if !commandExists("gcc") {
		t.Skip("gcc absent — end-to-end needs a real toolchain")
	}
	impl := "int add(int a,int b){return a+b;}\n"
	src := core.New(core.KindCode, impl).WithLang(core.LangC).WithState(core.StateUncompiled)
	compiled, err := CompileLanguageInvoker(context.Background(), &core.RuleEnv{}, src)
	if err != nil || compiled.State != core.StateCompiled {
		t.Fatalf("valid C must reach Compiled, got %s/%s err=%v", compiled.Kind, compiled.State, err)
	}
	suite := core.New(core.KindTestSuite,
		"int add(int,int);\nint main(){ return add(2,3)==5 ? 0 : 1; }\n")
	tested, err := TestRunInvoker(context.Background(), &core.RuleEnv{}, compiled, suite)
	if err != nil || tested.State != core.StateTestedPass {
		t.Fatalf("valid suite must reach Tested<Pass>, got %s/%s", tested.Kind, tested.State)
	}
}

func TestCCT_EndToEnd_SyntaxErrorIsCompileError(t *testing.T) {
	if !commandExists("gcc") {
		t.Skip("gcc absent")
	}
	src := core.New(core.KindCode, "int add(int a, { broken\n").
		WithLang(core.LangC).WithState(core.StateUncompiled)
	out, err := CompileLanguageInvoker(context.Background(), &core.RuleEnv{}, src)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != core.KindCompileError {
		t.Fatalf("syntax error must yield CompileError, got %s", out.Kind)
	}
	if strings.TrimSpace(out.Payload) == "" {
		t.Error("CompileError must carry the full compiler output")
	}
}

func TestCCT_EndToEnd_LogicWrongIsTestErrorNotRewrite(t *testing.T) {
	if !commandExists("gcc") {
		t.Skip("gcc absent")
	}
	impl := "int add(int a,int b){return a+b;}\n"
	compiled := core.New(core.KindCode, impl).WithLang(core.LangC).WithState(core.StateCompiled)
	suite := core.New(core.KindTestSuite,
		"int add(int,int);\nint main(){ return add(2,3)==999 ? 0 : 1; }\n")
	out, err := TestRunInvoker(context.Background(), &core.RuleEnv{}, compiled, suite)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != core.KindTestError {
		t.Fatalf("logic-wrong must yield TestError, got %s", out.Kind)
	}
	if out.Kind == core.KindClarificationReq {
		t.Fatal("TestError must NOT be a back-to-LLM rewrite/clarification request")
	}
}
