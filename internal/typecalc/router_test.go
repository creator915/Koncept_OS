package typecalc

import (
	"context"
	"testing"
)

func TestRouter_DispatchesByTag(t *testing.T) {
	reg := NewRegistry()
	calls := 0
	err := reg.Register(&Rule{
		Name:   "promote_uncompiled",
		Actor:  ActorSystem,
		Input:  []Tag{{Kind: KindCode, State: StateUncompiled}},
		Output: SumType{{Kind: KindCode, State: StateCompiled}},
		Handler: func(ctx context.Context, env *RuleEnv, inputs ...*TypedValue) (*TypedValue, error) {
			calls++
			return inputs[0].WithState(StateCompiled), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	router := &Router{Registry: reg, Env: &RuleEnv{}, MaxSteps: 5}
	in := New(KindCode, "package x\nfunc f() {}").
		WithLang(LangGo).
		WithState(StateUncompiled)
	out, err := router.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.State != StateCompiled {
		t.Fatalf("expected Compiled, got %v", out.State)
	}
	if calls != 1 {
		t.Fatalf("expected 1 dispatch, got %d", calls)
	}
}

func TestRouter_HaltsOnTerminalKind(t *testing.T) {
	reg := NewRegistry()
	router := &Router{Registry: reg, Env: &RuleEnv{}}
	in := NewObstacle("t", "stuck", nil)
	out, err := router.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Kind != KindObstacle {
		t.Fatalf("got %v", out.Kind)
	}
}

func TestRouter_HaltsOnConfirmedState(t *testing.T) {
	reg := NewRegistry()
	router := &Router{Registry: reg, Env: &RuleEnv{}}
	in := New(KindCode, "code").WithState(StateConfirmed)
	out, err := router.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.State != StateConfirmed {
		t.Fatalf("got %v", out.State)
	}
}

func TestRouter_BoundedByMaxSteps(t *testing.T) {
	reg := NewRegistry()
	// Pathological rule that never converges.
	_ = reg.Register(&Rule{
		Name:   "loop",
		Actor:  ActorSystem,
		Input:  []Tag{{Kind: KindReason}},
		Output: SumType{{Kind: KindReason}},
		Handler: func(ctx context.Context, env *RuleEnv, inputs ...*TypedValue) (*TypedValue, error) {
			return inputs[0], nil
		},
	})
	router := &Router{Registry: reg, Env: &RuleEnv{}, MaxSteps: 3}
	in := New(KindReason, "stuck")
	_, err := router.Run(context.Background(), in)
	if err == nil {
		t.Fatal("expected MaxSteps error")
	}
}

func TestRegistry_MatchPicksMostSpecific(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&Rule{
		Name:   "broad",
		Actor:  ActorSystem,
		Input:  []Tag{{Kind: KindCode}},
		Output: SumType{{Kind: KindCode, State: StateCompiled}},
		Handler: func(ctx context.Context, env *RuleEnv, inputs ...*TypedValue) (*TypedValue, error) {
			return inputs[0].WithState(StateCompiled), nil
		},
	})
	_ = reg.Register(&Rule{
		Name:   "narrow",
		Actor:  ActorCompiler,
		Input:  []Tag{{Kind: KindCode, State: StateUncompiled, Lang: LangGo}},
		Output: SumType{{Kind: KindCode, State: StateCompiled}},
		Handler: func(ctx context.Context, env *RuleEnv, inputs ...*TypedValue) (*TypedValue, error) {
			return inputs[0].WithState(StateCompiled), nil
		},
	})
	rule, ok := reg.Match(Tag{Kind: KindCode, State: StateUncompiled, Lang: LangGo})
	if !ok || rule.Name != "narrow" {
		t.Fatalf("specific rule should win; got %+v ok=%v", rule, ok)
	}
}
