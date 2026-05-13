package rule

import (
	"context"
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

func TestRouter_DispatchesByTag(t *testing.T) {
	reg := NewRegistry()
	calls := 0
	err := reg.Register(&Rule{
		Name:   "promote_uncompiled",
		Actor:  ActorSystem,
		Input:  []core.Tag{{Kind: core.KindCode, State: core.StateUncompiled}},
		Output: core.SumType{{Kind: core.KindCode, State: core.StateCompiled}},
		Handler: func(ctx context.Context, env *core.RuleEnv, inputs ...*core.TypedValue) (*core.TypedValue, error) {
			calls++
			return inputs[0].WithState(core.StateCompiled), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	router := &Router{Registry: reg, Env: &core.RuleEnv{}, MaxSteps: 5}
	in := core.New(core.KindCode, "package x\nfunc f() {}").
		WithLang(core.LangGo).
		WithState(core.StateUncompiled)
	out, err := router.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.State != core.StateCompiled {
		t.Fatalf("expected Compiled, got %v", out.State)
	}
	if calls != 1 {
		t.Fatalf("expected 1 dispatch, got %d", calls)
	}
}

func TestRouter_HaltsOnTerminalKind(t *testing.T) {
	reg := NewRegistry()
	router := &Router{Registry: reg, Env: &core.RuleEnv{}}
	in := core.NewObstacle("t", "stuck", nil)
	out, err := router.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Kind != core.KindObstacle {
		t.Fatalf("got %v", out.Kind)
	}
}

func TestRouter_HaltsOnConfirmedState(t *testing.T) {
	reg := NewRegistry()
	router := &Router{Registry: reg, Env: &core.RuleEnv{}}
	in := core.New(core.KindCode, "code").WithState(core.StateConfirmed)
	out, err := router.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.State != core.StateConfirmed {
		t.Fatalf("got %v", out.State)
	}
}

func TestRouter_BoundedByMaxSteps(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&Rule{
		Name:   "loop",
		Actor:  ActorSystem,
		Input:  []core.Tag{{Kind: core.KindReason}},
		Output: core.SumType{{Kind: core.KindReason}},
		Handler: func(ctx context.Context, env *core.RuleEnv, inputs ...*core.TypedValue) (*core.TypedValue, error) {
			return inputs[0], nil
		},
	})
	router := &Router{Registry: reg, Env: &core.RuleEnv{}, MaxSteps: 3}
	in := core.New(core.KindReason, "stuck")
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
		Input:  []core.Tag{{Kind: core.KindCode}},
		Output: core.SumType{{Kind: core.KindCode, State: core.StateCompiled}},
		Handler: func(ctx context.Context, env *core.RuleEnv, inputs ...*core.TypedValue) (*core.TypedValue, error) {
			return inputs[0].WithState(core.StateCompiled), nil
		},
	})
	_ = reg.Register(&Rule{
		Name:   "narrow",
		Actor:  ActorCompiler,
		Input:  []core.Tag{{Kind: core.KindCode, State: core.StateUncompiled, Lang: core.LangGo}},
		Output: core.SumType{{Kind: core.KindCode, State: core.StateCompiled}},
		Handler: func(ctx context.Context, env *core.RuleEnv, inputs ...*core.TypedValue) (*core.TypedValue, error) {
			return inputs[0].WithState(core.StateCompiled), nil
		},
	})
	rule, ok := reg.Match(core.Tag{Kind: core.KindCode, State: core.StateUncompiled, Lang: core.LangGo})
	if !ok || rule.Name != "narrow" {
		t.Fatalf("specific rule should win; got %+v ok=%v", rule, ok)
	}
}
