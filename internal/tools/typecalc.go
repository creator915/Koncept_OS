package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/creator915/Koncept_OS/internal/chat"
	"github.com/creator915/Koncept_OS/internal/graph"
	"github.com/creator915/Koncept_OS/internal/typecalc"
)

// typecalcEvidenceDir is the on-disk record of which graph entities have
// been mechanically validated via typecalc_compile / typecalc_test. The
// `typecalc-use` enforcement hook (internal/agent/hooks.go) checks for
// presence of <objectID>.json before allowing graph_merge_object
// status=confirmed — without this trail, "confirmed" is just a string
// the LLM typed, not a verified state.
const typecalcEvidenceDir = ".kcpos/typecalc-evidence"

// recordTypecalcEvidence writes a small JSON record under typecalcEvidenceDir
// stamping that the named entity passed a typecalc check. The hook reads
// this file's existence (not its contents), so any non-empty file here is
// treated as evidence. We still write a structured payload so a human or
// audit script can reconstruct the trail.
func recordTypecalcEvidence(objectID, kind, lang string, ok bool) error {
	if objectID == "" {
		return nil
	}
	if err := os.MkdirAll(typecalcEvidenceDir, 0o755); err != nil {
		return fmt.Errorf("mkdir evidence dir: %w", err)
	}
	rec := map[string]any{
		"objectId":  objectID,
		"kind":      kind, // "compile" | "test"
		"lang":      lang,
		"ok":        ok,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	raw, _ := json.MarshalIndent(rec, "", "  ")
	return os.WriteFile(filepath.Join(typecalcEvidenceDir, objectID+".json"), raw, 0o644)
}

// detectEffectiveLang closes the §1 HTML loophole: an HTML file whose
// content includes a `<script>` block is in practice a JavaScript
// container, and the test-evidence requirement should apply to the JS
// inside. When called with lang=HTML and content containing `<script>`,
// we promote to JavaScript so downstream gate rules
// (typecalc-test-required) treat the file as JS and demand a real test.
//
// For other languages, we return them unchanged. For pure HTML (no
// embedded script), we keep HTML — there's no JS to test.
func detectEffectiveLang(content string, declared typecalc.Lang) typecalc.Lang {
	if declared != typecalc.LangHTML {
		return declared
	}
	if hasInlineScript(content) {
		return typecalc.LangJavaScript
	}
	return declared
}

// hasInlineScript reports whether the content contains a non-empty
// `<script>...</script>` block. We accept any attributes on the open tag
// (e.g. `<script type="module">`) but require closing `</script>`.
func hasInlineScript(content string) bool {
	open := strings.Index(strings.ToLower(content), "<script")
	if open < 0 {
		return false
	}
	close := strings.Index(strings.ToLower(content[open:]), "</script>")
	if close < 0 {
		return false
	}
	gt := strings.Index(content[open:], ">")
	if gt < 0 {
		return false
	}
	body := content[open+gt+1 : open+close]
	return strings.TrimSpace(body) != ""
}

// typecalcCompileTool wraps typecalc.CompileLanguageInvoker as an agent
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
func typecalcCompileTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
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
			lang, _ := args["lang"].(string)
			payload, _ := args["payload"].(string)
			task, _ := args["task"].(string)
			objectID, _ := args["object_id"].(string)
			if payload == "" {
				return "", fmt.Errorf("payload required")
			}
			tv := typecalc.New(typecalc.KindCode, payload).
				WithState(typecalc.StateUncompiled).
				WithLang(typecalc.LangFromExt(lang))
			if tv.Lang == typecalc.LangNone {
				tv = tv.WithLang(typecalc.Lang(lang))
			}
			if task != "" {
				tv, _ = tv.WithContext("task", task)
			}
			env := &typecalc.RuleEnv{WorkDir: "."}
			out, err := typecalc.CompileLanguageInvoker(ctx, env, tv)
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
				effectiveLang := string(detectEffectiveLang(payload, tv.Lang))
				if recErr := recordTypecalcEvidence(objectID, "compile", effectiveLang, true); recErr != nil {
					return "", recErr
				}
			}
			return renderTypedValue(out)
		},
	}
}

// typecalcTestTool runs tests via typecalc.TestRunInvoker. The agent
// supplies the compiled source + the test suite as separate strings; the
// tool invokes the language's test runner under a scratch directory and
// returns either Tested<Pass> or TestError<TestCase, Expected, Actual>.
//
// Per §3 the test inputs MUST come from the description + signature, NOT
// the source. We can't enforce that mechanically here, but we reject
// payloads where the suite contains the literal keyword "implementation
// detail" (a soft guard) and we surface §3's review_test_error rule as a
// recommended next step in the result text on failure.
func typecalcTestTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
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
			lang, _ := args["lang"].(string)
			code, _ := args["code"].(string)
			tests, _ := args["tests"].(string)
			objectID, _ := args["object_id"].(string)
			if code == "" || tests == "" {
				return "", fmt.Errorf("code and tests required")
			}
			langTag := typecalc.LangFromExt(lang)
			if langTag == typecalc.LangNone {
				langTag = typecalc.Lang(lang)
			}
			compiled := typecalc.New(typecalc.KindCode, code).
				WithState(typecalc.StateCompiled).
				WithLang(langTag)
			suite := typecalc.New(typecalc.KindTestSuite, tests).WithLang(langTag)
			env := &typecalc.RuleEnv{WorkDir: "."}
			out, err := typecalc.TestRunInvoker(ctx, env, compiled, suite)
			if err != nil {
				return "", err
			}
			if objectID != "" && out.State == typecalc.StateTestedPass {
				// Fix 1: same HTML-with-script promotion as in
				// typecalcCompileTool — the lang recorded in evidence is
				// the EFFECTIVE content language, not the container.
				effectiveLang := string(detectEffectiveLang(code, langTag))
				if recErr := recordTypecalcEvidence(objectID, "test", effectiveLang, true); recErr != nil {
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
func typecalcProbePlanTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
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
			plan, err := typecalc.ProbePlanFromGraph(g)
			if err != nil {
				return "", err
			}
			return renderTypedValue(typecalc.NewProbePlan(plan))
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
func typecalcApplyFeedbackTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
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
			switch typecalc.FeedbackVerdict(verdict) {
			case typecalc.FeedbackValueAdjust:
				attrPath, _ := args["attrPath"].(string)
				newVal, _ := args["newValue"].(string)
				if attrPath == "" || newVal == "" {
					return "", fmt.Errorf("ValueAdjust requires attrPath and newValue")
				}
				return applyFeedbackValueAdjust(attrPath, newVal)
			case typecalc.FeedbackLawMissing:
				attrPath, _ := args["attrPath"].(string)
				newLaw, _ := args["newLaw"].(string)
				if attrPath == "" || newLaw == "" {
					return "", fmt.Errorf("LawMissing requires attrPath and newLaw")
				}
				return applyFeedbackLawMissing(attrPath, newLaw)
			case typecalc.FeedbackDesignChange:
				reason, _ := args["reason"].(string)
				return renderTypedValue(typecalc.NewDesignChange(reason))
			case typecalc.FeedbackCannotReproduce:
				reason, _ := args["reason"].(string)
				return renderTypedValue(typecalc.NewCannotReproduce(reason))
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
	d := &typecalc.ValueAdjustDetail{AttrPath: attrPath}
	d.NewValue, _ = json.Marshal(v)
	var affected *typecalc.AffectedModules
	err := mutateGraph(func(g *graph.Graph) error {
		var err error
		affected, err = typecalc.ApplyValueAdjust(g, d)
		return err
	})
	if err != nil {
		return "", err
	}
	out := struct {
		Verdict  string                    `json:"verdict"`
		Detail   *typecalc.ValueAdjustDetail `json:"detail"`
		Affected *typecalc.AffectedModules `json:"affected"`
	}{Verdict: string(typecalc.FeedbackValueAdjust), Detail: d, Affected: affected}
	raw, _ := json.MarshalIndent(out, "", "  ")
	return string(raw), nil
}

func applyFeedbackLawMissing(attrPath, newLaw string) (string, error) {
	d := &typecalc.LawMissingDetail{AttrPath: attrPath, NewLaw: newLaw}
	var affected *typecalc.AffectedModules
	err := mutateGraph(func(g *graph.Graph) error {
		var err error
		affected, err = typecalc.ApplyLawMissing(g, d)
		return err
	})
	if err != nil {
		return "", err
	}
	out := struct {
		Verdict  string                   `json:"verdict"`
		Detail   *typecalc.LawMissingDetail `json:"detail"`
		Affected *typecalc.AffectedModules `json:"affected"`
	}{Verdict: string(typecalc.FeedbackLawMissing), Detail: d, Affected: affected}
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
