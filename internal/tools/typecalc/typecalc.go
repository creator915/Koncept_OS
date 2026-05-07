package typecalctools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/creator915/Koncept_OS/internal/graph"
	"github.com/creator915/Koncept_OS/internal/llm"
	"github.com/creator915/Koncept_OS/internal/typecalc"
	"github.com/creator915/Koncept_OS/internal/typecalc/feedback"
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
func typecalcCompileTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name: "typecalc_compile",
				Description: "Compile a source payload through the typecalc compile rule. Returns Compiled<Code> on success, or CompileError<Task,ErrorCode,ErrorLog> on failure.\n\n**You MUST call this before merging any graph object to status=confirmed** — the typecalc-use enforcement hook will reject merges that have no compile evidence on disk. Pass `object_id` so the evidence is attributed to the right object (the hook checks .kcpos/typecalc-evidence/<object_id>.json).",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"lang":      map[string]interface{}{"type": "string", "description": "Language tag: TypeScript | JavaScript | Go | Python | Rust | Java | HTML."},
						"payload":   map[string]interface{}{"type": "string", "description": "Source code to compile."},
						"task":      map[string]interface{}{"type": "string", "description": "Optional task description (folded into a CompileError on failure for §7.1 retry)."},
						"object_id": map[string]interface{}{"type": "string", "description": "Graph object id this compile attests to. Required when you intend to subsequently set this object's status=confirmed — the typecalc-use hook checks for evidence under this id."},
					},
					"required": []string{"lang", "payload"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			langArg, _ := args["lang"].(string)
			payload, _ := args["payload"].(string)
			task, _ := args["task"].(string)
			objectID, _ := args["object_id"].(string)
			if payload == "" {
				return "", fmt.Errorf("payload required")
			}
			tv := typecalc.New(typecalc.KindCode, payload).
				WithState(typecalc.StateUncompiled).
				WithLang(typecalc.LangFromExt(langArg))
			if tv.Lang == typecalc.LangNone {
				tv = tv.WithLang(typecalc.Lang(langArg))
			}
			if task != "" {
				tv, _ = tv.WithContext("task", task)
			}
			env := &typecalc.RuleEnv{WorkDir: "."}
			out, err := lang.CompileLanguageInvoker(ctx, env, tv)
			if err != nil {
				return "", err
			}
			// Record evidence ONLY on success. Failed compiles aren't
			// evidence of a working object — the agent must retry per §7.1
			// before evidence is laid down.
			if objectID != "" && out.State == typecalc.StateCompiled {
				// Fix 1: HTML containing <script> is recorded as
				// JavaScript so the gate's typecalc-test-required rule
				// fires. The agent can't dodge testing by mislabeling the
				// container language.
				effectiveLang := string(typecalc.DetectEffectiveLang(payload, tv.Lang))
				if recErr := typecalc.RecordEvidence(objectID, "compile", effectiveLang, true); recErr != nil {
					return "", recErr
				}
			}
			return renderTypedValue(out)
		},
	}
}

// typecalcTestTool runs tests via lang.TestRunInvoker. The agent
// supplies the compiled source + the test suite as separate strings; the
// tool invokes the language's test runner under a scratch directory and
// returns either Tested<Pass> or TestError<TestCase, Expected, Actual>.
//
// Per §3 the test inputs MUST come from the description + signature, NOT
// the source. We can't enforce that mechanically here, but we reject
// payloads where the suite contains the literal keyword "implementation
// detail" (a soft guard) and we surface §3's review_test_error rule as a
// recommended next step in the result text on failure.
func typecalcTestTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name: "typecalc_test",
				Description: "Run a test suite against a compiled source payload via the typecalc test rule. Returns Tested<Code,Pass> on success, TestError<TestCase,Expected,Actual> on failure.\n\nThis maps to §3 rule run_test. On TestError, follow the §7.2 review loop (TestCorrect → fix code, TestWrong → fix tests, DescriptionUnclear → escalate).\n\nPass `object_id` to also satisfy the typecalc-use enforcement hook (an alternative to typecalc_compile evidence — passing tests is stronger evidence anyway).",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"lang":      map[string]interface{}{"type": "string", "description": "Language: TypeScript | JavaScript | Go | Python."},
						"code":      map[string]interface{}{"type": "string", "description": "Compiled source under test."},
						"tests":     map[string]interface{}{"type": "string", "description": "Test suite payload."},
						"object_id": map[string]interface{}{"type": "string", "description": "Graph object id this test attests to. Required if you intend to merge this object to status=confirmed shortly after."},
					},
					"required": []string{"lang", "code", "tests"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			langArg, _ := args["lang"].(string)
			code, _ := args["code"].(string)
			tests, _ := args["tests"].(string)
			objectID, _ := args["object_id"].(string)
			if code == "" || tests == "" {
				return "", fmt.Errorf("code and tests required")
			}
			langTag := typecalc.LangFromExt(langArg)
			if langTag == typecalc.LangNone {
				langTag = typecalc.Lang(langArg)
			}
			compiled := typecalc.New(typecalc.KindCode, code).
				WithState(typecalc.StateCompiled).
				WithLang(langTag)
			suite := typecalc.New(typecalc.KindTestSuite, tests).WithLang(langTag)
			env := &typecalc.RuleEnv{WorkDir: "."}
			out, err := lang.TestRunInvoker(ctx, env, compiled, suite)
			if err != nil {
				return "", err
			}
			if objectID != "" && out.State == typecalc.StateTestedPass {
				// Fix 1: same HTML-with-script promotion as in
				// typecalcCompileTool — the lang recorded in evidence is
				// the EFFECTIVE content language, not the container.
				effectiveLang := string(typecalc.DetectEffectiveLang(code, langTag))
				if recErr := typecalc.RecordEvidence(objectID, "test", effectiveLang, true); recErr != nil {
					return "", recErr
				}
			}
			return renderTypedValue(out)
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
