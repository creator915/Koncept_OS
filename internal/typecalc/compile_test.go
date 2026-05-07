package typecalc

import (
	"context"
	"strings"
	"testing"
)

func TestCompileLanguageInvoker_Go_Pass(t *testing.T) {
	src := New(KindCode, "package x\nfunc Foo() int { return 1 }").
		WithLang(LangGo).WithState(StateUncompiled)
	out, err := CompileLanguageInvoker(context.Background(), &RuleEnv{}, src)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != StateCompiled {
		t.Fatalf("expected Compiled, got %v: %s", out.State, out.Payload)
	}
}

func TestCompileLanguageInvoker_Go_Fail(t *testing.T) {
	src := New(KindCode, "package x\nfunc Foo() int { return \"not int\" }").
		WithLang(LangGo).WithState(StateUncompiled)
	out, err := CompileLanguageInvoker(context.Background(), &RuleEnv{}, src)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != KindCompileError {
		t.Fatalf("expected CompileError, got %s", out.Tag())
	}
	d, _ := DecodeCompileError(out)
	if !strings.Contains(d.ErrorLog, "go vet") && !strings.Contains(d.ErrorLog, "cannot use") {
		t.Logf("error log: %s", d.ErrorLog)
	}
}

func TestCompileLoop_GivesUpToObstacle(t *testing.T) {
	env := &RuleEnv{
		MaxRetries: 2,
		CompileInvoker: func(ctx context.Context, env *RuleEnv, src *TypedValue) (*TypedValue, error) {
			return NewCompileError("t", "X", "always fails"), nil
		},
	}
	out, err := CompileLoop(context.Background(), env, NewRequest("t"),
		func(ctx context.Context, env *RuleEnv, req *TypedValue) (*TypedValue, error) {
			return New(KindCode, "x").WithState(StateUncompiled).WithLang(LangGo), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != KindObstacle {
		t.Fatalf("expected Obstacle, got %s", out.Tag())
	}
	d, _ := DecodeObstacle(out)
	if !strings.Contains(d.Reason, "compile retried 2 times") {
		t.Fatalf("reason: %s", d.Reason)
	}
}

func TestCompileLoop_SucceedsOnSecondTry(t *testing.T) {
	calls := 0
	env := &RuleEnv{
		MaxRetries: 5,
		CompileInvoker: func(ctx context.Context, env *RuleEnv, src *TypedValue) (*TypedValue, error) {
			calls++
			if calls < 2 {
				return NewCompileError("t", "X", "first try fails"), nil
			}
			return src.WithState(StateCompiled), nil
		},
	}
	out, err := CompileLoop(context.Background(), env, NewRequest("t"),
		func(ctx context.Context, env *RuleEnv, req *TypedValue) (*TypedValue, error) {
			return New(KindCode, "x").WithState(StateUncompiled).WithLang(LangGo), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if out.State != StateCompiled {
		t.Fatalf("expected Compiled, got %s", out.Tag())
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}
