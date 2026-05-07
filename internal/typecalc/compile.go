package typecalc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// defaultMaxRetries caps the compile (§7.1) and test (§7.2) loops. The
// design doc specifies "maximum retry count N" — we pick 5 by default.
// Override via RuleEnv.MaxRetries.
const defaultMaxRetries = 5

// CompileLanguageInvoker is the default CompileInvoker used in production.
// It looks at the language tag and dispatches to a per-language compiler:
//
//	Go         → go vet / go build (in-place)
//	TypeScript → npx tsc --noEmit
//	JavaScript → node --check
//	Python     → python -m py_compile
//
// For unrecognized languages it returns the input unchanged with state
// upgraded to Compiled — the assumption being "we can't check this
// statically; trust that the runtime will catch issues".
//
// Tools must be available on PATH; if a tool is missing we fail open
// (return Compiled) rather than blocking the whole agent on toolchain
// availability — the higher-level test loop will still catch bugs.
func CompileLanguageInvoker(ctx context.Context, env *RuleEnv, src *TypedValue) (*TypedValue, error) {
	if src == nil || src.Kind != KindCode {
		return nil, fmt.Errorf("CompileLanguageInvoker: expected Code, got %v", src)
	}
	if src.State != StateUncompiled {
		// Only Uncompiled<Code> may be compiled; other states are pass-through.
		return src, nil
	}

	switch src.Lang {
	case LangGo:
		return runGoCompile(ctx, env, src)
	case LangTypeScript:
		return runTSCompile(ctx, env, src)
	case LangJavaScript:
		return runJSCompile(ctx, env, src)
	case LangPython:
		return runPythonCompile(ctx, env, src)
	}
	// Unknown language — accept the format check from format.go is
	// already-applied by the router, and upgrade to Compiled.
	return src.WithState(StateCompiled), nil
}

func runGoCompile(ctx context.Context, env *RuleEnv, src *TypedValue) (*TypedValue, error) {
	tmp, cleanup, err := writeTempFile(env, "kcpos-go-*.go", src.Payload)
	if err != nil {
		return NewCompileError(taskOf(src), "internal", err.Error()), nil
	}
	defer cleanup()
	out, err := runCmd(ctx, env.WorkDir, "go", "vet", tmp)
	if err != nil {
		return NewCompileError(taskOf(src), "go vet", string(out)+"\n"+err.Error()), nil
	}
	return src.WithState(StateCompiled), nil
}

func runTSCompile(ctx context.Context, env *RuleEnv, src *TypedValue) (*TypedValue, error) {
	if !commandExists("npx") && !commandExists("tsc") {
		return src.WithState(StateCompiled), nil
	}
	tmp, cleanup, err := writeTempFile(env, "kcpos-ts-*.ts", src.Payload)
	if err != nil {
		return NewCompileError(taskOf(src), "internal", err.Error()), nil
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
		return NewCompileError(taskOf(src), "tsc", string(out)+"\n"+err.Error()), nil
	}
	return src.WithState(StateCompiled), nil
}

func runJSCompile(ctx context.Context, env *RuleEnv, src *TypedValue) (*TypedValue, error) {
	if !commandExists("node") {
		return src.WithState(StateCompiled), nil
	}
	tmp, cleanup, err := writeTempFile(env, "kcpos-js-*.js", src.Payload)
	if err != nil {
		return NewCompileError(taskOf(src), "internal", err.Error()), nil
	}
	defer cleanup()
	out, err := runCmd(ctx, env.WorkDir, "node", "--check", tmp)
	if err != nil {
		return NewCompileError(taskOf(src), "node --check", string(out)+"\n"+err.Error()), nil
	}
	return src.WithState(StateCompiled), nil
}

func runPythonCompile(ctx context.Context, env *RuleEnv, src *TypedValue) (*TypedValue, error) {
	pyExe := "python3"
	if !commandExists(pyExe) {
		pyExe = "python"
		if !commandExists(pyExe) {
			return src.WithState(StateCompiled), nil
		}
	}
	tmp, cleanup, err := writeTempFile(env, "kcpos-py-*.py", src.Payload)
	if err != nil {
		return NewCompileError(taskOf(src), "internal", err.Error()), nil
	}
	defer cleanup()
	out, err := runCmd(ctx, env.WorkDir, pyExe, "-m", "py_compile", tmp)
	if err != nil {
		return NewCompileError(taskOf(src), "py_compile", string(out)+"\n"+err.Error()), nil
	}
	return src.WithState(StateCompiled), nil
}

// CompileLoop runs the rule chain from §7.1: generate_code → compile →
// (CompileError → compiler_in_the_loop → retry_compile → compile → ...) →
// Compiled<Code>.
//
// Inputs:
//   - req: a Request<Task, ...> whose History accumulates errors
//   - regen: callback to ask the LLM for a new Uncompiled<Code> given the
//     enriched request. The LLM should return Uncompiled<Lang<L, Code>>.
//
// On success returns Compiled<Lang<L, Code>>. On exhausting retries
// returns Obstacle<Task, "compile retried N times"> per the give-up rule.
func CompileLoop(ctx context.Context, env *RuleEnv, req *TypedValue,
	regen func(ctx context.Context, env *RuleEnv, req *TypedValue) (*TypedValue, error),
) (*TypedValue, error) {
	if req == nil || req.Kind != KindRequest {
		return nil, fmt.Errorf("CompileLoop: input must be Request, got %v", req)
	}
	maxRetries := env.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultMaxRetries
	}
	current := req
	for attempt := 0; attempt < maxRetries; attempt++ {
		uncompiled, err := regen(ctx, env, current)
		if err != nil {
			return nil, fmt.Errorf("attempt %d generate: %w", attempt, err)
		}
		if uncompiled.Kind != KindCode || uncompiled.State != StateUncompiled {
			return formatErr("CompileLoop: regen must return Uncompiled<Code>, got %s",
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
		if out.State == StateCompiled && out.Kind == KindCode {
			return out, nil
		}
		if out.Kind != KindCompileError {
			return formatErr("CompileLoop: compiler returned unexpected %s", out.Tag()), nil
		}
		ce, _ := DecodeCompileError(out)
		current, err = EnrichRequest(current, "compile_error", ce)
		if err != nil {
			return nil, fmt.Errorf("enrich attempt %d: %w", attempt, err)
		}
	}
	env0, _ := DecodeRequest(current)
	var task string
	var trail []RequestEntry
	if env0 != nil {
		task = env0.Task
		trail = env0.History
	}
	reason := fmt.Sprintf("compile retried %d times without success", maxRetries)
	return NewObstacle(task, reason, trail), nil
}

// taskOf attempts to extract the task description from an Uncompiled<Code>
// typed value for inclusion in error envelopes. It looks at the Channel
// (session id) and any "task" entry in Context. If neither is present,
// returns the empty string.
func taskOf(tv *TypedValue) string {
	if tv == nil {
		return ""
	}
	if raw, ok := tv.Context["task"]; ok {
		s := strings.Trim(string(raw), `"`)
		return s
	}
	if tv.Channel != "" {
		return "session " + tv.Channel
	}
	return ""
}

func writeTempFile(env *RuleEnv, pattern, content string) (string, func(), error) {
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
