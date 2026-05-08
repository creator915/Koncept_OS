package typecalctools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/creator915/Koncept_OS/internal/graph"
	"github.com/creator915/Koncept_OS/internal/llm"
	"github.com/creator915/Koncept_OS/internal/typecalc"
	"github.com/creator915/Koncept_OS/internal/typecalc/feedback"
	"github.com/creator915/Koncept_OS/internal/typecalc/harness"
	"github.com/creator915/Koncept_OS/internal/typecalc/lang"
	"github.com/creator915/Koncept_OS/internal/typecalc/probe"
)

// Evidence-write logic and HTML/JS detection live in
// internal/typecalc/evidence.go and are reused here as
// typecalc.RecordEvidence / typecalc.DetectEffectiveLang /
// typecalc.HasInlineScript. The previous file-local copies were
// deduped into the core package after the tools/ subpackage split.

// typecalcCompileTool wraps lang.CompileLanguageInvoker as an agent
// tool. The agent passes a language tag and the source payload; the tool
// runs the real toolchain (go vet / tsc / node --check / py_compile) and
// returns either:
//
//	OK: state advanced from Uncompiled to Compiled.
//	ERR: a compile error envelope with errorCode + errorLog the agent can
//	     fold into its next-attempt prompt (rule compiler_in_the_loop).
//
// Per §7.1 the agent should keep retrying with the enriched error log
// until it gets OK or chooses to escalate as Obstacle. The retry cap
// itself is enforced by the agent loop's iteration counter, not here —
// the tool is single-shot.
// typecalcCompileTool is the id-only redesign (post-2026-05-08): the
// tool no longer takes `lang` / `payload` string arguments. It reads
// the impl from `graph.Objects[id].Impl` and detects the language from
// the file extension.
//
// Why: when typecalc_compile took agent-supplied strings, the agent
// could compile a payload that wasn't actually on disk and pass the
// resulting "ok=true" evidence as proof for the on-disk impl. The id-
// only design closes that loophole — there is exactly one source for
// the impl text (the file the graph points at), and the tool reads it.
func typecalcCompileTool() llm.Tool {
	return llm.Tool{
		Concurrent: true, // subprocess + per-id evidence write; safe to parallelize
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name: "typecalc_compile",
				Description: "Compile the impl of a graph object. Reads `graph.Objects[<id>].Impl` from K/graph.json, loads the file, and runs the language-specific syntax/type check. Records `kind=compile` evidence on success.\n\nNote: write_file already auto-runs typecalc_compile when the path matches an object's impl, so explicit calls here are usually redundant. Use this only when you suspect the on-disk file diverged from the last write_file (e.g. you ran a `bash` command that touched the file) or when `graph_merge_object` set impl to a path you wrote earlier in a separate turn.\n\nReturns Compiled<Code> on success, or CompileError<...> on failure.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object_id": map[string]interface{}{"type": "string", "description": "Graph object id. The tool reads its impl path from K/graph.json and compiles that file."},
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
				return "", fmt.Errorf("object %q has no impl path set", objectID)
			}
			implBody, err := os.ReadFile(*obj.Impl)
			if err != nil {
				return "", fmt.Errorf("read impl %s: %w", *obj.Impl, err)
			}
			langTag := typecalc.LangFromExt(extOf(*obj.Impl))
			if langTag == typecalc.LangNone {
				return "", fmt.Errorf("cannot infer language from impl extension %q", *obj.Impl)
			}
			tv := typecalc.New(typecalc.KindCode, string(implBody)).
				WithState(typecalc.StateUncompiled).
				WithLang(langTag)
			env := &typecalc.RuleEnv{WorkDir: "."}
			out, err := lang.CompileLanguageInvoker(ctx, env, tv)
			if err != nil {
				return "", err
			}
			rendered, _ := renderTypedValue(out)
			implHash := typecalc.HashSource(string(implBody))
			if out.State == typecalc.StateCompiled {
				effectiveLang := string(typecalc.DetectEffectiveLang(string(implBody), langTag))
				if recErr := typecalc.RecordEvidenceFull(objectID, "compile", effectiveLang, true, rendered, implHash); recErr != nil {
					return "", recErr
				}
			} else if out.Kind == typecalc.KindInsufficient {
				effectiveLang := string(typecalc.DetectEffectiveLang(string(implBody), langTag))
				if recErr := typecalc.RecordEvidenceFull(objectID, "insufficient", effectiveLang, false, out.Payload, implHash); recErr != nil {
					return "", recErr
				}
			}
			return rendered, nil
		},
	}
}

// extOf is defined in synthesize.go; reuse from there.

// filepathIsAbs avoids pulling in path/filepath just to check absolute.
func filepathIsAbs(p string) bool {
	if p == "" {
		return false
	}
	if p[0] == '/' {
		return true
	}
	// Windows drive — defensive but unused on macOS/Linux targets.
	if len(p) >= 3 && p[1] == ':' && (p[2] == '/' || p[2] == '\\') {
		return true
	}
	return false
}

// typecalcTestTool is the id-only redesign: the tool reads BOTH the
// impl (from graph) AND the test code (from <id>.tests.json) directly.
// No `code` / `tests` / `lang` arguments — the agent has no string-arg
// affordance to substitute either.
//
// This is the architectural closure of Fix A's leak: previously the
// tool tried to enforce "use the synthesized tests" by checking for
// .tests.json and silently swapping; an agent could rm the file to
// fall through to its own string. With both inputs sourced from disk
// canonically, that bypass doesn't exist — the only way to change
// what tests run is to call typecalc_synthesize_tests again (which
// rewrites .tests.json from the current spec), and the only way to
// change the impl is to write_file.
//
// Prerequisites enforced by the tool:
//   - graph.Objects[id].Impl set + file exists + readable
//   - <id>.tests.json exists (call typecalc_synthesize_tests first)
func typecalcTestTool() llm.Tool {
	return llm.Tool{
		Concurrent: true, // subprocess + per-id trace + per-id evidence; needs unique scratch dirs
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name: "typecalc_test",
				Description: "Run the spec-derived tests against the impl of a graph object. Reads impl from `graph.Objects[<id>].Impl` and tests from `.kcpos/typecalc-evidence/<id>.tests.json` (output of typecalc_synthesize_tests). Records `kind=test` evidence with the runner's combined log on success.\n\nThis is the canonical contract verifier: tests come from spec, impl comes from disk, neither is agent-controllable as a string. To update tests, call typecalc_synthesize_tests (regenerates from the current spec). To update impl, call write_file.\n\nReturns Tested<Code,Pass> on success, TestError<...> on failure. The runner's stdout/stderr is captured into the evidence log so typecalc_review can read it without an explicit string handoff.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object_id": map[string]interface{}{"type": "string", "description": "Graph object id. The tool reads impl from the graph and synthesized tests from disk."},
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
				return "", fmt.Errorf("object %q has no impl path set", objectID)
			}
			implBody, err := os.ReadFile(*obj.Impl)
			if err != nil {
				return "", fmt.Errorf("read impl %s: %w", *obj.Impl, err)
			}
			t, ok := typecalc.ReadTests(objectID)
			if !ok || (len(t.Cases) == 0 && len(t.TestCode) == 0) {
				return "", fmt.Errorf("no synthesized tests for %s — call typecalc_synthesize_tests object_id=%q first", objectID, objectID)
			}
			langTag := typecalc.LangFromExt(extOf(*obj.Impl))
			if langTag == typecalc.LangNone {
				return "", fmt.Errorf("cannot infer language from impl extension %q", *obj.Impl)
			}
			// Schema-driven (harness) path: if the synthesizer produced
			// structured Cases AND a harness exists for this language,
			// render the harness with the cases and run that. The LLM
			// never sees the test runner code; "appendTrace BEFORE
			// assert" is enforced by the harness.
			//
			// IMPORTANT: pass the ABSOLUTE impl path. The test runner
			// executes in a scratch directory, so a path like
			// "index.html" relative to the project root will not be
			// findable. Same reason we resolve the runtime trace
			// directory below to an absolute path.
			testSource := t.TestCode
			usingHarness := false
			if len(t.Cases) > 0 {
				cwd, _ := os.Getwd()
				absImpl := *obj.Impl
				if !filepathIsAbs(absImpl) && cwd != "" {
					absImpl = cwd + string(os.PathSeparator) + absImpl
				}
				absTrace := cwd + string(os.PathSeparator) + typecalc.RuntimeTracePath(objectID)
				rendered, ok := harness.Render(harness.RenderInputs{
					Tests:           t,
					InputPorts:      obj.Consumes,
					OutputPorts:     obj.Produces,
					ImplPath:        absImpl,
					TracePath:       absTrace,
					PortObservation: obj.PortObservation,
				})
				if ok {
					testSource = rendered
					usingHarness = true
				}
			}
			if testSource == "" {
				return "", fmt.Errorf("no runnable test source for %s (cases empty AND testCode empty)", objectID)
			}
			_ = usingHarness
			compiled := typecalc.New(typecalc.KindCode, string(implBody)).
				WithState(typecalc.StateCompiled).
				WithLang(langTag)
			suite := typecalc.New(typecalc.KindTestSuite, testSource).WithLang(langTag)
			env := &typecalc.RuleEnv{WorkDir: "."}
			out, err := lang.TestRunInvoker(ctx, env, compiled, suite)
			if err != nil {
				return "", err
			}
			rendered, _ := renderTypedValue(out)
			implHash := typecalc.HashSource(string(implBody))
			if out.State == typecalc.StateTestedPass {
				effectiveLang := string(typecalc.DetectEffectiveLang(string(implBody), langTag))
				if recErr := typecalc.RecordEvidenceFull(objectID, "test", effectiveLang, true, rendered, implHash); recErr != nil {
					return "", recErr
				}
			} else if out.Kind == typecalc.KindInsufficient {
				effectiveLang := string(typecalc.DetectEffectiveLang(string(implBody), langTag))
				if recErr := typecalc.RecordEvidenceFull(objectID, "insufficient", effectiveLang, false, out.Payload, implHash); recErr != nil {
					return "", recErr
				}
			}
			return rendered, nil
		},
	}
}

// typecalcProbePlanTool generates a ProbePlan from the current graph. The
// agent invokes this when an integration test fails (rule plan_probes,
// §3) — the plan is the system's mechanical contribution to fault
// localization; the agent then routes through it (rule execute_probe +
// rule locate_fault) to find the offending module.
func typecalcProbePlanTool() llm.Tool {
	return llm.Tool{
		Concurrent: true, // pure read of K/graph.json
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "typecalc_probe_plan",
				Description: "Generate a ProbePlan from the current K/graph.json topology — the ordered list of intermediate attributes to observe for fault localization (§3 plan_probes). Use after an integration-test failure.",
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			g, err := graph.LoadOrInit(graph.DefaultPath)
			if err != nil {
				return "", err
			}
			plan, err := probe.PlanFromGraph(g)
			if err != nil {
				return "", err
			}
			return renderTypedValue(probe.NewPlan(plan))
		},
	}
}

// typecalcApplyFeedbackTool applies a typed user-feedback verdict to the
// graph: ValueAdjust adjusts the attribute's valueSpace, LawMissing
// appends to the attribute's laws. In both cases the tool returns the
// list of downstream objects whose Status should be reset (the
// AffectedModules rule output from §3).
//
// DesignChange and CannotReproduce don't mutate the graph — the tool
// echoes them back so the conversation has a structured record, and the
// agent can decide whether to spawn a re-design subagent or report back
// to the user.
func typecalcApplyFeedbackTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name: "typecalc_apply_feedback",
				Description: "Apply a typed user-feedback verdict (§3 receive_feedback / apply_value_adjust / apply_law_missing). The verdict comes from your own analysis of the user feedback — pick one of: ValueAdjust, LawMissing, DesignChange, CannotReproduce.\n\nValueAdjust and LawMissing mutate K/graph.json; DesignChange and CannotReproduce are recorded but not auto-applied.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"verdict":  map[string]interface{}{"type": "string", "description": "One of: ValueAdjust, LawMissing, DesignChange, CannotReproduce."},
						"attrPath": map[string]interface{}{"type": "string", "description": "Required for ValueAdjust/LawMissing. Dotted attribute path, e.g. 'player_state.position.y'."},
						"newValue": map[string]interface{}{"type": "string", "description": "ValueAdjust only. JSON-encoded new value to merge into the attribute's valueSpace."},
						"newLaw":   map[string]interface{}{"type": "string", "description": "LawMissing only. The new law / invariant text."},
						"reason":   map[string]interface{}{"type": "string", "description": "DesignChange / CannotReproduce. Explanation."},
					},
					"required": []string{"verdict"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			verdict, _ := args["verdict"].(string)
			switch feedback.Verdict(verdict) {
			case feedback.VerdictValueAdjust:
				attrPath, _ := args["attrPath"].(string)
				newVal, _ := args["newValue"].(string)
				if attrPath == "" || newVal == "" {
					return "", fmt.Errorf("ValueAdjust requires attrPath and newValue")
				}
				return applyFeedbackValueAdjust(attrPath, newVal)
			case feedback.VerdictLawMissing:
				attrPath, _ := args["attrPath"].(string)
				newLaw, _ := args["newLaw"].(string)
				if attrPath == "" || newLaw == "" {
					return "", fmt.Errorf("LawMissing requires attrPath and newLaw")
				}
				return applyFeedbackLawMissing(attrPath, newLaw)
			case feedback.VerdictDesignChange:
				reason, _ := args["reason"].(string)
				return renderTypedValue(feedback.NewDesignChange(reason))
			case feedback.VerdictCannotReproduce:
				reason, _ := args["reason"].(string)
				return renderTypedValue(feedback.NewCannotReproduce(reason))
			default:
				return "", fmt.Errorf("unknown verdict %q", verdict)
			}
		},
	}
}

func applyFeedbackValueAdjust(attrPath, rawNewValue string) (string, error) {
	var v any
	if err := json.Unmarshal([]byte(rawNewValue), &v); err != nil {
		return "", fmt.Errorf("newValue must be JSON: %w", err)
	}
	d := &feedback.ValueAdjustDetail{AttrPath: attrPath}
	d.NewValue, _ = json.Marshal(v)
	var affected *feedback.AffectedModules
	err := mutateGraph(func(g *graph.Graph) error {
		var err error
		affected, err = feedback.ApplyValueAdjust(g, d)
		return err
	})
	if err != nil {
		return "", err
	}
	out := struct {
		Verdict  string                    `json:"verdict"`
		Detail   *feedback.ValueAdjustDetail `json:"detail"`
		Affected *feedback.AffectedModules `json:"affected"`
	}{Verdict: string(feedback.VerdictValueAdjust), Detail: d, Affected: affected}
	raw, _ := json.MarshalIndent(out, "", "  ")
	return string(raw), nil
}

func applyFeedbackLawMissing(attrPath, newLaw string) (string, error) {
	d := &feedback.LawMissingDetail{AttrPath: attrPath, NewLaw: newLaw}
	var affected *feedback.AffectedModules
	err := mutateGraph(func(g *graph.Graph) error {
		var err error
		affected, err = feedback.ApplyLawMissing(g, d)
		return err
	})
	if err != nil {
		return "", err
	}
	out := struct {
		Verdict  string                   `json:"verdict"`
		Detail   *feedback.LawMissingDetail `json:"detail"`
		Affected *feedback.AffectedModules `json:"affected"`
	}{Verdict: string(feedback.VerdictLawMissing), Detail: d, Affected: affected}
	raw, _ := json.MarshalIndent(out, "", "  ")
	return string(raw), nil
}

// renderTypedValue is a small helper that marshals a typed value into a
// human + machine-readable form for tool results. We emit JSON wrapped in
// a banner line so the agent can both grep for the kind tag and parse
// the body.
func renderTypedValue(tv *typecalc.TypedValue) (string, error) {
	if tv == nil {
		return "", fmt.Errorf("nil typed value")
	}
	body, err := json.MarshalIndent(tv, "", "  ")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("TYPE: %s\n%s", tv.Tag(), string(body)), nil
}
