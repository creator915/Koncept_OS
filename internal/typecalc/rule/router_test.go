package rule

import (
	"context"
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc"
)

func TestRouter_DispatchesByTag(t *testing.T) {
	reg := NewRegistry()
	calls := 0
	err := reg.Register(&Rule{
		Name:   "promote_uncompiled",
		Actor:  ActorSystem,
		Input:  []typecalc.Tag{{Kind: typecalc.KindCode, State: typecalc.StateUncompiled}},
		Output: typecalc.SumType{{Kind: typecalc.KindCode, State: typecalc.StateCompiled}},
		Handler: func(ctx context.Context, env *typecalc.RuleEnv, inputs ...*typecalc.TypedValue) (*typecalc.TypedValue, error) {
			calls++
			return inputs[0].WithState(typecalc.StateCompiled), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	router := &Router{Registry: reg, Env: &typecalc.RuleEnv{}, MaxSteps: 5}
	in := typecalc.New(typecalc.KindCode, "package x\nfunc f() {}").
		WithLang(typecalc.LangGo).
		WithState(typecalc.StateUncompiled)
	out, err := router.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.State != typecalc.StateCompiled {
		t.Fatalf("expected Compiled, got %v", out.State)
	}
	if calls != 1 {
		t.Fatalf("expected 1 dispatch, got %d", calls)
	}
}

func TestRouter_HaltsOnTerminalKind(t *testing.T) {
	reg := NewRegistry()
	router := &Router{Registry: reg, Env: &typecalc.RuleEnv{}}
	in := typecalc.NewObstacle("t", "stuck", nil)
	out, err := router.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Kind != typecalc.KindObstacle {
		t.Fatalf("got %v", out.Kind)
	}
}

func TestRouter_HaltsOnConfirmedState(t *testing.T) {
	reg := NewRegistry()
	router := &Router{Registry: reg, Env: &typecalc.RuleEnv{}}
	in := typecalc.New(typecalc.KindCode, "code").WithState(typecalc.StateConfirmed)
	out, err := router.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.State != typecalc.StateConfirmed {
		t.Fatalf("got %v", out.State)
	}
}

func TestRouter_BoundedByMaxSteps(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&Rule{
		Name:   "loop",
		Actor:  ActorSystem,
		Input:  []typecalc.Tag{{Kind: typecalc.KindReason}},
		Output: typecalc.SumType{{Kind: typecalc.KindReason}},
		Handler: func(ctx context.Context, env *typecalc.RuleEnv, inputs ...*typecalc.TypedValue) (*typecalc.TypedValue, error) {
			return inputs[0], nil
		},
	})
	router := &Router{Registry: reg, Env: &typecalc.RuleEnv{}, MaxSteps: 3}
	in := typecalc.New(typecalc.KindReason, "stuck")
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
		Input:  []typecalc.Tag{{Kind: typecalc.KindCode}},
		Output: typecalc.SumType{{Kind: typecalc.KindCode, State: typecalc.StateCompiled}},
		Handler: func(ctx context.Context, env *typecalc.RuleEnv, inputs ...*typecalc.TypedValue) (*typecalc.TypedValue, error) {
			return inputs[0].WithState(typecalc.StateCompiled), nil
		},
	})
	_ = reg.Register(&Rule{
		Name:   "narrow",
		Actor:  ActorCompiler,
		Input:  []typecalc.Tag{{Kind: typecalc.KindCode, State: typecalc.StateUncompiled, Lang: typecalc.LangGo}},
		Output: typecalc.SumType{{Kind: typecalc.KindCode, State: typecalc.StateCompiled}},
		Handler: func(ctx context.Context, env *typecalc.RuleEnv, inputs ...*typecalc.TypedValue) (*typecalc.TypedValue, error) {
			return inputs[0].WithState(typecalc.StateCompiled), nil
		},
	})
	rule, ok := reg.Match(typecalc.Tag{Kind: typecalc.KindCode, State: typecalc.StateUncompiled, Lang: typecalc.LangGo})
	if !ok || rule.Name != "narrow" {
		t.Fatalf("specific rule should win; got %+v ok=%v", rule, ok)
	}
}
