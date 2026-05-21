package chains

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/router"
)

// defaultStepTimeout caps a single LLM-backed dep call (Compile / Describe
// / Synthesize / Test / Smoke / Review / FixImpl / Characterize / Build).
// Sized so a healthy step finishes within budget but a hung LLM stream can
// no longer silently consume the outer 1h cap.
//
// PB-30 batch #3 (2026-05-21) closed with 4/5 instances cap-killed mid-step
// after 17–27 min of silence following the last visible tool call. Without
// per-step deadlines the chain happily waited for an LLM stream that had
// long since stopped emitting tokens. The watchdog turns those silences
// into a stage-named Obstacle the outer Router can act on.
//
// Override via env KCPOS_CHAIN_STEP_TIMEOUT_SEC (clamped to [60, 1800]).
const defaultStepTimeout = 8 * time.Minute

// stepTimeoutDuration reads the env override or returns the default.
// Production recommends ≥60s — anything lower risks racing a healthy LLM
// stream. Tests use ≥1s to exercise the timeout path quickly. >30min
// defeats the purpose of the cap, so we clamp it.
func stepTimeoutDuration() time.Duration {
	v := os.Getenv("KCPOS_CHAIN_STEP_TIMEOUT_SEC")
	if v == "" {
		return defaultStepTimeout
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return defaultStepTimeout
	}
	if n > 1800 {
		n = 1800
	}
	return time.Duration(n) * time.Second
}

// stageContext wraps the parent ctx with the per-step deadline. The
// returned cancel MUST run when the dep call completes so the timer
// goroutine doesn't leak.
func stageContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, stepTimeoutDuration())
}

// isStageTimeout returns true when err looks like our per-step deadline
// firing (as opposed to e.g. a structured LLM transport error). Used to
// pick a stage-named Obstacle reason instead of a generic "<stage> failed".
//
// errors.Is covers the canonical context.DeadlineExceeded; the string
// check covers LLM transport wrappers that re-format the error before
// returning (transport/client.go wraps stream EOF + ctx-cancel into a
// "context deadline exceeded while reading body" message).
func isStageTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "context deadline exceeded") ||
		strings.Contains(s, "context canceled")
}

// stageTimeoutReason composes the Obstacle reason for a per-step deadline.
// Names the stage + the configured timeout so the agent / outer Router can
// see exactly which inner step ran the budget out.
func stageTimeoutReason(stage string) string {
	d := stepTimeoutDuration()
	return fmt.Sprintf(
		"chain stage %q exceeded the per-step inactivity timeout (%v). "+
			"The LLM-backed dep did not return within the budget — likely a "+
			"stream stall, an infinite retry, or a step that genuinely needs "+
			"longer (raise KCPOS_CHAIN_STEP_TIMEOUT_SEC if the latter). "+
			"The outer Router can now characterize / inspect / restart "+
			"instead of waiting silently.", stage, d,
	)
}

// hasReferenceProbe reports whether a ./probe shell wrapper / binary
// exists in the chain's current working directory. Presence of a probe
// is the signal that this run is brownfield (black-box rebuild), so
// the chain's TestError handler must NOT enrich back to the LLM
// (KonceptOS_implementation_plan.md §1.2 L158).
func hasReferenceProbe() bool {
	if fi, err := os.Stat("probe"); err == nil && !fi.IsDir() {
		return true
	}
	return false
}

// mkFailingCaseDetail formats the test failure context for the
// brownfield Obstacle reason so the agent sees what diverged.
func mkFailingCaseDetail(p TestErrorPayload) string {
	var b strings.Builder
	if p.FailingCase != "" {
		b.WriteString("\n\nFailing case:\n" + truncCap(p.FailingCase, 800))
	}
	if p.Expected != "" {
		b.WriteString("\n\nExpected (from synthesized test):\n" + truncCap(p.Expected, 400))
	}
	if p.Actual != "" {
		b.WriteString("\n\nActual (from impl):\n" + truncCap(p.Actual, 400))
	}
	if p.RunnerLog != "" {
		b.WriteString("\n\nRunner log tail:\n" + truncCap(p.RunnerLog, 600))
	}
	return b.String()
}

func truncCap(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

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
			sctx, cancel := stageContext(ctx)
			defer cancel()
			desc, err := d.Describe(sctx, p.ObjectID)
			if err != nil {
				if isStageTimeout(err) {
					return makeObstacle(p.ObjectID, stageTimeoutReason("describe"), TypeCompiled), nil
				}
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
				bctx, bcancel := stageContext(ctx)
				_, berr := d.Build(bctx)
				bcancel()
				if berr != nil {
					if isStageTimeout(berr) {
						return makeObstacle(p.ObjectID, stageTimeoutReason("build"), TypeDescribed), nil
					}
					return makeObstacle(p.ObjectID, "session_build (pre-smoke) failed: "+berr.Error(), TypeDescribed), nil
				}
				sctx, scancel := stageContext(ctx)
				defer scancel()
				ok, summary, serr := d.Smoke(sctx, p.ObjectID)
				if serr != nil {
					if isStageTimeout(serr) {
						return makeObstacle(p.ObjectID, stageTimeoutReason("smoke"), TypeDescribed), nil
					}
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
			synthCtx, synthCancel := stageContext(ctx)
			n, err := d.Synthesize(synthCtx, p.ObjectID)
			synthCancel()
			if err != nil {
				if isStageTimeout(err) {
					return makeObstacle(p.ObjectID, stageTimeoutReason("synthesize"), TypeDescribed), nil
				}
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

	// TestError → either Obstacle (brownfield: ./probe exists) or
	// Request<Test> (greenfield: enrich + LLM-rewrite loop, legacy behavior).
	//
	// Handler 1.2 brownfield branch (KonceptOS_implementation_plan.md
	// §1.2 L117 / L158 / L167 / L178): when a black-box reference
	// (./probe) is available, TestError MUST NOT be looped back to the
	// LLM as "edit impl and retry". The original design ("TestError
	// 不打回 LLM——进入 brownfield 流程") forbids that path because the
	// real diagnosis ("代码错还是测试错") needs reference-behavior
	// observation, not LLM speculation. Pre-2026-05-21 this branch was
	// missing — chain enriched + retried on every TestError, which is
	// why PB-30 batch #2's confirm_object calls silently spun for
	// 24-42 minutes inside the LLM rewrite cycle without progress.
	//
	// This handler now:
	//   - Greenfield (no ./probe): emit Request<Test> + LLM enrich
	//     guidance (existing v9 behavior; safe for SPEC-derived
	//     greenfield projects where synthesized tests ARE the oracle)
	//   - Brownfield (./probe exists): emit Obstacle with explicit
	//     "do NOT retry via LLM rewrite" guidance; the agent's outer
	//     loop sees the Obstacle and per H_confirm_one's prompt
	//     calls the `characterize` agent tool to diagnose whether
	//     the impl is wrong or the synthesized tests are wrong.
	r.Register(&router.HandlerFunc{
		In:  TypeTestError,
		Out: []string{TypeRequestTest, TypeObstacle},
		Run: func(ctx context.Context, in router.TypedValue) (router.TypedValue, error) {
			var p TestErrorPayload
			if err := in.Unmarshal(&p); err != nil {
				return router.TypedValue{}, err
			}
			// Brownfield mode: a reference ./probe in cwd means this
			// is a black-box rebuild task. Honor L158 — emit Obstacle
			// without enriching back to the LLM.
			if hasReferenceProbe() {
				reason := fmt.Sprintf(
					"TestError in brownfield mode (./probe reference detected). "+
						"Per Handler 1.2 design (KonceptOS_implementation_plan.md §1.2 L158: "+
						"\"TestError 不打回 LLM——进入 brownfield 流程\"), the chain does NOT "+
						"auto-retry via LLM impl rewrite. Diagnose with the `characterize` "+
						"agent tool to determine: (a) impl matches intent and synthesized tests "+
						"were wrong → regenerate via typecalc_synthesize_tests; "+
						"(b) impl diverges from intent → fix impl with reference behavior as oracle.",
					) +
					mkFailingCaseDetail(p)
				return makeObstacle(p.ObjectID, reason, TypeTestError), nil
			}
			// Greenfield fallback: enrich → LLM rewrite (v9 behavior).
			reqCtx := map[string]string{"objectId": p.ObjectID, "attempts": fmt.Sprintf("%d", p.Attempts)}
			if p.FailingCase != "" {
				reqCtx["failingCase"] = p.FailingCase
			}
			if p.Expected != "" {
				reqCtx["expected"] = p.Expected
			}
			if p.Actual != "" {
				reqCtx["actual"] = p.Actual
			}
			if p.RunnerLog != "" {
				reqCtx["runnerLogTail"] = p.RunnerLog
			}
			req := router.Request{
				Task: fmt.Sprintf("Test object %q until typecalc_test returns Tested<Pass>.", p.ObjectID),
				Context: reqCtx,
				Guidance: []string{
					"Inspect the failing case: what did the test expect, and what did the impl produce?",
					"If the impl is wrong: edit it to match the test's expected behavior.",
					"If the test is wrong (e.g. it assumes a port that doesn't exist): regenerate via typecalc_synthesize_tests after re-running typecalc_describe.",
					"Do NOT silently change the contract (intent) — surface that as Obstacle instead.",
					"After your edits, the router re-runs typecalc_test automatically.",
				},
				Attempts: p.Attempts,
			}
			payload, _ := json.Marshal(req)
			return router.TypedValue{Type: TypeRequestTest, Content: payload}, nil
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
				sctx, cancel := stageContext(ctx)
				locked, unlocked, err := d.Characterize(sctx, p.ObjectID)
				cancel()
				if err != nil {
					if isStageTimeout(err) {
						return makeObstacle(p.ObjectID, stageTimeoutReason("characterize"), TypeStartCharacterize), nil
					}
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
	sctx, cancel := stageContext(ctx)
	defer cancel()
	lang, _, ok, code, log, err := d.Compile(sctx, objectID)
	if err != nil {
		if isStageTimeout(err) {
			return makeObstacle(objectID, stageTimeoutReason("compile"), TypeStartConfirm), nil
		}
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
	sctx, cancel := stageContext(ctx)
	defer cancel()
	_, ok, failingCase, expected, actual, log, err := d.Test(sctx, objectID)
	if err != nil {
		if isStageTimeout(err) {
			return makeObstacle(objectID, stageTimeoutReason("test"), TypeSynthesized), nil
		}
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
	sctx, cancel := stageContext(ctx)
	defer cancel()
	ok, static, rt, reasons, conf, err := d.Review(sctx, objectID)
	if err != nil {
		if isStageTimeout(err) {
			return makeObstacle(objectID, stageTimeoutReason("review"), TypeTestedPass), nil
		}
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
	sctx, cancel := stageContext(ctx)
	branch, obstacleReason, err := d.FixImpl(sctx, objectID, prompt)
	cancel()
	if err != nil {
		if isStageTimeout(err) {
			return makeObstacle(objectID, stageTimeoutReason("fix_impl"), in.Type), nil
		}
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
