package chains

import (
	"context"
	"fmt"
	"strings"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/router"
)

// DefaultMaxRetries caps per-object enrich-retry cycles. 5 matches
// core.CycleCap — exhausting the cycle counter is the same event
// as exhausting the chain's retry budget.
const DefaultMaxRetries = 5

// Deps is the injection seam: handlers in this chain call into the
// existing typecalc tooling via these function-valued fields rather
// than importing the tools/typecalc package directly. This avoids
// the import cycle (tools/typecalc → router → tools/typecalc) and
// lets tests inject deterministic stubs without booting the full LLM
// + harness stack.
//
// Production wires these to the concrete tool implementations in
// internal/tools/typecalc/ and internal/typecalc/.
type Deps struct {
	// Compile runs the equivalent of typecalc_compile. Implementation
	// updates the on-disk bundle's Compile section. Returns
	// (lang, kind, ok, errorCode, errorLog, err).
	Compile func(ctx context.Context, objectID string) (lang string, kind string, ok bool, errorCode, errorLog string, err error)

	// Describe runs typecalc_describe. Returns the freshly written
	// description (or "" if the on-disk Spec section is still fresh
	// and no LLM call was needed).
	Describe func(ctx context.Context, objectID string) (description string, err error)

	// Synthesize runs typecalc_synthesize_tests. Returns the case
	// count for progress display.
	Synthesize func(ctx context.Context, objectID string) (caseCount int, err error)

	// Test runs typecalc_test. Returns (kind, ok, failingCase, expected, actual, runnerLogTail, err).
	// failingCase / expected / actual are populated only on TestError.
	Test func(ctx context.Context, objectID string) (kind string, ok bool, failingCase, expected, actual, runnerLog string, err error)

	// Smoke runs runtime_smoke for an HTML deliverable. The chain's HTML
	// branch (Described → Build → Smoke → TestedPass) calls this in place
	// of Synthesize+Test, which the vm.Script harness cannot meaningfully
	// run against a browser page (v92 batch had 5/5 instances waste
	// 30–80 minutes looping typecalc_test on HTML impls). Returns
	// (ok, summary log tail, err). summary is what's surfaced via the
	// TestError envelope on smoke failure.
	Smoke func(ctx context.Context, objectID string) (ok bool, summary string, err error)

	// Build runs session_build to assemble fragments into the deliverable
	// before Smoke. v9.3.1: pre-fix, smoke loaded a stub index.html that
	// didn't include any fragment code (chicken-and-egg from v93-02 retro:
	// agent couldn't smoke-test the fragment because session_build only
	// ran at Handler 1.3's build step at root-finish). With reference-mode session_build being
	// cheap (just writes a `<script src>` list), the chain can call it
	// before every smoke without breaking the bank. Returns the
	// build report or err.
	Build func(ctx context.Context) (report string, err error)

	// IsHTMLImpl returns true when the object's impl path ends in .html /
	// .htm. The chain consults this after Compiled/Described to decide
	// whether to enter the HTML branch (runtime_smoke) or the JS-fragment
	// /unit branch (synthesize → test). Production wires this to a graph
	// lookup; tests stub for branching coverage.
	IsHTMLImpl func(ctx context.Context, objectID string) (bool, error)

	// Review runs typecalc_review. Returns (ok, staticIssues, runtimeIssues, reviewerReasons, confidence, err).
	Review func(ctx context.Context, objectID string) (ok bool, staticIssues, runtimeIssues, reviewerReasons []string, confidence float64, err error)

	// FixImpl is the LLM-driven retry handler invoker. Given a
	// Request<...> formatted prompt, it asks the LLM to either
	// (a) emit a corrected impl source and overwrite the file
	// (then we re-enter the chain at Compiled<Object> by calling
	// Compile again), or (b) declare Obstacle. Returns the chosen
	// branch and any obstacle reason.
	//
	// Branch == "retry" means the impl was rewritten; the chain
	// should re-compile.
	// Branch == "obstacle" means escalate; the chain emits Obstacle.
	FixImpl func(ctx context.Context, objectID, requestPrompt string) (branch string, obstacleReason string, err error)

	// MarkConfirmed atomically updates graph.Objects[id].Status to
	// confirmed. Equivalent to graph_merge_object id=X patch={status:confirmed}.
	MarkConfirmed func(ctx context.Context, objectID string) error

	// Characterize runs the brownfield characterization front stage
	// (屎山代码维护Agent设计文档 v1.0 Part 6.6): synthesize probes →
	// run them against the UNTRUSTED legacy artifact → transcribe
	// observed behavior into a golden lock → persist Finite/Reproducible
	// evidence + Oracle into the object's bundle. Returns (locked,
	// unlocked, err).
	//
	// OPTIONAL — unlike every Dep above, this is NOT required by
	// validateDeps. When nil the StartCharacterize/Characterized
	// handlers are simply not registered: the chain is exactly the
	// pre-existing greenfield machine. When wired, it adds a brownfield
	// entry that feeds the recovered contract into the same compile→…→
	// confirm pipeline (now guarded by the gate's [method-use-rule]).
	Characterize func(ctx context.Context, objectID string) (locked int, unlocked int, err error)

	// GraphPath optionally overrides the default K/graph.json location
	// (used by tests). Empty = production default.
	GraphPath string
}

// BuildChain assembles a Router with every handler in the
// uncompiled-to-confirmed pipeline registered against d. Call
// BuildChain once, then run.Run(initialValue) per object.
//
// The connectivity check after every registration guarantees the
// graph is closed before the first Run — no orphan output types.
func BuildChain(d Deps) (*router.Router, error) {
	if err := validateDeps(d); err != nil {
		return nil, err
	}
	r := router.NewRouter()

	// StartConfirm — first hop. We compile, then move on.
	r.Register(&router.HandlerFunc{
		In:  TypeStartConfirm,
		Out: []string{TypeCompiled, TypeCompileError, TypeObstacle},
		Run: func(ctx context.Context, in router.TypedValue) (router.TypedValue, error) {
			var p StartConfirmPayload
			if err := in.Unmarshal(&p); err != nil {
				return router.TypedValue{}, err
			}
			return runCompile(ctx, d, p.ObjectID, 0)
		},
	})

	// CompileError → Request<Compile> via enricher.
	r.Register(&router.EnrichHandler{
		In:  TypeCompileError,
		Out: TypeRequestCompile,
		Transform: func(in router.TypedValue) (router.Request, error) {
			var p CompileErrorPayload
			if err := in.Unmarshal(&p); err != nil {
				return router.Request{}, err
			}
			return router.Request{
				Task: fmt.Sprintf("Compile object %q (lang=%s) successfully.", p.ObjectID, p.Lang),
				Context: map[string]string{
					"objectId":   p.ObjectID,
					"errorCode":  p.ErrorCode,
					"errorLog":   p.ErrorLog,
					"attempts":   fmt.Sprintf("%d", p.Attempts),
				},
				Guidance: []string{
					"Read the errorLog and locate the exact line/symbol it complains about.",
					"Edit the impl file at graph.Objects[" + p.ObjectID + "].Impl to fix only the reported issue.",
					"Do NOT change unrelated behavior — surgical edits.",
					"After editing, the router will re-run compile automatically.",
				},
				Attempts: p.Attempts,
			}, nil
		},
	})

	// Request<Compile> → retry compile or obstacle.
	r.Register(&router.HandlerFunc{
		In:  TypeRequestCompile,
		Out: []string{TypeCompiled, TypeCompileError, TypeObstacle},
		Run: func(ctx context.Context, in router.TypedValue) (router.TypedValue, error) {
			return runRetry(ctx, d, in, "compile")
		},
	})

	// Compiled → describe.
	r.Register(&router.HandlerFunc{
		In:  TypeCompiled,
		Out: []string{TypeDescribed, TypeObstacle},
		Run: func(ctx context.Context, in router.TypedValue) (router.TypedValue, error) {
			var p CompiledPayload
			if err := in.Unmarshal(&p); err != nil {
				return router.TypedValue{}, err
			}
			desc, err := d.Describe(ctx, p.ObjectID)
			if err != nil {
				return makeObstacle(p.ObjectID, "describe failed: "+err.Error(), TypeCompiled), nil
			}
			out, _ := router.NewTypedValue(TypeDescribed, DescribedPayload{ObjectID: p.ObjectID, Description: desc})
			return out, nil
		},
	})

	// Described → synthesize (JS/Go/Python branch) OR Smoke (HTML branch).
	// v9.3: HTML deliverables route through runtime_smoke instead of
	// synthesize+test. The vm.Script harness used by typecalc_test cannot
	// model the browser (no DOM, no requestAnimationFrame, no canvas), so
	// running it against an HTML impl is a category error that wasted
	// hours of agent loop time in the v92 batch. The smoke handler boots
	// real headless Chromium and treats `loadFired && no pageErrors` as
	// the equivalent of Tested<Pass>.
	r.Register(&router.HandlerFunc{
		In:  TypeDescribed,
		Out: []string{TypeSynthesized, TypeTestedPass, TypeTestError, TypeObstacle},
		Run: func(ctx context.Context, in router.TypedValue) (router.TypedValue, error) {
			var p DescribedPayload
			if err := in.Unmarshal(&p); err != nil {
				return router.TypedValue{}, err
			}
			isHTML, herr := d.IsHTMLImpl(ctx, p.ObjectID)
			if herr != nil {
				return makeObstacle(p.ObjectID, "IsHTMLImpl check failed: "+herr.Error(), TypeDescribed), nil
			}
			if isHTML {
				// v9.3.1: assemble fragments into the deliverable before
				// booting the browser. reference-mode session_build is
				// cheap (writes only the kcpos block), so running it here
				// keeps the smoke target in sync with the current fragment
				// set on every chain pass. Without this, smoke loads a
				// stub deliverable that doesn't contain the fragment under
				// test — v93-02 retro called out exactly this confusion.
				if _, berr := d.Build(ctx); berr != nil {
					return makeObstacle(p.ObjectID, "session_build (pre-smoke) failed: "+berr.Error(), TypeDescribed), nil
				}
				ok, summary, serr := d.Smoke(ctx, p.ObjectID)
				if serr != nil {
					return makeObstacle(p.ObjectID, "runtime_smoke failed to run: "+serr.Error(), TypeDescribed), nil
				}
				if ok {
					out, _ := router.NewTypedValue(TypeTestedPass, TestedPassPayload{ObjectID: p.ObjectID})
					return out, nil
				}
				// Smoke failure → TestError so the existing retry/enrich
				// loop picks it up. The "test" wording in TestError is
				// a slight misnomer for the HTML branch but the retry
				// path is identical: enrich → FixImpl → re-compile.
				out, _ := router.NewTypedValue(TypeTestError, TestErrorPayload{
					ObjectID: p.ObjectID, RunnerLog: summary, Attempts: 1,
				})
				return out, nil
			}
			n, err := d.Synthesize(ctx, p.ObjectID)
			if err != nil {
				return makeObstacle(p.ObjectID, "synthesize_tests failed: "+err.Error(), TypeDescribed), nil
			}
			if n == 0 {
				// CANNOT_SYNTHESIZE — agent must restructure the impl or
				// extend kcpos's runner support. v9.2: no waiver escape.
				return makeObstacle(p.ObjectID, "synthesize_tests returned CANNOT_SYNTHESIZE — port observation may need adjustment, or the contract is too implicit to test mechanically", TypeDescribed), nil
			}
			out, _ := router.NewTypedValue(TypeSynthesized, SynthesizedPayload{ObjectID: p.ObjectID, CaseCount: n})
			return out, nil
		},
	})

	// Synthesized → test.
	r.Register(&router.HandlerFunc{
		In:  TypeSynthesized,
		Out: []string{TypeTestedPass, TypeTestError, TypeObstacle},
		Run: func(ctx context.Context, in router.TypedValue) (router.TypedValue, error) {
			var p SynthesizedPayload
			if err := in.Unmarshal(&p); err != nil {
				return router.TypedValue{}, err
			}
			return runTest(ctx, d, p.ObjectID, 0)
		},
	})

	// TestError → Request<Test>.
	r.Register(&router.EnrichHandler{
		In:  TypeTestError,
		Out: TypeRequestTest,
		Transform: func(in router.TypedValue) (router.Request, error) {
			var p TestErrorPayload
			if err := in.Unmarshal(&p); err != nil {
				return router.Request{}, err
			}
			ctx := map[string]string{"objectId": p.ObjectID, "attempts": fmt.Sprintf("%d", p.Attempts)}
			if p.FailingCase != "" {
				ctx["failingCase"] = p.FailingCase
			}
			if p.Expected != "" {
				ctx["expected"] = p.Expected
			}
			if p.Actual != "" {
				ctx["actual"] = p.Actual
			}
			if p.RunnerLog != "" {
				ctx["runnerLogTail"] = p.RunnerLog
			}
			return router.Request{
				Task: fmt.Sprintf("Test object %q until typecalc_test returns Tested<Pass>.", p.ObjectID),
				Context: ctx,
				Guidance: []string{
					"Inspect the failing case: what did the test expect, and what did the impl produce?",
					"If the impl is wrong: edit it to match the test's expected behavior.",
					"If the test is wrong (e.g. it assumes a port that doesn't exist): regenerate via typecalc_synthesize_tests after re-running typecalc_describe.",
					"Do NOT silently change the contract (intent) — surface that as Obstacle instead.",
					"After your edits, the router re-runs typecalc_test automatically.",
				},
				Attempts: p.Attempts,
			}, nil
		},
	})

	// Request<Test> → retry. v9.0.1 C: after an impl edit, the chain
	// re-enters at Compile so the spec + tests regenerate from the new
	// source. That means the retry handler can emit TypeCompiled /
	// TypeCompileError as well as the direct test outcomes — both are
	// declared here for the router's connectivity check.
	r.Register(&router.HandlerFunc{
		In:  TypeRequestTest,
		Out: []string{TypeCompiled, TypeCompileError, TypeTestedPass, TypeTestError, TypeObstacle},
		Run: func(ctx context.Context, in router.TypedValue) (router.TypedValue, error) {
			return runRetry(ctx, d, in, "test")
		},
	})

	// Tested<Pass> → review.
	r.Register(&router.HandlerFunc{
		In:  TypeTestedPass,
		Out: []string{TypeReviewed, TypeReviewFailed, TypeObstacle},
		Run: func(ctx context.Context, in router.TypedValue) (router.TypedValue, error) {
			var p TestedPassPayload
			if err := in.Unmarshal(&p); err != nil {
				return router.TypedValue{}, err
			}
			return runReview(ctx, d, p.ObjectID, 0)
		},
	})

	// ReviewFailed → Request<Review>.
	r.Register(&router.EnrichHandler{
		In:  TypeReviewFailed,
		Out: TypeRequestReview,
		Transform: func(in router.TypedValue) (router.Request, error) {
			var p ReviewFailedPayload
			if err := in.Unmarshal(&p); err != nil {
				return router.Request{}, err
			}
			ctx := map[string]string{"objectId": p.ObjectID, "attempts": fmt.Sprintf("%d", p.Attempts)}
			if len(p.StaticIssues) > 0 {
				ctx["staticIssues"] = strings.Join(p.StaticIssues, "; ")
			}
			if len(p.RuntimeIssues) > 0 {
				ctx["runtimeIssues"] = strings.Join(p.RuntimeIssues, "; ")
			}
			if len(p.ReviewerReasons) > 0 {
				ctx["reviewerReasons"] = strings.Join(p.ReviewerReasons, "; ")
			}
			return router.Request{
				Task: fmt.Sprintf("Object %q must pass typecalc_review with ok=true.", p.ObjectID),
				Context: ctx,
				Guidance: []string{
					"Static issues (e.g. value-space-empty, spec-stale): fix the graph or re-describe.",
					"Runtime issues (port missing, out-of-range, enum violation): fix the impl, then router will re-test.",
					"Reasonableness fail: re-write the impl to genuinely satisfy the intent (do NOT rewrite intent).",
					"If the review issues are structurally infeasible (e.g. harness can't model a side_effect-only function), declare Obstacle.",
				},
				Attempts: p.Attempts,
			}, nil
		},
	})

	// Request<Review> → retry. Same v9.0.1 C re-walk reasoning as
	// Request<Test>: after an impl edit the chain re-enters at
	// Compile so the regenerated spec/tests/review align with the
	// fresh impl source.
	r.Register(&router.HandlerFunc{
		In:  TypeRequestReview,
		Out: []string{TypeCompiled, TypeCompileError, TypeReviewed, TypeReviewFailed, TypeObstacle},
		Run: func(ctx context.Context, in router.TypedValue) (router.TypedValue, error) {
			return runRetry(ctx, d, in, "review")
		},
	})

	// Reviewed → mark confirmed.
	r.Register(&router.HandlerFunc{
		In:  TypeReviewed,
		Out: []string{TypeConfirmed, TypeObstacle},
		Run: func(ctx context.Context, in router.TypedValue) (router.TypedValue, error) {
			var p ReviewedPayload
			if err := in.Unmarshal(&p); err != nil {
				return router.TypedValue{}, err
			}
			if err := d.MarkConfirmed(ctx, p.ObjectID); err != nil {
				return makeObstacle(p.ObjectID, "mark-confirmed failed: "+err.Error(), TypeReviewed), nil
			}
			out, _ := router.NewTypedValue(TypeConfirmed, ConfirmedPayload{ObjectID: p.ObjectID})
			return out, nil
		},
	})

	// Brownfield front stage — registered ONLY when wired (additive;
	// keeps every existing caller/test byte-compatible). StartCharacterize
	// recovers the legacy artifact's behavior into a golden lock, then
	// Characterized feeds it into the existing pipeline via the SAME
	// runCompile shared with StartConfirm — so the recovered contract is
	// verified by the unchanged compile→describe→…→review machine, now
	// guarded by the gate's [method-use-rule].
	if d.Characterize != nil {
		r.Register(&router.HandlerFunc{
			In:  TypeStartCharacterize,
			Out: []string{TypeCharacterized, TypeObstacle},
			Run: func(ctx context.Context, in router.TypedValue) (router.TypedValue, error) {
				var p StartCharacterizePayload
				if err := in.Unmarshal(&p); err != nil {
					return router.TypedValue{}, err
				}
				locked, unlocked, err := d.Characterize(ctx, p.ObjectID)
				if err != nil {
					return makeObstacle(p.ObjectID, "characterize failed: "+err.Error(), TypeStartCharacterize), nil
				}
				out, _ := router.NewTypedValue(TypeCharacterized, CharacterizedPayload{
					ObjectID: p.ObjectID, Locked: locked, Unlocked: unlocked,
				})
				return out, nil
			},
		})
		r.Register(&router.HandlerFunc{
			In:  TypeCharacterized,
			Out: []string{TypeCompiled, TypeCompileError, TypeObstacle},
			Run: func(ctx context.Context, in router.TypedValue) (router.TypedValue, error) {
				var p CharacterizedPayload
				if err := in.Unmarshal(&p); err != nil {
					return router.TypedValue{}, err
				}
				// The golden lock is on disk; verify the (now-locked)
				// artifact through the existing pipeline unchanged.
				return runCompile(ctx, d, p.ObjectID, 0)
			},
		})
	}

	r.RegisterTerminal(TypeConfirmed)
	r.RegisterTerminal(TypeObstacle)

	// Static connectivity check — every declared Output must resolve.
	if orphans := r.Connectivity(); len(orphans) > 0 {
		return nil, fmt.Errorf("typecalcchain: orphan output tags %v — chain is not closed", orphans)
	}
	return r, nil
}

// runCompile is shared by the StartConfirm handler and the Request<Compile>
// retry path. attemptCount=0 means "first run".
func runCompile(ctx context.Context, d Deps, objectID string, attempts int) (router.TypedValue, error) {
	lang, _, ok, code, log, err := d.Compile(ctx, objectID)
	if err != nil {
		return makeObstacle(objectID, "compile invoker error: "+err.Error(), TypeStartConfirm), nil
	}
	if ok {
		out, _ := router.NewTypedValue(TypeCompiled, CompiledPayload{ObjectID: objectID, Lang: lang})
		return out, nil
	}
	out, _ := router.NewTypedValue(TypeCompileError, CompileErrorPayload{
		ObjectID: objectID, Lang: lang, ErrorCode: code, ErrorLog: log, Attempts: attempts + 1,
	})
	return out, nil
}

func runTest(ctx context.Context, d Deps, objectID string, attempts int) (router.TypedValue, error) {
	_, ok, failingCase, expected, actual, log, err := d.Test(ctx, objectID)
	if err != nil {
		return makeObstacle(objectID, "test invoker error: "+err.Error(), TypeSynthesized), nil
	}
	if ok {
		out, _ := router.NewTypedValue(TypeTestedPass, TestedPassPayload{ObjectID: objectID})
		return out, nil
	}
	out, _ := router.NewTypedValue(TypeTestError, TestErrorPayload{
		ObjectID: objectID, FailingCase: failingCase, Expected: expected, Actual: actual, RunnerLog: log, Attempts: attempts + 1,
	})
	return out, nil
}

func runReview(ctx context.Context, d Deps, objectID string, attempts int) (router.TypedValue, error) {
	ok, static, rt, reasons, conf, err := d.Review(ctx, objectID)
	if err != nil {
		return makeObstacle(objectID, "review invoker error: "+err.Error(), TypeTestedPass), nil
	}
	if ok {
		out, _ := router.NewTypedValue(TypeReviewed, ReviewedPayload{ObjectID: objectID, Confidence: conf})
		return out, nil
	}
	out, _ := router.NewTypedValue(TypeReviewFailed, ReviewFailedPayload{
		ObjectID: objectID, StaticIssues: static, RuntimeIssues: rt, ReviewerReasons: reasons, Attempts: attempts + 1,
	})
	return out, nil
}

// runRetry consumes a Request<...> and asks d.FixImpl to either rewrite
// the impl (and re-run the underlying step) OR declare Obstacle.
//
// stage tells us which step we came from ("compile" | "test" | "review")
// so we re-run the right step after a successful impl rewrite.
func runRetry(ctx context.Context, d Deps, in router.TypedValue, stage string) (router.TypedValue, error) {
	var req router.Request
	if err := in.Unmarshal(&req); err != nil {
		return router.TypedValue{}, err
	}
	objectID := req.Context["objectId"]
	if req.Attempts >= maxRetriesFor(in.Meta) {
		return makeObstacle(objectID, fmt.Sprintf("retry budget exhausted at stage=%s after %d attempts; structurally giving up", stage, req.Attempts), in.Type), nil
	}

	allowed := []string{"Uncompiled<Lang<Code>>", "Obstacle<Object,Reason>"}
	prompt := router.FormatRequestForLLM(req, allowed)
	branch, obstacleReason, err := d.FixImpl(ctx, objectID, prompt)
	if err != nil {
		return makeObstacle(objectID, "FixImpl invoker error: "+err.Error(), in.Type), nil
	}
	if branch == "obstacle" {
		return makeObstacle(objectID, obstacleReason, in.Type), nil
	}
	// branch == "retry": impl is rewritten. Re-enter the chain.
	//
	// v9.0.1 C: when the retry comes from test/review (i.e. the agent
	// just edited the impl after a test failure or review issue), the
	// previously-synthesized tests are tied to the OLD spec/source —
	// re-running test directly would either compare against a stale
	// suite or fail with evidence-stale. Routing back through compile
	// makes the router naturally re-flow compile → describe →
	// synthesize → test, refreshing the spec hash and tests at each
	// downstream stage. The attempt counter is preserved so the
	// 5-cycle budget still applies across the whole loop.
	//
	// Compile-stage retries route back to runCompile directly since
	// nothing downstream has run yet.
	switch stage {
	case "compile":
		return runCompile(ctx, d, objectID, req.Attempts)
	case "test", "review":
		return runCompile(ctx, d, objectID, req.Attempts)
	default:
		return makeObstacle(objectID, "unknown retry stage "+stage, in.Type), nil
	}
}

func makeObstacle(objectID, reason, lastType string) router.TypedValue {
	// v9.2 — emits a TypeObstacle typed value the caller can surface to
	// the agent ("I gave up after N retries"), but does NOT persist
	// anything to the bundle. Pre-v9.2 this wrote an Obstacle section
	// that the gate accepted as a waiver-pair escape. With waivers and
	// obstacles removed entirely, the chain's terminal failure is just
	// a failure — the agent must fix the code, not record a paper trail.
	out, _ := router.NewTypedValue(TypeObstacle, ObstaclePayload{
		ObjectID: objectID, Reason: reason, LastType: lastType,
	})
	return out
}

func maxRetriesFor(meta map[string]string) int {
	if meta == nil {
		return DefaultMaxRetries
	}
	// Future hook: per-invocation override via meta key. For now
	// default applies.
	return DefaultMaxRetries
}

func validateDeps(d Deps) error {
	if d.Compile == nil {
		return fmt.Errorf("typecalcchain: Deps.Compile is required")
	}
	if d.Describe == nil {
		return fmt.Errorf("typecalcchain: Deps.Describe is required")
	}
	if d.Synthesize == nil {
		return fmt.Errorf("typecalcchain: Deps.Synthesize is required")
	}
	if d.Test == nil {
		return fmt.Errorf("typecalcchain: Deps.Test is required")
	}
	if d.Smoke == nil {
		return fmt.Errorf("typecalcchain: Deps.Smoke is required (v9.3 HTML branch)")
	}
	if d.Build == nil {
		return fmt.Errorf("typecalcchain: Deps.Build is required (v9.3.1 HTML branch pre-smoke assembly)")
	}
	if d.IsHTMLImpl == nil {
		return fmt.Errorf("typecalcchain: Deps.IsHTMLImpl is required (v9.3 HTML branch)")
	}
	if d.Review == nil {
		return fmt.Errorf("typecalcchain: Deps.Review is required")
	}
	if d.FixImpl == nil {
		return fmt.Errorf("typecalcchain: Deps.FixImpl is required (LLM-driven retry handler)")
	}
	if d.MarkConfirmed == nil {
		return fmt.Errorf("typecalcchain: Deps.MarkConfirmed is required")
	}
	return nil
}

// _ silences "imported and not used" if graph is removed later; the
// import documents the chain's dependency on the canonical graph
// package for resolving impl paths via Object.Impl.
var _ = graph.NewGraph
