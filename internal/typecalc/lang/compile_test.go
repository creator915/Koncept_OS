package lang

import (
	"context"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc"
)

func TestCompileLanguageInvoker_Go_Pass(t *testing.T) {
	src := typecalc.New(typecalc.KindCode, "package x\nfunc Foo() int { return 1 }").
		WithLang(typecalc.LangGo).WithState(typecalc.StateUncompiled)
	out, err := CompileLanguageInvoker(context.Background(), &typecalc.RuleEnv{}, src)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != typecalc.StateCompiled {
		t.Fatalf("expected Compiled, got %v: %s", out.State, out.Payload)
	}
}

func TestCompileLanguageInvoker_Go_Fail(t *testing.T) {
	src := typecalc.New(typecalc.KindCode, "package x\nfunc Foo() int { return \"not int\" }").
		WithLang(typecalc.LangGo).WithState(typecalc.StateUncompiled)
	out, err := CompileLanguageInvoker(context.Background(), &typecalc.RuleEnv{}, src)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != typecalc.KindCompileError {
		t.Fatalf("expected CompileError, got %s", out.Tag())
	}
	d, _ := typecalc.DecodeCompileError(out)
	if !strings.Contains(d.ErrorLog, "go vet") && !strings.Contains(d.ErrorLog, "cannot use") {
		t.Logf("error log: %s", d.ErrorLog)
	}
}

func TestCompileLoop_GivesUpToObstacle(t *testing.T) {
	env := &typecalc.RuleEnv{
		MaxRetries: 2,
		CompileInvoker: func(ctx context.Context, env *typecalc.RuleEnv, src *typecalc.TypedValue) (*typecalc.TypedValue, error) {
			return typecalc.NewCompileError("t", "X", "always fails"), nil
		},
	}
	out, err := CompileLoop(context.Background(), env, typecalc.NewRequest("t"),
		func(ctx context.Context, env *typecalc.RuleEnv, req *typecalc.TypedValue) (*typecalc.TypedValue, error) {
			return typecalc.New(typecalc.KindCode, "x").WithState(typecalc.StateUncompiled).WithLang(typecalc.LangGo), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != typecalc.KindObstacle {
		t.Fatalf("expected Obstacle, got %s", out.Tag())
	}
	d, _ := typecalc.DecodeObstacle(out)
	if !strings.Contains(d.Reason, "compile retried 2 times") {
		t.Fatalf("reason: %s", d.Reason)
	}
}

func TestCompileLoop_SucceedsOnSecondTry(t *testing.T) {
	calls := 0
	env := &typecalc.RuleEnv{
		MaxRetries: 5,
		CompileInvoker: func(ctx context.Context, env *typecalc.RuleEnv, src *typecalc.TypedValue) (*typecalc.TypedValue, error) {
			calls++
			if calls < 2 {
				return typecalc.NewCompileError("t", "X", "first try fails"), nil
			}
			return src.WithState(typecalc.StateCompiled), nil
		},
	}
	out, err := CompileLoop(context.Background(), env, typecalc.NewRequest("t"),
		func(ctx context.Context, env *typecalc.RuleEnv, req *typecalc.TypedValue) (*typecalc.TypedValue, error) {
			return typecalc.New(typecalc.KindCode, "x").WithState(typecalc.StateUncompiled).WithLang(typecalc.LangGo), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if out.State != typecalc.StateCompiled {
		t.Fatalf("expected Compiled, got %s", out.Tag())
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}
