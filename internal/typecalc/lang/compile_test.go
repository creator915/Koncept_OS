package lang

import (
	"context"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

func TestCompileLanguageInvoker_Go_Pass(t *testing.T) {
	src := core.New(core.KindCode, "package x\nfunc Foo() int { return 1 }").
		WithLang(core.LangGo).WithState(core.StateUncompiled)
	out, err := CompileLanguageInvoker(context.Background(), &core.RuleEnv{}, src)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != core.StateCompiled {
		t.Fatalf("expected Compiled, got %v: %s", out.State, out.Payload)
	}
}

func TestCompileLanguageInvoker_Go_Fail(t *testing.T) {
	src := core.New(core.KindCode, "package x\nfunc Foo() int { return \"not int\" }").
		WithLang(core.LangGo).WithState(core.StateUncompiled)
	out, err := CompileLanguageInvoker(context.Background(), &core.RuleEnv{}, src)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != core.KindCompileError {
		t.Fatalf("expected CompileError, got %s", out.Tag())
	}
	d, _ := core.DecodeCompileError(out)
	if !strings.Contains(d.ErrorLog, "go vet") && !strings.Contains(d.ErrorLog, "cannot use") {
		t.Logf("error log: %s", d.ErrorLog)
	}
}

func TestCompileLoop_GivesUpToObstacle(t *testing.T) {
	env := &core.RuleEnv{
		MaxRetries: 2,
		CompileInvoker: func(ctx context.Context, env *core.RuleEnv, src *core.TypedValue) (*core.TypedValue, error) {
			return core.NewCompileError("t", "X", "always fails"), nil
		},
	}
	out, err := CompileLoop(context.Background(), env, core.NewRequest("t"),
		func(ctx context.Context, env *core.RuleEnv, req *core.TypedValue) (*core.TypedValue, error) {
			return core.New(core.KindCode, "x").WithState(core.StateUncompiled).WithLang(core.LangGo), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != core.KindObstacle {
		t.Fatalf("expected Obstacle, got %s", out.Tag())
	}
	d, _ := core.DecodeObstacle(out)
	if !strings.Contains(d.Reason, "compile retried 2 times") {
		t.Fatalf("reason: %s", d.Reason)
	}
}

func TestCompileLoop_SucceedsOnSecondTry(t *testing.T) {
	calls := 0
	env := &core.RuleEnv{
		MaxRetries: 5,
		CompileInvoker: func(ctx context.Context, env *core.RuleEnv, src *core.TypedValue) (*core.TypedValue, error) {
			calls++
			if calls < 2 {
				return core.NewCompileError("t", "X", "first try fails"), nil
			}
			return src.WithState(core.StateCompiled), nil
		},
	}
	out, err := CompileLoop(context.Background(), env, core.NewRequest("t"),
		func(ctx context.Context, env *core.RuleEnv, req *core.TypedValue) (*core.TypedValue, error) {
			return core.New(core.KindCode, "x").WithState(core.StateUncompiled).WithLang(core.LangGo), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if out.State != core.StateCompiled {
		t.Fatalf("expected Compiled, got %s", out.Tag())
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}
