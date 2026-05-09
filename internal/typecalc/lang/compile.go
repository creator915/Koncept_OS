// Package lang implements the per-language toolchain layer of the type
// calculator: real compilers (compile.go), real test runners (test.go),
// real format/syntax checkers (format.go), and the scratch-dir helper
// they all share (fs.go).
//
// Sits one level below typecalc/rule (which wires this layer into rule
// handlers) and depends only on typecalc core types. The compile/test
// loops here implement §7.1 / §7.2 of the design doc.
package lang

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/creator915/Koncept_OS/internal/typecalc"
)

// DefaultMaxRetries caps the compile (§7.1) and test (§7.2) loops. The
// design doc specifies "maximum retry count N" — we pick 5 by default.
// Override via RuleEnv.MaxRetries.
const DefaultMaxRetries = 5

// CompileLanguageInvoker is the default CompileInvoker used in production.
// It looks at the language tag and dispatches to a per-language compiler:
//
//	Go         → go vet
//	TypeScript → tsc / npx tsc --noEmit
//	JavaScript → node --check
//	Python     → python -m py_compile
//
// For unrecognized languages it returns the input unchanged with state
// upgraded to Compiled. Tools must be on PATH; if a tool is missing we
// fail open (return Compiled) rather than blocking the whole agent on
// toolchain availability — the higher-level test loop will still catch bugs.
func CompileLanguageInvoker(ctx context.Context, env *typecalc.RuleEnv, src *typecalc.TypedValue) (*typecalc.TypedValue, error) {
	if src == nil || src.Kind != typecalc.KindCode {
		return nil, fmt.Errorf("CompileLanguageInvoker: expected Code, got %v", src)
	}
	if src.State != typecalc.StateUncompiled {
		return src, nil
	}

	switch src.Lang {
	case typecalc.LangGo:
		return runGoCompile(ctx, env, src)
	case typecalc.LangTypeScript:
		return runTSCompile(ctx, env, src)
	case typecalc.LangJavaScript:
		return runJSCompile(ctx, env, src)
	case typecalc.LangHTML:
		// 2026-05-09 v8.2 — companion to lang/test.go HTML routing.
		// Extract every inline <script> body and run node --check on
		// the concatenation. This catches obvious syntax errors in the
		// deliverable's actual code (the same content the harness will
		// load + execute at test time) without a separate K/impl/*.js
		// shadow. If extraction yields no scripts, we still upgrade
		// to Compiled — an HTML file with no JS is degenerate but not
		// a compile failure.
		return runHTMLCompile(ctx, env, src)
	case typecalc.LangPython:
		return runPythonCompile(ctx, env, src)
	}
	// CRITICAL (D1): no in-tree compile invoker for this language.
	// Previously returned Compiled — that lied. Now Insufficient.
	return typecalc.NewInsufficient(fmt.Sprintf(
		"no in-tree compile invoker for language %q — kcpos cannot mechanically syntax-check this code. To proceed, restructure into a supported language or record an explicit waiver via typecalc_waive.",
		src.Lang)), nil
}

func runGoCompile(ctx context.Context, env *typecalc.RuleEnv, src *typecalc.TypedValue) (*typecalc.TypedValue, error) {
	tmp, cleanup, err := writeTempFile(env, "kcpos-go-*.go", src.Payload)
	if err != nil {
		return typecalc.NewCompileError(taskOf(src), "internal", err.Error()), nil
	}
	defer cleanup()
	out, err := runCmd(ctx, env.WorkDir, "go", "vet", tmp)
	if err != nil {
		return typecalc.NewCompileError(taskOf(src), "go vet", string(out)+"\n"+err.Error()), nil
	}
	return src.WithState(typecalc.StateCompiled), nil
}

func runTSCompile(ctx context.Context, env *typecalc.RuleEnv, src *typecalc.TypedValue) (*typecalc.TypedValue, error) {
	if !commandExists("npx") && !commandExists("tsc") {
		return src.WithState(typecalc.StateCompiled), nil
	}
	tmp, cleanup, err := writeTempFile(env, "kcpos-ts-*.ts", src.Payload)
	if err != nil {
		return typecalc.NewCompileError(taskOf(src), "internal", err.Error()), nil
	}
	defer cleanup()
	args := []string{"--noEmit", "--target", "es2020", "--module", "esnext", "--moduleResolution", "node", tmp}
	var out []byte
	if commandExists("tsc") {
		out, err = runCmd(ctx, env.WorkDir, "tsc", args...)
	} else {
		out, err = runCmd(ctx, env.WorkDir, "npx", append([]string{"tsc"}, args...)...)
	}
	if err != nil {
		return typecalc.NewCompileError(taskOf(src), "tsc", string(out)+"\n"+err.Error()), nil
	}
	return src.WithState(typecalc.StateCompiled), nil
}

func runJSCompile(ctx context.Context, env *typecalc.RuleEnv, src *typecalc.TypedValue) (*typecalc.TypedValue, error) {
	if !commandExists("node") {
		return src.WithState(typecalc.StateCompiled), nil
	}
	tmp, cleanup, err := writeTempFile(env, "kcpos-js-*.js", src.Payload)
	if err != nil {
		return typecalc.NewCompileError(taskOf(src), "internal", err.Error()), nil
	}
	defer cleanup()
	out, err := runCmd(ctx, env.WorkDir, "node", "--check", tmp)
	if err != nil {
		return typecalc.NewCompileError(taskOf(src), "node --check", string(out)+"\n"+err.Error()), nil
	}
	return src.WithState(typecalc.StateCompiled), nil
}

// extractHTMLScripts pulls every inline <script>...</script> body
// (it ignores src="..." externals — kcpos owns the file in-tree, so
// inline is the only relevant case). The same regex the harness's
// loadImpl uses, kept in sync.
var htmlScriptRe = regexp.MustCompile(`(?is)<script[^>]*>(.*?)</script>`)

func runHTMLCompile(ctx context.Context, env *typecalc.RuleEnv, src *typecalc.TypedValue) (*typecalc.TypedValue, error) {
	matches := htmlScriptRe.FindAllStringSubmatch(src.Payload, -1)
	var combined strings.Builder
	for _, m := range matches {
		if len(m) >= 2 {
			combined.WriteString(m[1])
			combined.WriteString("\n")
		}
	}
	js := combined.String()
	if strings.TrimSpace(js) == "" {
		// HTML with no inline JS — nothing to syntax-check, accept.
		return src.WithState(typecalc.StateCompiled), nil
	}
	if !commandExists("node") {
		return src.WithState(typecalc.StateCompiled), nil
	}
	tmp, cleanup, err := writeTempFile(env, "kcpos-html-*.js", js)
	if err != nil {
		return typecalc.NewCompileError(taskOf(src), "internal", err.Error()), nil
	}
	defer cleanup()
	out, err := runCmd(ctx, env.WorkDir, "node", "--check", tmp)
	if err != nil {
		return typecalc.NewCompileError(taskOf(src), "node --check (extracted from <script>)", string(out)+"\n"+err.Error()), nil
	}
	return src.WithState(typecalc.StateCompiled), nil
}

func runPythonCompile(ctx context.Context, env *typecalc.RuleEnv, src *typecalc.TypedValue) (*typecalc.TypedValue, error) {
	pyExe := "python3"
	if !commandExists(pyExe) {
		pyExe = "python"
		if !commandExists(pyExe) {
			return src.WithState(typecalc.StateCompiled), nil
		}
	}
	tmp, cleanup, err := writeTempFile(env, "kcpos-py-*.py", src.Payload)
	if err != nil {
		return typecalc.NewCompileError(taskOf(src), "internal", err.Error()), nil
	}
	defer cleanup()
	out, err := runCmd(ctx, env.WorkDir, pyExe, "-m", "py_compile", tmp)
	if err != nil {
		return typecalc.NewCompileError(taskOf(src), "py_compile", string(out)+"\n"+err.Error()), nil
	}
	return src.WithState(typecalc.StateCompiled), nil
}

// CompileLoop runs §7.1: generate_code → compile → (CompileError →
// compiler_in_the_loop → retry_compile → compile → ...) → Compiled<Code>.
// On exhausting retries returns Obstacle.
func CompileLoop(ctx context.Context, env *typecalc.RuleEnv, req *typecalc.TypedValue,
	regen func(ctx context.Context, env *typecalc.RuleEnv, req *typecalc.TypedValue) (*typecalc.TypedValue, error),
) (*typecalc.TypedValue, error) {
	if req == nil || req.Kind != typecalc.KindRequest {
		return nil, fmt.Errorf("CompileLoop: input must be Request, got %v", req)
	}
	maxRetries := env.MaxRetries
	if maxRetries <= 0 {
		maxRetries = DefaultMaxRetries
	}
	current := req
	for attempt := 0; attempt < maxRetries; attempt++ {
		uncompiled, err := regen(ctx, env, current)
		if err != nil {
			return nil, fmt.Errorf("attempt %d generate: %w", attempt, err)
		}
		if uncompiled.Kind != typecalc.KindCode || uncompiled.State != typecalc.StateUncompiled {
			return typecalc.FormatErr("CompileLoop: regen must return Uncompiled<Code>, got %s",
				uncompiled.Tag()), nil
		}
		invoker := env.CompileInvoker
		if invoker == nil {
			invoker = CompileLanguageInvoker
		}
		out, err := invoker(ctx, env, uncompiled)
		if err != nil {
			return nil, fmt.Errorf("attempt %d compile: %w", attempt, err)
		}
		if out.State == typecalc.StateCompiled && out.Kind == typecalc.KindCode {
			return out, nil
		}
		if out.Kind != typecalc.KindCompileError {
			return typecalc.FormatErr("CompileLoop: compiler returned unexpected %s", out.Tag()), nil
		}
		ce, _ := typecalc.DecodeCompileError(out)
		current, err = typecalc.EnrichRequest(current, "compile_error", ce)
		if err != nil {
			return nil, fmt.Errorf("enrich attempt %d: %w", attempt, err)
		}
	}
	env0, _ := typecalc.DecodeRequest(current)
	var task string
	var trail []typecalc.RequestEntry
	if env0 != nil {
		task = env0.Task
		trail = env0.History
	}
	reason := fmt.Sprintf("compile retried %d times without success", maxRetries)
	return typecalc.NewObstacle(task, reason, trail), nil
}

// taskOf extracts the task description from a typed value's Context.
func taskOf(tv *typecalc.TypedValue) string {
	if tv == nil {
		return ""
	}
	if raw, ok := tv.Context["task"]; ok {
		return strings.Trim(string(raw), `"`)
	}
	if tv.Channel != "" {
		return "session " + tv.Channel
	}
	return ""
}

func writeTempFile(env *typecalc.RuleEnv, pattern, content string) (string, func(), error) {
	dir := os.TempDir()
	if env != nil && env.WorkDir != "" {
		dir = filepath.Join(env.WorkDir, ".kcpos", "typecalc-tmp")
		_ = os.MkdirAll(dir, 0o755)
	}
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", func() {}, err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", func() {}, err
	}
	f.Close()
	cleanup := func() { os.Remove(f.Name()) }
	return f.Name(), cleanup, nil
}

func runCmd(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd.CombinedOutput()
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
