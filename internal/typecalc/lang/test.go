package lang

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/creator915/Koncept_OS/internal/typecalc"
)

// TestReview is the verdict returned by the review_test_error rule (§3,
// "测试阶段"). The LLM is given the description, signature, and one
// failing test case (NOT the source code) and must classify the failure.
type TestReview string

const (
	TestReviewCorrect            TestReview = "TestCorrect"        // test is right, code has a bug
	TestReviewWrong              TestReview = "TestWrong"          // test is wrong, code is fine
	TestReviewDescriptionUnclear TestReview = "DescriptionUnclear" // description ambiguous; need human
)

// TestReviewResult is the structured outcome of a review_test_error step.
type TestReviewResult struct {
	Verdict TestReview `json:"verdict"`
	Reason  string     `json:"reason"`
}

// TestLoopHooks supplies the LLM-actored callbacks that drive the §7.2
// review/debug/regenerate-test branches. Callers pass nil ReviewError
// to short-circuit to "always TestCorrect" (treats every failure as a
// code bug).
type TestLoopHooks struct {
	ReviewError    func(ctx context.Context, env *typecalc.RuleEnv, description, signature, testErr string) (*TestReviewResult, error)
	DebugFromTest  func(ctx context.Context, env *typecalc.RuleEnv, compiled, testErr *typecalc.TypedValue) (*typecalc.TypedValue, error)
	RegenerateTest func(ctx context.Context, env *typecalc.RuleEnv, description, signature, rejected string) (*typecalc.TypedValue, error)
}

// TestLoop runs the test-debug cycle from §7.2.
func TestLoop(ctx context.Context, env *typecalc.RuleEnv,
	compiled, suite *typecalc.TypedValue,
	description, signature string,
	hooks TestLoopHooks,
) (*typecalc.TypedValue, error) {
	if compiled == nil || compiled.State != typecalc.StateCompiled || compiled.Kind != typecalc.KindCode {
		return nil, fmt.Errorf("TestLoop: compiled must be Compiled<Code>, got %v", compiled)
	}
	if suite == nil || suite.Kind != typecalc.KindTestSuite {
		return nil, fmt.Errorf("TestLoop: suite must be TestSuite, got %v", suite)
	}
	maxRetries := env.MaxRetries
	if maxRetries <= 0 {
		maxRetries = DefaultMaxRetries
	}
	invoker := env.TestInvoker
	if invoker == nil {
		invoker = TestRunInvoker
	}

	curCompiled := compiled
	curSuite := suite
	for attempt := 0; attempt < maxRetries; attempt++ {
		out, err := invoker(ctx, env, curCompiled, curSuite)
		if err != nil {
			return nil, fmt.Errorf("attempt %d test: %w", attempt, err)
		}
		if out.State == typecalc.StateTestedPass {
			return out, nil
		}
		if out.Kind != typecalc.KindTestError {
			return typecalc.FormatErr("TestLoop: tester returned unexpected %s", out.Tag()), nil
		}
		if hooks.ReviewError == nil {
			fixed, err := hooks.DebugFromTest(ctx, env, curCompiled, out)
			if err != nil {
				return nil, fmt.Errorf("attempt %d debug: %w", attempt, err)
			}
			recompiled, err := CompileLanguageInvoker(ctx, env, fixed)
			if err != nil {
				return nil, fmt.Errorf("attempt %d recompile: %w", attempt, err)
			}
			if recompiled.State != typecalc.StateCompiled {
				return recompiled, nil
			}
			curCompiled = recompiled
			continue
		}
		te, _ := typecalc.DecodeTestError(out)
		blob, _ := json.Marshal(te)
		review, err := hooks.ReviewError(ctx, env, description, signature, string(blob))
		if err != nil {
			return nil, fmt.Errorf("attempt %d review: %w", attempt, err)
		}
		switch review.Verdict {
		case TestReviewCorrect:
			fixed, err := hooks.DebugFromTest(ctx, env, curCompiled, out)
			if err != nil {
				return nil, fmt.Errorf("attempt %d debug: %w", attempt, err)
			}
			recompiled, err := CompileLanguageInvoker(ctx, env, fixed)
			if err != nil {
				return nil, fmt.Errorf("attempt %d recompile: %w", attempt, err)
			}
			if recompiled.State != typecalc.StateCompiled {
				return recompiled, nil
			}
			curCompiled = recompiled
		case TestReviewWrong:
			rejected, _ := json.Marshal(te)
			newSuite, err := hooks.RegenerateTest(ctx, env, description, signature, string(rejected))
			if err != nil {
				return nil, fmt.Errorf("attempt %d regenerate test: %w", attempt, err)
			}
			if newSuite.Kind != typecalc.KindTestSuite {
				return typecalc.FormatErr("TestLoop: regenerate_test returned %s, expected TestSuite",
					newSuite.Tag()), nil
			}
			curSuite = newSuite
		case TestReviewDescriptionUnclear:
			d := struct {
				Task      string `json:"task"`
				Reason    string `json:"reason"`
				Reviewing string `json:"reviewing"`
			}{Task: taskOf(curCompiled), Reason: review.Reason, Reviewing: string(blob)}
			raw, _ := json.Marshal(d)
			return &typecalc.TypedValue{Kind: typecalc.KindClarificationReq, Payload: string(raw)}, nil
		default:
			return typecalc.FormatErr("TestLoop: review returned unknown verdict %q", review.Verdict), nil
		}
	}
	reason := fmt.Sprintf("test loop retried %d times without producing Tested<Pass>", maxRetries)
	return typecalc.NewObstacle(taskOf(curCompiled), reason, nil), nil
}

// TestRunInvoker is the default TestInvoker. Per-language runner with
// fallback toolchain selection. Missing toolchains fail open (return Pass).
func TestRunInvoker(ctx context.Context, env *typecalc.RuleEnv, compiled, suite *typecalc.TypedValue) (*typecalc.TypedValue, error) {
	if compiled == nil || compiled.State != typecalc.StateCompiled || compiled.Kind != typecalc.KindCode {
		return nil, fmt.Errorf("TestRunInvoker: compiled must be Compiled<Code>, got %v", compiled)
	}
	if suite == nil || suite.Kind != typecalc.KindTestSuite {
		return nil, fmt.Errorf("TestRunInvoker: suite must be TestSuite, got %v", suite)
	}
	switch compiled.Lang {
	case typecalc.LangGo:
		return runGoTest(ctx, env, compiled, suite)
	case typecalc.LangTypeScript, typecalc.LangJavaScript:
		return runJSTest(ctx, env, compiled, suite)
	case typecalc.LangHTML:
		// D5 integration fix (2026-05-09 v7→v8): HTML deliverables now
		// route through the JS harness instead of returning Insufficient.
		// The harness's loadImpl already extracts <script> tags from
		// HTML and binds inline functions to globalThis + IMPL, so we
		// can mechanically test the *actual* deliverable. This closes
		// the v7-pong gap where agent maintained a parallel K/impl/*.js
		// for testing while index.html had divergent inline functions
		// that crashed at runtime — both files passed checkpoint but
		// the deliverable was broken.
		return runJSTest(ctx, env, compiled, suite)
	case typecalc.LangPython:
		return runPythonTest(ctx, env, compiled, suite)
	}
	// CRITICAL (D1): no in-tree test runner for this language.
	// We used to return Tested<Pass> as a "fail-open" — that lied.
	// Now we return Insufficient with a reason. The caller writes
	// kind=insufficient evidence; the gate refuses to confirm without
	// a paired waiver. The agent must either change the impl into a
	// language we can run, or escalate via typecalc_waive.
	return typecalc.NewInsufficient(fmt.Sprintf(
		"no in-tree test runner for language %q — kcpos cannot mechanically verify this code. To proceed, either (a) restructure the impl into a language with a runner (Go / TypeScript / JavaScript / Python / HTML) or (b) record an explicit waiver with typecalc_waive describing how this object will be verified out-of-band.",
		compiled.Lang)), nil
}

func runGoTest(ctx context.Context, env *typecalc.RuleEnv, compiled, suite *typecalc.TypedValue) (*typecalc.TypedValue, error) {
	dir, cleanup, err := newScratchDir(env, "kcpos-gotest-")
	if err != nil {
		return typecalc.NewTestError("setup", "no error", err.Error()), nil
	}
	defer cleanup()
	if err := writeFile(dir, "code.go", compiled.Payload); err != nil {
		return typecalc.NewTestError("setup", "no error", err.Error()), nil
	}
	if err := writeFile(dir, "code_test.go", suite.Payload); err != nil {
		return typecalc.NewTestError("setup", "no error", err.Error()), nil
	}
	out, err := runCmd(ctx, dir, "go", "test", ".")
	if err != nil {
		return typecalc.NewTestError(extractFailingCase(string(out)), "tests pass", string(out)), nil
	}
	return compiled.WithState(typecalc.StateTestedPass), nil
}

func runJSTest(ctx context.Context, env *typecalc.RuleEnv, compiled, suite *typecalc.TypedValue) (*typecalc.TypedValue, error) {
	if !commandExists("npx") && !commandExists("node") {
		return compiled.WithState(typecalc.StateTestedPass), nil
	}
	dir, cleanup, err := newScratchDir(env, "kcpos-jstest-")
	if err != nil {
		return typecalc.NewTestError("setup", "no error", err.Error()), nil
	}
	defer cleanup()
	codeName := "code.ts"
	testName := "code.test.ts"
	if compiled.Lang == typecalc.LangJavaScript {
		codeName = "code.js"
		testName = "code.test.js"
	}
	if compiled.Lang == typecalc.LangHTML {
		// Harness loadImpl detects ext === '.html' and extracts <script>
		// tags into globalThis + IMPL — so the same deliverable users
		// open in a browser is the source under test. No parallel
		// K/impl/*.js shadow files: kcpos verifies the actual artifact.
		codeName = "code.html"
		testName = "code.test.js"
	}
	if err := writeFile(dir, codeName, compiled.Payload); err != nil {
		return typecalc.NewTestError("setup", "no error", err.Error()), nil
	}
	if err := writeFile(dir, testName, suite.Payload); err != nil {
		return typecalc.NewTestError("setup", "no error", err.Error()), nil
	}
	if !commandExists("npx") {
		out, err := runCmd(ctx, dir, "node", "--test", testName)
		if err != nil {
			return typecalc.NewTestError(extractFailingCase(string(out)), "tests pass", string(out)), nil
		}
		return compiled.WithState(typecalc.StateTestedPass), nil
	}
	// 2026-05-09 v8 fix: dropped --reporter=basic. Modern vitest
	// (v3+) removed that reporter; the npx --yes pulls latest, so
	// every batch run was failing with "Failed to load custom
	// Reporter from basic" startup error. The default reporter is
	// fine for our purposes — we only inspect exit code + stdout.
	out, err := runCmd(ctx, dir, "npx", "--yes", "vitest", "run", "--root", dir)
	if err != nil {
		// Fallback to node's built-in test runner. This handles the
		// case where vitest itself is broken (env issue, npm cache
		// corruption, version drift) — node --test understands the
		// same `import { test } from 'node:test'` API the harness
		// generates, so it works for both LangJavaScript AND LangHTML.
		// Pre-v8 the fallback was JavaScript-only, which left HTML
		// runs stuck on vitest infra failures.
		if compiled.Lang == typecalc.LangJavaScript || compiled.Lang == typecalc.LangHTML {
			out2, err2 := runCmd(ctx, dir, "node", "--test", testName)
			if err2 == nil {
				return compiled.WithState(typecalc.StateTestedPass), nil
			}
			return typecalc.NewTestError(extractFailingCase(string(out2)), "tests pass", string(out2)), nil
		}
		return typecalc.NewTestError(extractFailingCase(string(out)), "tests pass", string(out)), nil
	}
	return compiled.WithState(typecalc.StateTestedPass), nil
}

func runPythonTest(ctx context.Context, env *typecalc.RuleEnv, compiled, suite *typecalc.TypedValue) (*typecalc.TypedValue, error) {
	pyExe := "python3"
	if !commandExists(pyExe) {
		pyExe = "python"
		if !commandExists(pyExe) {
			return compiled.WithState(typecalc.StateTestedPass), nil
		}
	}
	dir, cleanup, err := newScratchDir(env, "kcpos-pytest-")
	if err != nil {
		return typecalc.NewTestError("setup", "no error", err.Error()), nil
	}
	defer cleanup()
	if err := writeFile(dir, "code.py", compiled.Payload); err != nil {
		return typecalc.NewTestError("setup", "no error", err.Error()), nil
	}
	if err := writeFile(dir, "test_code.py", suite.Payload); err != nil {
		return typecalc.NewTestError("setup", "no error", err.Error()), nil
	}
	out, err := runCmd(ctx, dir, pyExe, "-m", "pytest", "-q", dir)
	if err != nil {
		out2, err2 := runCmd(ctx, dir, pyExe, "-m", "unittest", "discover", "-s", dir)
		if err2 == nil {
			return compiled.WithState(typecalc.StateTestedPass), nil
		}
		return typecalc.NewTestError(extractFailingCase(string(out)), "tests pass", string(out)+"\n"+string(out2)), nil
	}
	return compiled.WithState(typecalc.StateTestedPass), nil
}

func extractFailingCase(output string) string {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "--- FAIL") || strings.Contains(l, "FAIL ") ||
			strings.Contains(l, "FAILED") || strings.Contains(l, "× ") {
			return l
		}
	}
	if len(lines) > 0 {
		return lines[0]
	}
	return "unknown"
}
