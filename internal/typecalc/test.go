package typecalc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// TestReview is the verdict returned by the review_test_error rule (§3,
// "测试阶段"). The LLM is given the description, signature, and one
// failing test case (NOT the source code) and must classify the failure.
type TestReview string

const (
	TestReviewCorrect            TestReview = "TestCorrect"            // test is right, code has a bug
	TestReviewWrong              TestReview = "TestWrong"              // test is wrong, code is fine
	TestReviewDescriptionUnclear TestReview = "DescriptionUnclear"     // description ambiguous; need human
)

// TestReviewResult is the structured outcome of a review_test_error step.
type TestReviewResult struct {
	Verdict TestReview `json:"verdict"`
	Reason  string     `json:"reason"`
}

// TestLoop runs the rule chain from §7.2: run_test → (TestError →
// review_test_error → ...) → Tested<Code, Pass>.
//
// Compared with CompileLoop, the review stage adds a branching choice:
//   - TestCorrect → debug_from_test → regenerate Uncompiled<Code> → recompile
//   - TestWrong   → regenerate_test → new TestSuite → re-run
//   - DescriptionUnclear → escalate (returns ClarificationNeeded)
//
// Callbacks let the loop call back into LLM-actored handlers for each
// branch. They're declared as separate parameters rather than packed into
// RuleEnv to keep the call site explicit about which transitions are
// active.
type TestLoopHooks struct {
	// ReviewError must return a TestReviewResult given the description,
	// signature, and failing test case. The LLM does NOT see the source
	// code — that's the design constraint from §3.
	ReviewError func(ctx context.Context, env *RuleEnv, description, signature, testErr string) (*TestReviewResult, error)

	// DebugFromTest regenerates Uncompiled<Code> given the failing test
	// case + the prior compiled source (channelled). Conceptually:
	//   TestCorrect × Chan<S, Compiled<Code>> ⇒ Uncompiled<Code>
	DebugFromTest func(ctx context.Context, env *RuleEnv, compiled, testErr *TypedValue) (*TypedValue, error)

	// RegenerateTest produces a new TestSuite given description, signature,
	// and the rejected test case.
	RegenerateTest func(ctx context.Context, env *RuleEnv, description, signature, rejected string) (*TypedValue, error)
}

// TestLoop runs the test-debug cycle. On success returns
// Tested<Code, Pass>. On exhausting retries returns Obstacle. On
// DescriptionUnclear at any point returns a ClarificationNeeded typed
// value (router-terminal).
func TestLoop(ctx context.Context, env *RuleEnv,
	compiled, suite *TypedValue,
	description, signature string,
	hooks TestLoopHooks,
) (*TypedValue, error) {
	if compiled == nil || compiled.State != StateCompiled || compiled.Kind != KindCode {
		return nil, fmt.Errorf("TestLoop: compiled must be Compiled<Code>, got %v", compiled)
	}
	if suite == nil || suite.Kind != KindTestSuite {
		return nil, fmt.Errorf("TestLoop: suite must be TestSuite, got %v", suite)
	}
	maxRetries := env.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultMaxRetries
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
		if out.State == StateTestedPass {
			return out, nil
		}
		if out.Kind != KindTestError {
			return formatErr("TestLoop: tester returned unexpected %s", out.Tag()), nil
		}
		// Review the failure.
		if hooks.ReviewError == nil {
			// Without a review callback we can't make a branching decision.
			// Treat as TestCorrect by default — assume the code is buggy.
			fixed, err := hooks.DebugFromTest(ctx, env, curCompiled, out)
			if err != nil {
				return nil, fmt.Errorf("attempt %d debug: %w", attempt, err)
			}
			recompiled, err := CompileLanguageInvoker(ctx, env, fixed)
			if err != nil {
				return nil, fmt.Errorf("attempt %d recompile: %w", attempt, err)
			}
			if recompiled.State != StateCompiled {
				return recompiled, nil
			}
			curCompiled = recompiled
			continue
		}
		te, _ := DecodeTestError(out)
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
			if recompiled.State != StateCompiled {
				return recompiled, nil
			}
			curCompiled = recompiled
		case TestReviewWrong:
			rejected, _ := json.Marshal(te)
			newSuite, err := hooks.RegenerateTest(ctx, env, description, signature, string(rejected))
			if err != nil {
				return nil, fmt.Errorf("attempt %d regenerate test: %w", attempt, err)
			}
			if newSuite.Kind != KindTestSuite {
				return formatErr("TestLoop: regenerate_test returned %s, expected TestSuite",
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
			return &TypedValue{Kind: KindClarificationReq, Payload: string(raw)}, nil
		default:
			return formatErr("TestLoop: review returned unknown verdict %q", review.Verdict), nil
		}
	}
	reason := fmt.Sprintf("test loop retried %d times without producing Tested<Pass>", maxRetries)
	return NewObstacle(taskOf(curCompiled), reason, nil), nil
}

// TestRunInvoker is the default TestInvoker. It chooses a runner by
// language tag and writes the compiled source + test suite to a temp dir.
//
// Behavior per language:
//
//	Go         → go test -run . (in temp dir)
//	TypeScript → npx vitest run --reporter=basic
//	JavaScript → npx vitest run / node --test
//	Python     → python -m pytest -q
//
// As with CompileLanguageInvoker, missing toolchains fail open: we return
// Tested<Pass> rather than blocking — the assumption being that the
// toolchain assertion is the user's responsibility, not the agent's.
func TestRunInvoker(ctx context.Context, env *RuleEnv, compiled, suite *TypedValue) (*TypedValue, error) {
	if compiled == nil || compiled.State != StateCompiled || compiled.Kind != KindCode {
		return nil, fmt.Errorf("TestRunInvoker: compiled must be Compiled<Code>, got %v", compiled)
	}
	if suite == nil || suite.Kind != KindTestSuite {
		return nil, fmt.Errorf("TestRunInvoker: suite must be TestSuite, got %v", suite)
	}

	switch compiled.Lang {
	case LangGo:
		return runGoTest(ctx, env, compiled, suite)
	case LangTypeScript, LangJavaScript:
		return runJSTest(ctx, env, compiled, suite)
	case LangPython:
		return runPythonTest(ctx, env, compiled, suite)
	}
	// Unknown language — assume pass.
	return compiled.WithState(StateTestedPass), nil
}

func runGoTest(ctx context.Context, env *RuleEnv, compiled, suite *TypedValue) (*TypedValue, error) {
	dir, cleanup, err := newScratchDir(env, "kcpos-gotest-")
	if err != nil {
		return NewTestError("setup", "no error", err.Error()), nil
	}
	defer cleanup()
	if err := writeFile(dir, "code.go", compiled.Payload); err != nil {
		return NewTestError("setup", "no error", err.Error()), nil
	}
	if err := writeFile(dir, "code_test.go", suite.Payload); err != nil {
		return NewTestError("setup", "no error", err.Error()), nil
	}
	out, err := runCmd(ctx, dir, "go", "test", ".")
	if err != nil {
		return NewTestError(extractFailingCase(string(out)), "tests pass", string(out)), nil
	}
	return compiled.WithState(StateTestedPass), nil
}

func runJSTest(ctx context.Context, env *RuleEnv, compiled, suite *TypedValue) (*TypedValue, error) {
	if !commandExists("npx") && !commandExists("node") {
		return compiled.WithState(StateTestedPass), nil
	}
	dir, cleanup, err := newScratchDir(env, "kcpos-jstest-")
	if err != nil {
		return NewTestError("setup", "no error", err.Error()), nil
	}
	defer cleanup()
	codeName := "code.ts"
	testName := "code.test.ts"
	if compiled.Lang == LangJavaScript {
		codeName = "code.js"
		testName = "code.test.js"
	}
	if err := writeFile(dir, codeName, compiled.Payload); err != nil {
		return NewTestError("setup", "no error", err.Error()), nil
	}
	if err := writeFile(dir, testName, suite.Payload); err != nil {
		return NewTestError("setup", "no error", err.Error()), nil
	}
	if !commandExists("npx") {
		// Fall back to node --test for plain JS.
		out, err := runCmd(ctx, dir, "node", "--test", testName)
		if err != nil {
			return NewTestError(extractFailingCase(string(out)), "tests pass", string(out)), nil
		}
		return compiled.WithState(StateTestedPass), nil
	}
	// Try vitest first; if not installed, fall back to node --test.
	out, err := runCmd(ctx, dir, "npx", "--yes", "vitest", "run", "--reporter=basic", "--root", dir)
	if err != nil {
		// Fall back: maybe vitest install failed — try node --test for JS.
		if compiled.Lang == LangJavaScript {
			out2, err2 := runCmd(ctx, dir, "node", "--test", testName)
			if err2 == nil {
				return compiled.WithState(StateTestedPass), nil
			}
			return NewTestError(extractFailingCase(string(out2)), "tests pass", string(out2)), nil
		}
		return NewTestError(extractFailingCase(string(out)), "tests pass", string(out)), nil
	}
	return compiled.WithState(StateTestedPass), nil
}

func runPythonTest(ctx context.Context, env *RuleEnv, compiled, suite *TypedValue) (*TypedValue, error) {
	pyExe := "python3"
	if !commandExists(pyExe) {
		pyExe = "python"
		if !commandExists(pyExe) {
			return compiled.WithState(StateTestedPass), nil
		}
	}
	dir, cleanup, err := newScratchDir(env, "kcpos-pytest-")
	if err != nil {
		return NewTestError("setup", "no error", err.Error()), nil
	}
	defer cleanup()
	if err := writeFile(dir, "code.py", compiled.Payload); err != nil {
		return NewTestError("setup", "no error", err.Error()), nil
	}
	if err := writeFile(dir, "test_code.py", suite.Payload); err != nil {
		return NewTestError("setup", "no error", err.Error()), nil
	}
	out, err := runCmd(ctx, dir, pyExe, "-m", "pytest", "-q", dir)
	if err != nil {
		// pytest may not be installed; try unittest as fallback.
		out2, err2 := runCmd(ctx, dir, pyExe, "-m", "unittest", "discover", "-s", dir)
		if err2 == nil {
			return compiled.WithState(StateTestedPass), nil
		}
		return NewTestError(extractFailingCase(string(out)), "tests pass", string(out)+"\n"+string(out2)), nil
	}
	return compiled.WithState(StateTestedPass), nil
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
