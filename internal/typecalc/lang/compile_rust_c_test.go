package lang

import (
	"context"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// P1.2.1: the two newly-wired languages from the doc's six-language
// mapping — Rust (rustc --emit=metadata) and C (gcc -fsyntax-only).
// When the toolchain is absent the invoker fail-opens (Compiled) like
// TS/JS/Python, so these tests SKIP rather than assert fail-open — a
// skip with the toolchain noted is honest; a green that only proved
// fail-open is not.

func compileLang(t *testing.T, code string, lang core.Lang) *core.TypedValue {
	t.Helper()
	src := core.New(core.KindCode, code).WithLang(lang).WithState(core.StateUncompiled)
	out, err := CompileLanguageInvoker(context.Background(), &core.RuleEnv{}, src)
	if err != nil {
		t.Fatalf("invoker error: %v", err)
	}
	return out
}

func TestCompile_Rust_Pass_SkipIfNoRustc(t *testing.T) {
	if !commandExists("rustc") {
		t.Skip("rustc not on PATH — toolchain absent, skipping (fail-open path untested)")
	}
	out := compileLang(t, "pub fn add(a: i32, b: i32) -> i32 { a + b }\n", core.LangRust)
	if out.State != core.StateCompiled {
		t.Fatalf("valid Rust must compile, got %s / %s", out.Kind, out.State)
	}
}

func TestCompile_Rust_Fail_SkipIfNoRustc(t *testing.T) {
	if !commandExists("rustc") {
		t.Skip("rustc not on PATH — toolchain absent, skipping")
	}
	out := compileLang(t, "pub fn add( this is not valid rust {{{\n", core.LangRust)
	if out.Kind != core.KindCompileError {
		t.Fatalf("invalid Rust must yield CompileError, got %s", out.Kind)
	}
	if strings.TrimSpace(out.Payload) == "" {
		t.Error("CompileError must carry the verbatim compiler output (errors[])")
	}
}

func TestCompile_C_Pass_SkipIfNoGcc(t *testing.T) {
	if !commandExists("gcc") {
		t.Skip("gcc not on PATH — toolchain absent, skipping (fail-open path untested)")
	}
	out := compileLang(t, "int add(int a, int b){ return a + b; }\n", core.LangC)
	if out.State != core.StateCompiled {
		t.Fatalf("valid C must compile, got %s / %s", out.Kind, out.State)
	}
}

func TestCompile_C_Fail_SkipIfNoGcc(t *testing.T) {
	if !commandExists("gcc") {
		t.Skip("gcc not on PATH — toolchain absent, skipping")
	}
	out := compileLang(t, "int add(int a, { not valid c\n", core.LangC)
	if out.Kind != core.KindCompileError {
		t.Fatalf("invalid C must yield CompileError, got %s", out.Kind)
	}
	if strings.TrimSpace(out.Payload) == "" {
		t.Error("CompileError must carry the verbatim compiler output (errors[])")
	}
}

// The doc's six languages must all be recognised by LangFromExt
// (regression: C was the missing one before P1.2.1).
func TestCompile_SixLanguageExtensions(t *testing.T) {
	want := map[string]core.Lang{
		"go": core.LangGo, "ts": core.LangTypeScript, "js": core.LangJavaScript,
		"py": core.LangPython, "rs": core.LangRust, "c": core.LangC,
	}
	for ext, lang := range want {
		if got := core.LangFromExt(ext); got != lang {
			t.Errorf("LangFromExt(%q) = %q, want %q", ext, got, lang)
		}
	}
}
