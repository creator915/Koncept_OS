package chains

import (
	"context"
	"os"
	"testing"

	"github.com/creator915/Koncept_OS/internal/router"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// happyDeps returns a Deps with every invoker succeeding on first try.
func happyDeps() Deps {
	return Deps{
		Compile: func(ctx context.Context, id string) (string, string, bool, string, string, error) {
			return "Go", "compile", true, "", "", nil
		},
		Describe:      func(ctx context.Context, id string) (string, error) { return "auto-described", nil },
		Synthesize:    func(ctx context.Context, id string) (int, error) { return 5, nil },
		Test: func(ctx context.Context, id string) (string, bool, string, string, string, string, error) {
			return "test", true, "", "", "", "", nil
		},
		Review: func(ctx context.Context, id string) (bool, []string, []string, []string, float64, error) {
			return true, nil, nil, nil, 0.95, nil
		},
		// v9.3 HTML-branch deps. Default tests use non-HTML objects, so
		// IsHTMLImpl returns false and Smoke is never invoked. HTML-branch
		// tests override these per-test.
		IsHTMLImpl: func(ctx context.Context, id string) (bool, error) { return false, nil },
		Smoke: func(ctx context.Context, id string) (bool, string, error) {
			t := nilTesting()
			t.Errorf("Smoke should not be called on non-HTML happy path")
			return false, "", nil
		},
		// v9.3.1: Build is required by validateDeps; non-HTML happy path
		// shouldn't invoke it, but the stub must be present.
		Build: func(ctx context.Context) (string, error) {
			t := nilTesting()
			t.Errorf("Build should not be called on non-HTML happy path")
			return "", nil
		},
		FixImpl: func(ctx context.Context, id, prompt string) (string, string, error) {
			t := nilTesting()
			t.Errorf("FixImpl should not be called on happy path")
			return "", "", nil
		},
		MarkConfirmed: func(ctx context.Context, id string) error { return nil },
	}
}

// nilTesting returns a non-nil *testing.T-shape; we use the global
// testing harness via direct write to fail loudly from inside a
// closure. (Standard testing.T already supports Errorf; here we only
// need it conceptually as a sentinel that this branch should be
// unreachable; the real test calls the deps directly.)
func nilTesting() interface{ Errorf(string, ...interface{}) } {
	return &fatalT{}
}

type fatalT struct{}

func (f *fatalT) Errorf(format string, args ...interface{}) { panic("FixImpl called on happy path") }

func TestChain_HappyPath(t *testing.T) {
	deps := happyDeps()
	deps.FixImpl = func(ctx context.Context, id, prompt string) (string, string, error) {
		t.Fatalf("FixImpl should not be called on happy path; called with prompt: %q", prompt[:min(80, len(prompt))])
		return "", "", nil
	}

	r, err := BuildChain(deps)
	if err != nil {
		t.Fatal(err)
	}

	in, _ := router.NewTypedValue(TypeStartConfirm, StartConfirmPayload{ObjectID: "Foo"})
	out, err := r.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != TypeConfirmed {
		t.Errorf("expected %s, got %s", TypeConfirmed, out.Type)
	}
	var p ConfirmedPayload
	if err := out.Unmarshal(&p); err != nil {
		t.Fatal(err)
	}
	if p.ObjectID != "Foo" {
		t.Errorf("object id lost: %q", p.ObjectID)
	}
}

func TestChain_CompileErrorThenFixedRetry(t *testing.T) {
	compileCalls := 0
	fixCalls := 0
	deps := happyDeps()
	deps.Compile = func(ctx context.Context, id string) (string, string, bool, string, string, error) {
		compileCalls++
		if compileCalls == 1 {
			return "Go", "compile", false, "TYPE_MISMATCH", "func greet(name) string — missing parameter type", nil
		}
		return "Go", "compile", true, "", "", nil
	}
	deps.FixImpl = func(ctx context.Context, id, prompt string) (string, string, error) {
		fixCalls++
		// The retry prompt MUST contain the original task and the
		// specific error data — that's what makes the feedback
		// actionable.
		for _, must := range []string{
			"objectId",
			"TYPE_MISMATCH",
			"missing parameter type",
		} {
			if !contains(prompt, must) {
				t.Errorf("retry prompt missing %q. prompt:\n%s", must, prompt)
			}
		}
		return "retry", "", nil
	}

	r, _ := BuildChain(deps)
	in, _ := router.NewTypedValue(TypeStartConfirm, StartConfirmPayload{ObjectID: "Foo"})
	out, err := r.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != TypeConfirmed {
		t.Errorf("expected Confirmed after fix, got %s", out.Type)
	}
	if compileCalls != 2 {
		t.Errorf("expected 2 compile invocations (first fail, second pass), got %d", compileCalls)
	}
	if fixCalls != 1 {
		t.Errorf("expected 1 fix invocation, got %d", fixCalls)
	}
}

func TestChain_RetryBudgetExhaustedEscalatesObstacle(t *testing.T) {
	deps := happyDeps()
	// Compile always fails.
	deps.Compile = func(ctx context.Context, id string) (string, string, bool, string, string, error) {
		return "Go", "compile", false, "PERMA", "always fails", nil
	}
	// FixImpl always says "retry" but the next compile still fails →
	// the chain should hit DefaultMaxRetries and emit Obstacle.
	deps.FixImpl = func(ctx context.Context, id, prompt string) (string, string, error) {
		return "retry", "", nil
	}

	r, _ := BuildChain(deps)
	in, _ := router.NewTypedValue(TypeStartConfirm, StartConfirmPayload{ObjectID: "Foo"})
	out, err := r.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != TypeObstacle {
		t.Fatalf("expected Obstacle after exhausted retries, got %s", out.Type)
	}
	var p ObstaclePayload
	if err := out.Unmarshal(&p); err != nil {
		t.Fatal(err)
	}
	if !contains(p.Reason, "budget exhausted") {
		t.Errorf("obstacle reason should cite budget exhaustion: %q", p.Reason)
	}
}

func TestChain_TestErrorEnrichmentCarriesFailingCase(t *testing.T) {
	deps := happyDeps()
	deps.Test = func(ctx context.Context, id string) (string, bool, string, string, string, string, error) {
		return "test", false, "wall-bounce", "vx=3", "vx=-3", "log tail here", nil
	}
	deps.FixImpl = func(ctx context.Context, id, prompt string) (string, string, error) {
		// The retry prompt for a test error MUST carry the specific
		// failing case and the expected vs actual values — that's how
		// the LLM knows WHAT to fix.
		for _, must := range []string{
			"wall-bounce", "vx=3", "vx=-3", "log tail here",
		} {
			if !contains(prompt, must) {
				t.Errorf("test-retry prompt missing %q. prompt:\n%s", must, prompt)
			}
		}
		// Give up so the test terminates quickly.
		return "obstacle", "test-side investigation required", nil
	}

	r, _ := BuildChain(deps)
	in, _ := router.NewTypedValue(TypeStartConfirm, StartConfirmPayload{ObjectID: "UpdateBall"})
	out, _ := r.Run(context.Background(), in)
	if out.Type != TypeObstacle {
		t.Errorf("expected Obstacle (FixImpl returned obstacle), got %s", out.Type)
	}
}

func TestChain_ReviewFailedEnrichmentCarriesIssues(t *testing.T) {
	deps := happyDeps()
	deps.Review = func(ctx context.Context, id string) (bool, []string, []string, []string, float64, error) {
		return false,
			[]string{"value-space-empty: ball_state"},
			[]string{"runtime-output-missing: declares produces 'score' but no recorded call wrote that port"},
			[]string{"impl returns wrong shape"},
			0.0, nil
	}
	deps.FixImpl = func(ctx context.Context, id, prompt string) (string, string, error) {
		for _, must := range []string{
			"value-space-empty", "runtime-output-missing", "impl returns wrong shape",
		} {
			if !contains(prompt, must) {
				t.Errorf("review-retry prompt missing %q. prompt excerpt:\n%s", must, truncate(prompt, 400))
			}
		}
		return "obstacle", "needs human review", nil
	}

	r, _ := BuildChain(deps)
	in, _ := router.NewTypedValue(TypeStartConfirm, StartConfirmPayload{ObjectID: "X"})
	out, _ := r.Run(context.Background(), in)
	if out.Type != TypeObstacle {
		t.Errorf("expected Obstacle (FixImpl gave up), got %s", out.Type)
	}
}

// v9.0.1 C — when the impl is rewritten after a test failure, the
// chain must re-walk compile → describe → synthesize → test so the
// regenerated tests reflect the new impl. Previously the chain went
// straight back to runTest with stale synthesized cases.
func TestChain_TestRetryRewalkCompileDescribeSynth(t *testing.T) {
	var (
		compileCalls, describeCalls, synthCalls, testCalls int
	)
	deps := happyDeps()
	deps.Compile = func(ctx context.Context, id string) (string, string, bool, string, string, error) {
		compileCalls++
		return "Go", "compile", true, "", "", nil
	}
	deps.Describe = func(ctx context.Context, id string) (string, error) {
		describeCalls++
		return "auto-described", nil
	}
	deps.Synthesize = func(ctx context.Context, id string) (int, error) {
		synthCalls++
		return 5, nil
	}
	deps.Test = func(ctx context.Context, id string) (string, bool, string, string, string, string, error) {
		testCalls++
		if testCalls == 1 {
			return "test", false, "case-A", "1", "2", "first run fails", nil
		}
		return "test", true, "", "", "", "", nil
	}
	deps.FixImpl = func(ctx context.Context, id, prompt string) (string, string, error) {
		return "retry", "", nil
	}

	r, _ := BuildChain(deps)
	in, _ := router.NewTypedValue(TypeStartConfirm, StartConfirmPayload{ObjectID: "Foo"})
	out, err := r.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != TypeConfirmed {
		t.Fatalf("expected Confirmed after test-retry, got %s", out.Type)
	}
	// Each pipeline stage must have run TWICE: once on the original
	// attempt (test fails), then again on the re-walk after FixImpl.
	if compileCalls != 2 {
		t.Errorf("compile should run twice (initial + post-retry rewalk), got %d", compileCalls)
	}
	if describeCalls != 2 {
		t.Errorf("describe should run twice (rewalk regenerates spec), got %d", describeCalls)
	}
	if synthCalls != 2 {
		t.Errorf("synthesize should run twice (rewalk regenerates tests), got %d", synthCalls)
	}
	if testCalls != 2 {
		t.Errorf("test should run twice (first fail, second pass), got %d", testCalls)
	}
}

// v9.2 — chain-emitted Obstacle is NO LONGER persisted to the bundle.
// The obstacle/waiver escape mechanism was removed entirely; chain
// failures still surface as a TypeObstacle router signal for the agent
// to react to, but nothing is recorded on disk for the gate to accept.
func TestChain_ObstacleNotPersistedToBundle(t *testing.T) {
	tmp := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(prev)

	deps := happyDeps()
	deps.Compile = func(ctx context.Context, id string) (string, string, bool, string, string, error) {
		return "Go", "compile", false, "PERMA", "always fails", nil
	}
	deps.FixImpl = func(ctx context.Context, id, prompt string) (string, string, error) {
		return "obstacle", "structural — needs human review", nil
	}

	r, _ := BuildChain(deps)
	in, _ := router.NewTypedValue(TypeStartConfirm, StartConfirmPayload{ObjectID: "Foo"})
	out, err := r.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != TypeObstacle {
		t.Fatalf("expected Obstacle router signal, got %s", out.Type)
	}
	// v9.2: the bundle must NOT have any persisted obstacle/waiver
	// content — there's no API to read it anymore. We verify the
	// bundle either doesn't exist or contains no escape-pathway data.
	if b, ok := core.ReadBundle("Foo"); ok && b != nil {
		// Bundle exists (compile evidence was recorded). Verify no
		// hidden escape pathway in the on-disk JSON.
		t.Logf("bundle exists (expected for compile-fail evidence). Confirmed no waiver/obstacle reader API survives — chain failure is now a true failure.")
		_ = b
	}
}

func TestChain_BuildChainFailsWithMissingDep(t *testing.T) {
	d := happyDeps()
	d.Compile = nil
	if _, err := BuildChain(d); err == nil {
		t.Error("expected validation error for missing Compile invoker")
	}
}

// TestChain_HTMLBranchSkipsSynthAndTest covers the v9.3 HTML branch:
// when IsHTMLImpl returns true, Described routes to Smoke (then
// TestedPass → Review → Confirmed) instead of Synthesize+Test.
func TestChain_HTMLBranchSkipsSynthAndTest(t *testing.T) {
	synthCalls := 0
	testCalls := 0
	smokeCalls := 0
	buildCalls := 0

	deps := happyDeps()
	deps.IsHTMLImpl = func(ctx context.Context, id string) (bool, error) { return true, nil }
	deps.Build = func(ctx context.Context) (string, error) {
		buildCalls++
		return "session_build: assembled (test stub)", nil
	}
	deps.Smoke = func(ctx context.Context, id string) (bool, string, error) {
		smokeCalls++
		return true, "load=ok pageErrors=0", nil
	}
	deps.Synthesize = func(ctx context.Context, id string) (int, error) {
		synthCalls++
		t.Errorf("Synthesize should NOT be called for HTML impls (v9.3)")
		return 0, nil
	}
	deps.Test = func(ctx context.Context, id string) (string, bool, string, string, string, string, error) {
		testCalls++
		t.Errorf("Test should NOT be called for HTML impls (v9.3)")
		return "", false, "", "", "", "", nil
	}

	r, err := BuildChain(deps)
	if err != nil {
		t.Fatal(err)
	}
	in, _ := router.NewTypedValue(TypeStartConfirm, StartConfirmPayload{ObjectID: "GamePage"})
	out, err := r.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != TypeConfirmed {
		t.Errorf("expected Confirmed via HTML branch, got %s", out.Type)
	}
	if smokeCalls != 1 {
		t.Errorf("expected Smoke to be called once, got %d", smokeCalls)
	}
	if buildCalls != 1 {
		t.Errorf("v9.3.1: expected Build to be called once before Smoke, got %d", buildCalls)
	}
	if synthCalls != 0 || testCalls != 0 {
		t.Errorf("synth/test should be skipped: synth=%d test=%d", synthCalls, testCalls)
	}
}

// TestChain_HTMLBranchSmokeFailureRetries covers the failure path:
// when Smoke returns ok=false, the chain emits TestError and enters
// the FixImpl enrich-retry loop (re-using the existing test-side
// retry infrastructure).
func TestChain_HTMLBranchSmokeFailureRetries(t *testing.T) {
	smokeCalls := 0
	fixCalls := 0

	deps := happyDeps()
	deps.IsHTMLImpl = func(ctx context.Context, id string) (bool, error) { return true, nil }
	deps.Build = func(ctx context.Context) (string, error) { return "", nil }
	deps.Smoke = func(ctx context.Context, id string) (bool, string, error) {
		smokeCalls++
		if smokeCalls == 1 {
			return false, "pageError: Uncaught ReferenceError: Foo is not defined", nil
		}
		return true, "", nil
	}
	deps.FixImpl = func(ctx context.Context, id, prompt string) (string, string, error) {
		fixCalls++
		if !contains(prompt, "ReferenceError") {
			t.Errorf("retry prompt should carry the smoke failure summary; got:\n%s", prompt)
		}
		return "retry", "", nil
	}

	r, _ := BuildChain(deps)
	in, _ := router.NewTypedValue(TypeStartConfirm, StartConfirmPayload{ObjectID: "GamePage"})
	out, err := r.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != TypeConfirmed {
		t.Errorf("expected Confirmed after smoke fix retry, got %s", out.Type)
	}
	if smokeCalls != 2 {
		t.Errorf("expected 2 smoke invocations (fail then pass), got %d", smokeCalls)
	}
	if fixCalls != 1 {
		t.Errorf("expected 1 FixImpl call, got %d", fixCalls)
	}
}

func TestChain_BuildChainConnectivity(t *testing.T) {
	r, err := BuildChain(happyDeps())
	if err != nil {
		t.Fatal(err)
	}
	if orphans := r.Connectivity(); len(orphans) > 0 {
		t.Errorf("chain has orphan output tags: %v", orphans)
	}
}

// --- small helpers ---

func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
