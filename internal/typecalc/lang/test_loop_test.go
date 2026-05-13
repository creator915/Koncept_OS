package lang

import (
	"context"
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

func TestTestLoop_PassFirstTry(t *testing.T) {
	env := &core.RuleEnv{
		MaxRetries: 3,
		TestInvoker: func(ctx context.Context, env *core.RuleEnv, compiled, suite *core.TypedValue) (*core.TypedValue, error) {
			return compiled.WithState(core.StateTestedPass), nil
		},
	}
	compiled := core.New(core.KindCode, "code").WithState(core.StateCompiled).WithLang(core.LangGo)
	suite := core.New(core.KindTestSuite, "expect(x).toBe(1)").WithLang(core.LangGo)
	out, err := TestLoop(context.Background(), env, compiled, suite, "desc", "sig", TestLoopHooks{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State != core.StateTestedPass {
		t.Fatalf("got %s", out.Tag())
	}
}

func TestTestLoop_TestWrong_RegeneratesSuite(t *testing.T) {
	calls := 0
	env := &core.RuleEnv{
		MaxRetries: 3,
		TestInvoker: func(ctx context.Context, env *core.RuleEnv, compiled, suite *core.TypedValue) (*core.TypedValue, error) {
			calls++
			if calls == 1 {
				return core.NewTestError("case", "1", "0"), nil
			}
			return compiled.WithState(core.StateTestedPass), nil
		},
	}
	hooks := TestLoopHooks{
		ReviewError: func(ctx context.Context, env *core.RuleEnv, _, _, _ string) (*TestReviewResult, error) {
			return &TestReviewResult{Verdict: TestReviewWrong, Reason: "test asserts wrong value"}, nil
		},
		RegenerateTest: func(ctx context.Context, env *core.RuleEnv, _, _, _ string) (*core.TypedValue, error) {
			return core.New(core.KindTestSuite, "expect(x).toBe(0)"), nil
		},
	}
	compiled := core.New(core.KindCode, "code").WithState(core.StateCompiled).WithLang(core.LangGo)
	suite := core.New(core.KindTestSuite, "expect(x).toBe(1)").WithLang(core.LangGo)
	out, err := TestLoop(context.Background(), env, compiled, suite, "d", "s", hooks)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != core.StateTestedPass {
		t.Fatalf("got %s", out.Tag())
	}
}

func TestTestLoop_DescriptionUnclear_Escalates(t *testing.T) {
	env := &core.RuleEnv{
		MaxRetries: 3,
		TestInvoker: func(ctx context.Context, env *core.RuleEnv, compiled, suite *core.TypedValue) (*core.TypedValue, error) {
			return core.NewTestError("case", "?", "?"), nil
		},
	}
	hooks := TestLoopHooks{
		ReviewError: func(ctx context.Context, env *core.RuleEnv, _, _, _ string) (*TestReviewResult, error) {
			return &TestReviewResult{Verdict: TestReviewDescriptionUnclear, Reason: "spec ambiguous"}, nil
		},
	}
	compiled := core.New(core.KindCode, "code").WithState(core.StateCompiled).WithLang(core.LangGo)
	suite := core.New(core.KindTestSuite, "expect(x).toBe(1)").WithLang(core.LangGo)
	out, err := TestLoop(context.Background(), env, compiled, suite, "d", "s", hooks)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != core.KindClarificationReq {
		t.Fatalf("expected ClarificationNeeded, got %s", out.Tag())
	}
}
