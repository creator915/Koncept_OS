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

	"github.com/creator915/Koncept_OS/internal/runtime/preflight"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// preflightToolFor maps the legacy `commandExists(name)` string to a
// preflight.Tool id. Only the tools that lang.go actively probes are
// listed; anything else falls back to a direct LookPath in commandExists.
var preflightToolFor = map[string]preflight.Tool{
	"node":    preflight.Node,
	"npm":     preflight.NPM,
	"npx":     preflight.NPX,
	"tsc":     preflight.TSC,
	"go":      preflight.Go,
	"python3": preflight.Python3,
}

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
//	Rust       → rustc --emit=metadata
//	C          → gcc -fsyntax-only
//
// For unrecognized languages it returns the input unchanged with state
// upgraded to Compiled. Tools must be on PATH; if a tool is missing we
// fail open (return Compiled) rather than blocking the whole agent on
// toolchain availability — the higher-level test loop will still catch bugs.
func CompileLanguageInvoker(ctx context.Context, env *core.RuleEnv, src *core.TypedValue) (*core.TypedValue, error) {
	if src == nil || src.Kind != core.KindCode {
		return nil, fmt.Errorf("CompileLanguageInvoker: expected Code, got %v", src)
	}
	if src.State != core.StateUncompiled {
		return src, nil
	}

	switch src.Lang {
	case core.LangGo:
		return runGoCompile(ctx, env, src)
	case core.LangTypeScript:
		return runTSCompile(ctx, env, src)
	case core.LangJavaScript:
		return runJSCompile(ctx, env, src)
	case core.LangHTML:
		// 2026-05-09 v8.2 — companion to lang/test.go HTML routing.
		// Extract every inline <script> body and run node --check on
		// the concatenation. This catches obvious syntax errors in the
		// deliverable's actual code (the same content the harness will
		// load + execute at test time) without a separate K/impl/*.js
		// shadow. If extraction yields no scripts, we still upgrade
		// to Compiled — an HTML file with no JS is degenerate but not
		// a compile failure.
		return runHTMLCompile(ctx, env, src)
	case core.LangPython:
		return runPythonCompile(ctx, env, src)
	case core.LangRust:
		return runRustCompile(ctx, env, src)
	case core.LangC:
		return runCCompile(ctx, env, src)
	}
	// CRITICAL: no in-tree compile invoker for this language. Returns
	// Insufficient. v9.2 — the waiver escape is gone; the gate WILL
	// refuse to confirm. Resolve by restructuring into a supported
	// language or extending internal/typecalc/lang/ to add an invoker.
	return core.NewInsufficient(fmt.Sprintf(
		"no in-tree compile invoker for language %q — kcpos cannot mechanically syntax-check this code. v9.2 has no waiver escape; restructure into a supported language (Go/TypeScript/JavaScript/HTML/Python) or extend internal/typecalc/lang/ with a CompileLanguageInvoker for %q.",
		src.Lang, src.Lang)), nil
}

func runGoCompile(ctx context.Context, env *core.RuleEnv, src *core.TypedValue) (*core.TypedValue, error) {
	// 2026-05-14 v9.6 — Go must be checked at package granularity, not
	// per file. The pre-v9.6 single-file `go vet <tmp>` failed any object
	// whose impl referenced a type defined elsewhere in the same package
	// (the 2026-05-14 walk batch lost ~13 minutes to this exact case;
	// the agent ended up duplicating type decls into impl files to coerce
	// isolation, breaking the real multi-file build). Strategy: stage a
	// scratch package holding the impl + every sibling .go in its dir
	// (and K/defs/ as fallback), then run `go vet ./...`. Single-file
	// impls (no siblings) still work — the staging just copies one file.
	dir, cleanup, err := stageGoPackage(env, src.Payload, "code.go")
	if err != nil {
		return core.NewCompileError(taskOf(src), "internal", err.Error()), nil
	}
	defer cleanup()
	// `go vet ./...` requires a module; stamp one in scratch.
	if err := writeFile(dir, "go.mod", "module kcposscratch\n\ngo 1.21\n"); err != nil {
		cleanup()
		return core.NewCompileError(taskOf(src), "internal", err.Error()), nil
	}
	out, err := runCmd(ctx, dir, "go", "vet", "./...")
	if err != nil {
		return core.NewCompileError(taskOf(src), "go vet", string(out)+"\n"+err.Error()), nil
	}
	return src.WithState(core.StateCompiled), nil
}

func runTSCompile(ctx context.Context, env *core.RuleEnv, src *core.TypedValue) (*core.TypedValue, error) {
	if !commandExists("npx") && !commandExists("tsc") {
		return src.WithState(core.StateCompiled), nil
	}
	tmp, cleanup, err := writeTempFile(env, "kcpos-ts-*.ts", src.Payload)
	if err != nil {
		return core.NewCompileError(taskOf(src), "internal", err.Error()), nil
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
		return core.NewCompileError(taskOf(src), "tsc", string(out)+"\n"+err.Error()), nil
	}
	return src.WithState(core.StateCompiled), nil
}

func runJSCompile(ctx context.Context, env *core.RuleEnv, src *core.TypedValue) (*core.TypedValue, error) {
	if !commandExists("node") {
		return src.WithState(core.StateCompiled), nil
	}
	tmp, cleanup, err := writeTempFile(env, "kcpos-js-*.js", src.Payload)
	if err != nil {
		return core.NewCompileError(taskOf(src), "internal", err.Error()), nil
	}
	defer cleanup()
	out, err := runCmd(ctx, env.WorkDir, "node", "--check", tmp)
	if err != nil {
		return core.NewCompileError(taskOf(src), "node --check", string(out)+"\n"+err.Error()), nil
	}
	return src.WithState(core.StateCompiled), nil
}

// extractHTMLScripts pulls every inline <script>...</script> body
// (it ignores src="..." externals — kcpos owns the file in-tree, so
// inline is the only relevant case). The same regex the harness's
// loadImpl uses, kept in sync.
var htmlScriptRe = regexp.MustCompile(`(?is)<script[^>]*>(.*?)</script>`)

func runHTMLCompile(ctx context.Context, env *core.RuleEnv, src *core.TypedValue) (*core.TypedValue, error) {
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
		return src.WithState(core.StateCompiled), nil
	}
	if !commandExists("node") {
		return src.WithState(core.StateCompiled), nil
	}
	tmp, cleanup, err := writeTempFile(env, "kcpos-html-*.js", js)
	if err != nil {
		return core.NewCompileError(taskOf(src), "internal", err.Error()), nil
	}
	defer cleanup()
	out, err := runCmd(ctx, env.WorkDir, "node", "--check", tmp)
	if err != nil {
		return core.NewCompileError(taskOf(src), "node --check (extracted from <script>)", string(out)+"\n"+err.Error()), nil
	}
	return src.WithState(core.StateCompiled), nil
}

func runPythonCompile(ctx context.Context, env *core.RuleEnv, src *core.TypedValue) (*core.TypedValue, error) {
	pyExe := "python3"
	if !commandExists(pyExe) {
		pyExe = "python"
		if !commandExists(pyExe) {
			return src.WithState(core.StateCompiled), nil
		}
	}
	tmp, cleanup, err := writeTempFile(env, "kcpos-py-*.py", src.Payload)
	if err != nil {
		return core.NewCompileError(taskOf(src), "internal", err.Error()), nil
	}
	defer cleanup()
	out, err := runCmd(ctx, env.WorkDir, pyExe, "-m", "py_compile", tmp)
	if err != nil {
		return core.NewCompileError(taskOf(src), "py_compile", string(out)+"\n"+err.Error()), nil
	}
	return src.WithState(core.StateCompiled), nil
}

func runRustCompile(ctx context.Context, env *core.RuleEnv, src *core.TypedValue) (*core.TypedValue, error) {
	if !commandExists("rustc") {
		// Toolchain absent — fail open like TS/JS/Python (the test loop
		// still catches behavioural bugs).
		return src.WithState(core.StateCompiled), nil
	}
	tmp, cleanup, err := writeTempFile(env, "kcpos-rs-*.rs", src.Payload)
	if err != nil {
		return core.NewCompileError(taskOf(src), "internal", err.Error()), nil
	}
	defer cleanup()
	// --emit=metadata is the doc's syntax/type check (no codegen);
	// --crate-type=lib avoids requiring a main; artifacts to tmp dir.
	out, err := runCmd(ctx, env.WorkDir, "rustc", "--emit=metadata",
		"--crate-type=lib", tmp, "--out-dir", filepath.Dir(tmp))
	if err != nil {
		return core.NewCompileError(taskOf(src), "rustc --emit=metadata", string(out)+"\n"+err.Error()), nil
	}
	return src.WithState(core.StateCompiled), nil
}

func runCCompile(ctx context.Context, env *core.RuleEnv, src *core.TypedValue) (*core.TypedValue, error) {
	if !commandExists("gcc") {
		return src.WithState(core.StateCompiled), nil
	}
	tmp, cleanup, err := writeTempFile(env, "kcpos-c-*.c", src.Payload)
	if err != nil {
		return core.NewCompileError(taskOf(src), "internal", err.Error()), nil
	}
	defer cleanup()
	// -fsyntax-only: parse + typecheck, emit nothing (the doc's mapping).
	out, err := runCmd(ctx, env.WorkDir, "gcc", "-fsyntax-only", tmp)
	if err != nil {
		return core.NewCompileError(taskOf(src), "gcc -fsyntax-only", string(out)+"\n"+err.Error()), nil
	}
	return src.WithState(core.StateCompiled), nil
}

// CompileLoop runs §7.1: generate_code → compile → (CompileError →
// compiler_in_the_loop → retry_compile → compile → ...) → Compiled<Code>.
// On exhausting retries returns Obstacle.
func CompileLoop(ctx context.Context, env *core.RuleEnv, req *core.TypedValue,
	regen func(ctx context.Context, env *core.RuleEnv, req *core.TypedValue) (*core.TypedValue, error),
) (*core.TypedValue, error) {
	if req == nil || req.Kind != core.KindRequest {
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
		if uncompiled.Kind != core.KindCode || uncompiled.State != core.StateUncompiled {
			return core.FormatErr("CompileLoop: regen must return Uncompiled<Code>, got %s",
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
		if out.State == core.StateCompiled && out.Kind == core.KindCode {
			return out, nil
		}
		if out.Kind != core.KindCompileError {
			return core.FormatErr("CompileLoop: compiler returned unexpected %s", out.Tag()), nil
		}
		ce, _ := core.DecodeCompileError(out)
		current, err = core.EnrichRequest(current, "compile_error", ce)
		if err != nil {
			return nil, fmt.Errorf("enrich attempt %d: %w", attempt, err)
		}
	}
	env0, _ := core.DecodeRequest(current)
	var task string
	var trail []core.RequestEntry
	if env0 != nil {
		task = env0.Task
		trail = env0.History
	}
	reason := fmt.Sprintf("compile retried %d times without success", maxRetries)
	return core.NewObstacle(task, reason, trail), nil
}

// taskOf extracts the task description from a typed value's Context.
func taskOf(tv *core.TypedValue) string {
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

func writeTempFile(env *core.RuleEnv, pattern, content string) (string, func(), error) {
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

// commandExists is now a thin facade over preflight.Detect — it keeps
// the original boolean signature so call sites elsewhere in this file
// stay untouched. Routing through preflight ensures detection results
// are cached per-process and visible to `kcpos doctor`.
//
// Note: preflight.Detect returns a richer Result (path, version, probe
// audit trail). Callers that need any of those should query preflight
// directly. v9.0.6 verification-chain failures are partly traceable to
// silent no-ops when a tool was missing; new code paths (runtime_smoke)
// must NOT use this shim — they surface ErrUserDeclined / Hint via the
// agent tool result instead.
func commandExists(name string) bool {
	if pft, ok := preflightToolFor[name]; ok {
		return preflight.Detect(pft).Found
	}
	// Fall back to a direct LookPath probe for tools not in the
	// preflight registry (legacy callers, niche tools). Matches
	// the original v9.0 semantics.
	_, err := exec.LookPath(name)
	return err == nil
}
