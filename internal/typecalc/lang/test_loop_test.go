package lang

import (
	"context"
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc"
)

func TestTestLoop_PassFirstTry(t *testing.T) {
	env := &typecalc.RuleEnv{
		MaxRetries: 3,
		TestInvoker: func(ctx context.Context, env *typecalc.RuleEnv, compiled, suite *typecalc.TypedValue) (*typecalc.TypedValue, error) {
			return compiled.WithState(typecalc.StateTestedPass), nil
		},
	}
	compiled := typecalc.New(typecalc.KindCode, "code").WithState(typecalc.StateCompiled).WithLang(typecalc.LangGo)
	suite := typecalc.New(typecalc.KindTestSuite, "expect(x).toBe(1)").WithLang(typecalc.LangGo)
	out, err := TestLoop(context.Background(), env, compiled, suite, "desc", "sig", TestLoopHooks{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State != typecalc.StateTestedPass {
		t.Fatalf("got %s", out.Tag())
	}
}

func TestTestLoop_TestWrong_RegeneratesSuite(t *testing.T) {
	calls := 0
	env := &typecalc.RuleEnv{
		MaxRetries: 3,
		TestInvoker: func(ctx context.Context, env *typecalc.RuleEnv, compiled, suite *typecalc.TypedValue) (*typecalc.TypedValue, error) {
			calls++
			if calls == 1 {
				return typecalc.NewTestError("case", "1", "0"), nil
			}
			return compiled.WithState(typecalc.StateTestedPass), nil
		},
	}
	hooks := TestLoopHooks{
		ReviewError: func(ctx context.Context, env *typecalc.RuleEnv, _, _, _ string) (*TestReviewResult, error) {
			return &TestReviewResult{Verdict: TestReviewWrong, Reason: "test asserts wrong value"}, nil
		},
		RegenerateTest: func(ctx context.Context, env *typecalc.RuleEnv, _, _, _ string) (*typecalc.TypedValue, error) {
			return typecalc.New(typecalc.KindTestSuite, "expect(x).toBe(0)"), nil
		},
	}
	compiled := typecalc.New(typecalc.KindCode, "code").WithState(typecalc.StateCompiled).WithLang(typecalc.LangGo)
	suite := typecalc.New(typecalc.KindTestSuite, "expect(x).toBe(1)").WithLang(typecalc.LangGo)
	out, err := TestLoop(context.Background(), env, compiled, suite, "d", "s", hooks)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != typecalc.StateTestedPass {
		t.Fatalf("got %s", out.Tag())
	}
}

func TestTestLoop_DescriptionUnclear_Escalates(t *testing.T) {
	env := &typecalc.RuleEnv{
		MaxRetries: 3,
		TestInvoker: func(ctx context.Context, env *typecalc.RuleEnv, compiled, suite *typecalc.TypedValue) (*typecalc.TypedValue, error) {
			return typecalc.NewTestError("case", "?", "?"), nil
		},
	}
	hooks := TestLoopHooks{
		ReviewError: func(ctx context.Context, env *typecalc.RuleEnv, _, _, _ string) (*TestReviewResult, error) {
			return &TestReviewResult{Verdict: TestReviewDescriptionUnclear, Reason: "spec ambiguous"}, nil
		},
	}
	compiled := typecalc.New(typecalc.KindCode, "code").WithState(typecalc.StateCompiled).WithLang(typecalc.LangGo)
	suite := typecalc.New(typecalc.KindTestSuite, "expect(x).toBe(1)").WithLang(typecalc.LangGo)
	out, err := TestLoop(context.Background(), env, compiled, suite, "d", "s", hooks)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != typecalc.KindClarificationReq {
		t.Fatalf("expected ClarificationNeeded, got %s", out.Tag())
	}
}
