package typecalctools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/creator915/Koncept_OS/internal/graph"
	"github.com/creator915/Koncept_OS/internal/llm"
	"github.com/creator915/Koncept_OS/internal/typecalc"
)

// typecalcDescribeTool wraps typecalc.Describe as an agent tool. The
// agent passes an object_id; the tool reads the object's def + impl off
// disk, asks the LLM to write a precise description, and persists it as
// kind=spec evidence. The description is the second source of truth
// alongside the original `intent` field — together they form the spec
// the reviewer (typecalc_review) will judge against.
//
// Why this is a separate tool rather than a sub-step of typecalc_review:
// re-describing on every review wastes LLM tokens and produces
// non-deterministic descriptions. The agent should describe once
// (after each non-trivial impl change), then review against that frozen
// description.
func typecalcDescribeTool() llm.Tool {
	return llm.Tool{
		Concurrent: true, // LLM call + per-id <id>.spec.json write; safe to parallelize
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name: "typecalc_describe",
				Description: "Read an object's def + impl files and have an LLM produce a precise description of what the implementation does. Persists kind=spec evidence at .kcpos/typecalc-evidence/<id>.spec.json.\n\nThis is the FIRST step of the review pipeline (describe → review → confirmed). The description complements the original `intent` field: intent is what was wanted, description is what was built. Both feed into the reasonableness reviewer.\n\nRe-run after any non-trivial impl change — the description's SourceHash is keyed to the impl content, and a stale description will fail the static check during review.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object_id": map[string]interface{}{
							"type":        "string",
							"description": "Graph object id whose impl should be described.",
						},
					},
					"required": []string{"object_id"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			objectID, _ := args["object_id"].(string)
			if objectID == "" {
				return "", fmt.Errorf("object_id required")
			}
			g, err := graph.LoadOrInit(graph.DefaultPath)
			if err != nil {
				return "", err
			}
			obj, ok := g.Objects[objectID]
			if !ok {
				return "", fmt.Errorf("object %q not found in K/graph.json", objectID)
			}
			if obj.Impl == nil || *obj.Impl == "" {
				return "", fmt.Errorf("object %q has no impl path set — set impl before describing", objectID)
			}
			// v9.0.5: describe per-object source (the fragment) when set
			// rather than the shared deliverable (which is empty / not-yet-
			// built during child confirm_object). For multi-file projects
			// where ImplFragment is unset, this falls back to obj.Impl.
			describeTarget := *obj.Impl
			if obj.ImplFragment != nil && *obj.ImplFragment != "" {
				describeTarget = *obj.ImplFragment
			}
			implBody, err := os.ReadFile(describeTarget)
			if err != nil {
				return "", fmt.Errorf("read impl %s: %w", describeTarget, err)
			}
			implHash := typecalc.HashSource(string(implBody))
			symbolHash := computeSymbolHash(string(implBody), describeTarget, obj.ImplSymbol, objectID)
			// Hash cache: if a fresh spec exists for this exact impl, the
			// description is still valid — skip the LLM call. The agent
			// often re-runs describe redundantly during fix loops; this
			// is the cheapest way to drop those costs.
			// v9.0.2: prefer SymbolHash match (per-object) over SourceHash
			// match — single-file-impl projects regenerate the description
			// only when THIS object's function body actually changed.
			if existing, ok := typecalc.ReadSpec(objectID); ok {
				if symbolHash != "" && existing.SymbolHash != "" && existing.SymbolHash == symbolHash {
					return fmt.Sprintf(
						"described %s [cache hit on symbolHash, no LLM call] — kept %s\n\n--- description ---\n%s",
						objectID,
						typecalc.SpecEvidencePath(objectID),
						existing.Description,
					), nil
				}
				if existing.SourceHash == implHash {
					return fmt.Sprintf(
						"described %s [cache hit on sourceHash, no LLM call] — kept %s\n\n--- description ---\n%s",
						objectID,
						typecalc.SpecEvidencePath(objectID),
						existing.Description,
					), nil
				}
			}
			defBody := []byte{}
			if obj.Def != "" {
				defBody, _ = os.ReadFile(obj.Def) // tolerate missing def
			}
			desc, err := typecalc.Describe(ctx, typecalc.DescribeInputs{
				ObjectID:  objectID,
				Intent:    obj.Intent,
				Signature: string(defBody),
				Impl:      string(implBody),
			})
			if err != nil {
				return "", err
			}
			rec := &typecalc.SpecEvidence{
				ObjectID:    objectID,
				Description: desc,
				SourceHash:  implHash,
				SymbolHash:  symbolHash,
			}
			if err := typecalc.WriteSpec(rec); err != nil {
				return "", fmt.Errorf("persist spec evidence: %w", err)
			}
			return fmt.Sprintf(
				"described %s — wrote %s\n\n--- description ---\n%s",
				objectID,
				typecalc.SpecEvidencePath(objectID),
				desc,
			), nil
		},
	}
}

// typecalcReviewTool runs the three-tier acceptance pipeline:
//
//  1. Static check       — graph-structure "what must be wrong" filter.
//  2. Runtime port check — verifies the trace produced by typecalc_test
//     against the graph's port declarations and valueSpace.
//  3. Reasonableness     — LLM judgement of intent + description + tests
//                          + impl + runner log.
//
// All three inputs (test code, runner log, runtime trace) are read from
// the canonical evidence locations on disk — typecalc_synthesize_tests
// writes <id>.tests.json, typecalc_test writes <id>.json with the log
// embedded, and the synthesized tests append .kcpos/typecalc-runtime/
// <id>.json. The agent has NO string-arg affordance to substitute any
// of them. To change anything, regenerate via the canonical writer.
//
// All three tiers must pass for kind=accepted ok=true. The gate's
// [accepted-evidence-required] rule blocks root-finish without it.
func typecalcReviewTool() llm.Tool {
	return llm.Tool{
		Concurrent: true, // LLM call + per-id <id>.accepted.json write
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name: "typecalc_review",
				Description: "Run the three-tier acceptance check (static + runtime + LLM reasonableness) and persist kind=accepted evidence.\n\nReads everything from disk:\n  - intent: K/graph.json (graph.Objects[id].Intent)\n  - signature: K/defs/<id>.* (graph.Objects[id].Def)\n  - impl: graph.Objects[id].Impl\n  - description: <id>.spec.json (output of typecalc_describe)\n  - test code: <id>.tests.json (output of typecalc_synthesize_tests)\n  - test runner log: <id>.json kind=test (output of typecalc_test)\n  - runtime trace: .kcpos/typecalc-runtime/<id>.json (appended by tests)\n\nPrerequisites: typecalc_describe + typecalc_synthesize_tests + typecalc_test all run with the same object_id. If any source is missing, the static or runtime tier surfaces it and the LLM tier is skipped.\n\nReturns the verdict + all issue lists. Required before graph_merge_object status=confirmed (the gate refuses root-finish without ok=true).",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object_id": map[string]interface{}{
							"type":        "string",
							"description": "Graph object id under review.",
						},
					},
					"required": []string{"object_id"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			objectID, _ := args["object_id"].(string)
			if objectID == "" {
				return "", fmt.Errorf("object_id required")
			}
			// D4: iteration cap. Compute the current "impl key" — a
			// hash that summarizes the source state under judgment.
			// If the impl or portObservation has changed since the
			// last cycle was incremented, the prior failures were
			// against a different artifact and shouldn't tax the
			// fresh attempt: clear the counter before checking.
			implKeyForReset := computeImplKey(objectID)
			_ = typecalc.MaybeResetCycleOnImplChange(objectID, implKeyForReset)
			if existing, ok := typecalc.ReadCycle(objectID); ok && existing.Count >= typecalc.CycleCap {
				return "", fmt.Errorf(
					"iteration cap reached: %d review cycles already consumed for %s. The cycle counter only resets when (a) review verdict ok=true, (b) the issue set strictly shrinks (progress detected), or (c) the impl / portObservation changed since the previous cycle. Persistent failure with no progress and no source change usually means the structural fix is elsewhere. Either: change the impl/graph and try a fresh approach, or call typecalc_obstacle object_id=%q reason=<...> to surface this as a human-decision point and unblock the gate flow",
					existing.Count, objectID, objectID)
			}
			// Read test code + runner log from disk (no string args from agent).
			testCode := ""
			if t, ok := typecalc.ReadTests(objectID); ok {
				testCode = t.TestCode
			}
			testLog := ""
			if ev, ok := typecalc.ReadEvidence(objectID); ok {
				testLog = ev.Log
			}
			cwd, _ := os.Getwd()
			g, err := graph.LoadOrInit(graph.DefaultPath)
			if err != nil {
				return "", err
			}
			obj, ok := g.Objects[objectID]
			if !ok {
				return "", fmt.Errorf("object %q not found in K/graph.json", objectID)
			}

			// 1. Static check (graph-structure layer — what must be wrong).
			issues := typecalc.StaticCheck(cwd, g, objectID)

			// 1.5. Runtime port-signal check (operational layer — observed
			//      port presence, value ranges, temporal causality vs the
			//      graph's declarations). This is the user-design 'standard
			//      1' implemented at runtime: read the trace synthesized
			//      tests appended to .kcpos/typecalc-runtime/<id>.json and
			//      surface only what is mechanically wrong.
			runtimeIssues := typecalc.RuntimeCheck(g, objectID)

			// 2. Reasonableness — only attempted when static check passed,
			//    since a stale spec or missing impl would corrupt the
			//    judgement. If static failed, we still persist accepted
			//    evidence with OK=false so the agent gets actionable
			//    feedback in one round-trip.
			var verdict typecalc.ReviewVerdict
			implBody, defBody := []byte{}, []byte{}
			if obj.Impl != nil && *obj.Impl != "" {
				implBody, _ = os.ReadFile(resolveCwd(cwd, *obj.Impl))
			}
			if obj.Def != "" {
				defBody, _ = os.ReadFile(resolveCwd(cwd, obj.Def))
			}
			spec, _ := typecalc.ReadSpec(objectID)
			specDesc := ""
			specHash := ""
			if spec != nil {
				specDesc = spec.Description
				specHash = spec.SourceHash
			}
			// Reasonableness review — 2026-05-09 v8.6: previously
			// short-circuited to a fixed "static/runtime issues exist"
			// fail when issues != []. That made the gate's
			// accepted-evidence-required layer impossible to satisfy
			// even when obstacle+waiver excused the issues (v8.5
			// batch: pong-01/04 stuck on this). Now we ALWAYS run
			// the LLM judge so the verdict reflects code reasonableness
			// independently of structural issues. The issue lists are
			// surfaced to the LLM as context (so it can weigh whether
			// the failures are real defects or known harness/test
			// noise), and to the agent via the issue rendering — but
			// the verdict itself is no longer hostage to them. The
			// gate still combines acc.OK with the obstacle/waiver
			// pair before deciding finish-eligibility.
			cleanRun := len(issues) == 0 && len(runtimeIssues) == 0
			verdict, err = typecalc.ReviewReasonableness(ctx, typecalc.ReviewInputs{
				ObjectID:    objectID,
				Intent:      obj.Intent,
				Description: specDesc,
				Signature:   string(defBody),
				Impl:        string(implBody),
				TestCode:    testCode,
				TestLog:     renderTestLogForReview(testLog),
			})
			if err != nil {
				// Don't lose the static-check effort on review error —
				// persist a fail record with the error as the reason.
				verdict = typecalc.ReviewVerdict{
					Verdict:    "fail",
					Reasons:    []string{fmt.Sprintf("review invocation error: %v", err)},
					Confidence: 0.0,
				}
			}

			testsHash := ""
			if t, ok := typecalc.ReadTests(objectID); ok {
				testsHash = typecalc.HashSource(t.TestCode)
			}
			// ok2 collapses to true only when BOTH (a) reasonableness
			// verdict says pass AND (b) there were no static/runtime
			// issues. That preserves the strong-signal acc.OK=true
			// semantic; cases where the LLM judges "code is fine but
			// trace has flapping" go through obstacle+waiver instead.
			ok2 := cleanRun && verdict.Verdict == "pass"
			rec := &typecalc.AcceptedEvidence{
				ObjectID:       objectID,
				OK:             ok2,
				StaticIssues:   issues,
				RuntimeIssues:  runtimeIssues,
				Reasonableness: verdict,
				SourceHash:     typecalc.HashSource(string(implBody)),
				SpecHash:       specHash,
				TestsHash:      testsHash,
			}
			if err := typecalc.WriteAccepted(rec); err != nil {
				return "", fmt.Errorf("persist accepted evidence: %w", err)
			}

			// D4: cycle accounting. ok=true resets; otherwise increment
			// with the issue rule names so progress detection (strict-
			// subset shrinkage) can hold the count steady when the
			// agent is genuinely converging. The implKey persists so
			// the next review can detect a source change.
			if ok2 {
				_ = typecalc.ResetCycle(objectID)
			} else {
				_, _ = typecalc.IncrementCycleWithIssues(objectID, collectIssueRules(issues, runtimeIssues, verdict), implKeyForReset)
			}

			return renderReviewResult(rec), nil
		},
	}
}

// renderTestLogForReview prepares the test runner log for the
// reasonableness reviewer. v8.8: the reviewer's job is semantic-only
// ("does impl satisfy intent?"), not test-mechanic judgment. So we
// keep the log itself (the LLM may glean useful signal) but no
// longer paste in issue lists / trace summaries — those used to
// dominate the prompt and dragged the LLM into judging testability
// (pong-05 v8.7: "Implementation is an HTML script, not a module
// exporting updatePhysics as required"). The system prompt now
// explicitly forbids that class of reasoning; this helper keeps
// the prompt narrow.
//
// When the log is empty we return "(no test runner output — judge
// semantics from impl + intent alone)" so the LLM doesn't try to
// fabricate test-side reasoning.
func renderTestLogForReview(testLog string) string {
	if strings.TrimSpace(testLog) == "" {
		return "(no test runner output — judge semantics from impl + intent alone)"
	}
	return testLog
}

func resolveCwd(cwd, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cwd, path)
}

// computeImplKey hashes the source state under judgment so the
// cycle counter can detect "the artifact has changed since last
// review." Includes impl-file content + portObservation map (the
// two things synthesizer / harness behavior depends on). Returns
// "" if either piece is unreadable, which signals the caller to
// skip the source-change reset (defensive: never wrongly clear).
func computeImplKey(objectID string) string {
	g, err := graph.LoadOrInit(graph.DefaultPath)
	if err != nil {
		return ""
	}
	obj, ok := g.Objects[objectID]
	if !ok || obj.Impl == nil || *obj.Impl == "" {
		return ""
	}
	cwd, _ := os.Getwd()
	implBody, err := os.ReadFile(resolveCwd(cwd, *obj.Impl))
	if err != nil {
		return ""
	}
	// Stable serialization of portObservation: the JSON marshaller
	// emits map keys in sorted order, so this is deterministic.
	poJSON, _ := json.Marshal(obj.PortObservation)
	return typecalc.HashSource(string(implBody) + "\x00" + string(poJSON))
}

// collectIssueRules pulls just the rule-name (Code) from each
// static / runtime issue plus a "reasonableness" pseudo-rule when
// the LLM verdict failed. This is the input to progress detection
// — comparing rule sets across cycles avoids over-counting on
// flapping issue messages while still catching genuine regressions.
func collectIssueRules(static []typecalc.StaticIssue, runtime []typecalc.StaticIssue, verdict typecalc.ReviewVerdict) []string {
	out := make([]string, 0, len(static)+len(runtime)+1)
	for _, iss := range static {
		out = append(out, iss.Code)
	}
	for _, iss := range runtime {
		out = append(out, iss.Code)
	}
	if verdict.Verdict != "" && verdict.Verdict != "pass" {
		out = append(out, "reasonableness")
	}
	return out
}

func renderReviewResult(rec *typecalc.AcceptedEvidence) string {
	var b strings.Builder
	verdict := "FAIL"
	if rec.OK {
		verdict = "PASS"
	}
	fmt.Fprintf(&b, "review %s: %s\n", rec.ObjectID, verdict)
	fmt.Fprintf(&b, "evidence: %s\n\n", typecalc.AcceptedEvidencePath(rec.ObjectID))

	fmt.Fprintf(&b, "static check: %d issue(s)\n", len(rec.StaticIssues))
	for _, iss := range rec.StaticIssues {
		fmt.Fprintf(&b, "  [%s] %s — %s\n", iss.Code, iss.Where, iss.Message)
	}
	fmt.Fprintln(&b)

	fmt.Fprintf(&b, "runtime port-signal check: %d issue(s)\n", len(rec.RuntimeIssues))
	for _, iss := range rec.RuntimeIssues {
		fmt.Fprintf(&b, "  [%s] %s — %s\n", iss.Code, iss.Where, iss.Message)
	}
	fmt.Fprintln(&b)

	fmt.Fprintf(&b, "reasonableness: %s (confidence %.2f)\n", rec.Reasonableness.Verdict, rec.Reasonableness.Confidence)
	for _, r := range rec.Reasonableness.Reasons {
		fmt.Fprintf(&b, "  - %s\n", r)
	}

	if !rec.OK {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "next steps: address the issues above and re-run typecalc_review. The gate will not allow root-finish until ok=true.")
	}

	// Echo the JSON for callers that want to grep the verdict mechanically.
	raw, _ := json.MarshalIndent(rec, "", "  ")
	fmt.Fprintf(&b, "\n--- raw evidence ---\n%s\n", string(raw))
	return b.String()
}
