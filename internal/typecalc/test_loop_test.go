package typecalc

import (
	"context"
	"testing"
)

func TestTestLoop_PassFirstTry(t *testing.T) {
	env := &RuleEnv{
		MaxRetries: 3,
		TestInvoker: func(ctx context.Context, env *RuleEnv, compiled, suite *TypedValue) (*TypedValue, error) {
			return compiled.WithState(StateTestedPass), nil
		},
	}
	compiled := New(KindCode, "code").WithState(StateCompiled).WithLang(LangGo)
	suite := New(KindTestSuite, "expect(x).toBe(1)").WithLang(LangGo)
	out, err := TestLoop(context.Background(), env, compiled, suite, "desc", "sig", TestLoopHooks{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State != StateTestedPass {
		t.Fatalf("got %s", out.Tag())
	}
}

func TestTestLoop_TestWrong_RegeneratesSuite(t *testing.T) {
	calls := 0
	env := &RuleEnv{
		MaxRetries: 3,
		TestInvoker: func(ctx context.Context, env *RuleEnv, compiled, suite *TypedValue) (*TypedValue, error) {
			calls++
			if calls == 1 {
				return NewTestError("case", "1", "0"), nil
			}
			return compiled.WithState(StateTestedPass), nil
		},
	}
	hooks := TestLoopHooks{
		ReviewError: func(ctx context.Context, env *RuleEnv, _, _, _ string) (*TestReviewResult, error) {
			return &TestReviewResult{Verdict: TestReviewWrong, Reason: "test asserts wrong value"}, nil
		},
		RegenerateTest: func(ctx context.Context, env *RuleEnv, _, _, _ string) (*TypedValue, error) {
			return New(KindTestSuite, "expect(x).toBe(0)"), nil
		},
	}
	compiled := New(KindCode, "code").WithState(StateCompiled).WithLang(LangGo)
	suite := New(KindTestSuite, "expect(x).toBe(1)").WithLang(LangGo)
	out, err := TestLoop(context.Background(), env, compiled, suite, "d", "s", hooks)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != StateTestedPass {
		t.Fatalf("got %s", out.Tag())
	}
}

func TestTestLoop_DescriptionUnclear_Escalates(t *testing.T) {
	env := &RuleEnv{
		MaxRetries: 3,
		TestInvoker: func(ctx context.Context, env *RuleEnv, compiled, suite *TypedValue) (*TypedValue, error) {
			return NewTestError("case", "?", "?"), nil
		},
	}
	hooks := TestLoopHooks{
		ReviewError: func(ctx context.Context, env *RuleEnv, _, _, _ string) (*TestReviewResult, error) {
			return &TestReviewResult{Verdict: TestReviewDescriptionUnclear, Reason: "spec ambiguous"}, nil
		},
	}
	compiled := New(KindCode, "code").WithState(StateCompiled).WithLang(LangGo)
	suite := New(KindTestSuite, "expect(x).toBe(1)").WithLang(LangGo)
	out, err := TestLoop(context.Background(), env, compiled, suite, "d", "s", hooks)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != KindClarificationReq {
		t.Fatalf("expected ClarificationNeeded, got %s", out.Tag())
	}
}
