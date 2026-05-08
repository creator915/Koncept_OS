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
			implBody, err := os.ReadFile(*obj.Impl)
			if err != nil {
				return "", fmt.Errorf("read impl %s: %w", *obj.Impl, err)
			}
			implHash := typecalc.HashSource(string(implBody))
			// Hash cache: if a fresh spec exists for this exact impl, the
			// description is still valid — skip the LLM call. The agent
			// often re-runs describe redundantly during fix loops; this
			// is the cheapest way to drop those costs.
			if existing, ok := typecalc.ReadSpec(objectID); ok && existing.SourceHash == implHash {
				return fmt.Sprintf(
					"described %s [cache hit, no LLM call] — kept %s\n\n--- description ---\n%s",
					objectID,
					typecalc.SpecEvidencePath(objectID),
					existing.Description,
				), nil
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
			// D4: iteration cap. Block the review BEFORE running it if
			// the agent has already used CycleCap retries. The agent
			// must either change approach or emit typecalc_obstacle.
			if existing, ok := typecalc.ReadCycle(objectID); ok && existing.Count >= typecalc.CycleCap {
				return "", fmt.Errorf(
					"iteration cap reached: %d review cycles already consumed for %s. The cycle counter only resets when review verdict ok=true; persistent failure means the structural fix is elsewhere. Either: (a) restructure the impl / graph and try a fresh approach, or (b) call typecalc_obstacle object_id=%q reason=<...> to surface this as a human-decision point and unblock the gate flow",
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
			// Reasonableness only attempts when BOTH static and runtime
			// checks pass — running the LLM judge against a known-broken
			// trace would just amplify noise.
			canReason := len(issues) == 0 && len(runtimeIssues) == 0
			if canReason {
				verdict, err = typecalc.ReviewReasonableness(ctx, typecalc.ReviewInputs{
					ObjectID:    objectID,
					Intent:      obj.Intent,
					Description: specDesc,
					Signature:   string(defBody),
					Impl:        string(implBody),
					TestCode:    testCode,
					TestLog:     testLog,
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
			} else {
				reason := "static or runtime check produced issues — fix them before reasonableness review can run"
				if len(issues) == 0 && len(runtimeIssues) > 0 {
					reason = "runtime port-signal check produced issues — fix them before reasonableness review can run"
				}
				verdict = typecalc.ReviewVerdict{
					Verdict:    "fail",
					Reasons:    []string{reason},
					Confidence: 1.0,
				}
			}

			testsHash := ""
			if t, ok := typecalc.ReadTests(objectID); ok {
				testsHash = typecalc.HashSource(t.TestCode)
			}
			ok2 := canReason && verdict.Verdict == "pass"
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

			// D4: cycle accounting. ok=true resets; otherwise increment.
			if ok2 {
				_ = typecalc.ResetCycle(objectID)
			} else {
				_, _ = typecalc.IncrementCycle(objectID)
			}

			return renderReviewResult(rec), nil
		},
	}
}

func resolveCwd(cwd, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cwd, path)
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
