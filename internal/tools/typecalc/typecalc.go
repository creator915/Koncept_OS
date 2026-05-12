package typecalctools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/creator915/Koncept_OS/internal/graph"
	"github.com/creator915/Koncept_OS/internal/llm"
	"github.com/creator915/Koncept_OS/internal/typecalc"
	"github.com/creator915/Koncept_OS/internal/typecalc/feedback"
	"github.com/creator915/Koncept_OS/internal/typecalc/harness"
	"github.com/creator915/Koncept_OS/internal/typecalc/lang"
	"github.com/creator915/Koncept_OS/internal/typecalc/probe"
)

// buildIsolatedTestBundle (v9.0.5) wraps a fragment .js file in a
// minimal HTML scaffold so the existing harness HTML extraction path
// can load it. This lets a child agent test its own fragment in
// isolation, without depending on session_build to have assembled the
// final deliverable.
//
// Returns (path, cleanup, err). The caller MUST defer cleanup() so
// the temp file is removed after the test runner finishes.
func buildIsolatedTestBundle(objectID, fragmentPath string) (string, func(), error) {
	body, err := os.ReadFile(fragmentPath)
	if err != nil {
		return "", func() {}, fmt.Errorf("read fragment %s: %w", fragmentPath, err)
	}
	tmp, err := os.CreateTemp("", "kcpos-test-bundle-"+objectID+"-*.html")
	if err != nil {
		return "", func() {}, err
	}
	defer tmp.Close()
	html := "<!doctype html><html><body>\n<script>\n" + string(body) + "\n</script>\n</body></html>\n"
	if _, err := tmp.WriteString(html); err != nil {
		_ = os.Remove(tmp.Name())
		return "", func() {}, err
	}
	path := tmp.Name()
	cleanup := func() { _ = os.Remove(path) }
	return path, cleanup, nil
}

// _ silences unused-import detection for filepath when no other site
// in this file uses it. Used inside buildIsolatedTestBundle in some
// branches.
var _ = filepath.Separator

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
			// v9.0.5: when implFragment is set, the per-object code lives
			// there (e.g. K/frags/Foo.js), and obj.Impl is the assembled
			// deliverable (e.g. index.html) which won't have THIS object's
			// code until session_build runs in R2. Compile against the
			// fragment so child sessions get meaningful evidence during
			// their confirm_object loop, not against an empty / not-yet-
			// built deliverable.
			compileTarget := *obj.Impl
			if obj.ImplFragment != nil && *obj.ImplFragment != "" {
				compileTarget = *obj.ImplFragment
			}
			implBody, err := os.ReadFile(compileTarget)
			if err != nil {
				return "", fmt.Errorf("read impl %s: %w", compileTarget, err)
			}
			langTag := typecalc.LangFromExt(extOf(compileTarget))
			if langTag == typecalc.LangNone {
				return "", fmt.Errorf("cannot infer language from impl extension %q", compileTarget)
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
			symbolHash := computeSymbolHash(string(implBody), compileTarget, obj.ImplSymbol, objectID)
			if out.State == typecalc.StateCompiled {
				effectiveLang := string(typecalc.DetectEffectiveLang(string(implBody), langTag))
				if recErr := typecalc.RecordEvidenceWithSymbol(objectID, "compile", effectiveLang, true, rendered, implHash, symbolHash); recErr != nil {
					return "", recErr
				}
			} else if out.Kind == typecalc.KindInsufficient {
				effectiveLang := string(typecalc.DetectEffectiveLang(string(implBody), langTag))
				if recErr := typecalc.RecordEvidenceWithSymbol(objectID, "insufficient", effectiveLang, false, out.Payload, implHash, symbolHash); recErr != nil {
					return "", recErr
				}
			}
			return rendered, nil
		},
	}
}

// computeSymbolHash returns the per-object fragment hash (v9.0.2). When
// implSymbol is unset on the graph object we default to objectID — the
// harness uses the same fallback. Returns "" when fragment extraction
// fails (non-HTML impl, symbol not present) so callers can let the
// bundle keep whatever SymbolHash it already had instead of overwriting
// with a noisy default.
func computeSymbolHash(implContent, implPath, implSymbol, objectID string) string {
	symbol := implSymbol
	if symbol == "" {
		symbol = objectID
	}
	if h, ok := typecalc.SymbolFragmentHash(implContent, implPath, symbol); ok {
		return h
	}
	return ""
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

// appendUnique appends each x to dst only if it is not already present.
// Used to merge Produces and Mutates into the harness OUTPUT_PORTS list
// without double-counting ports that legitimately appear in both.
func appendUnique(dst []string, xs ...string) []string {
	seen := make(map[string]bool, len(dst))
	for _, d := range dst {
		seen[d] = true
	}
	for _, x := range xs {
		if !seen[x] {
			dst = append(dst, x)
			seen[x] = true
		}
	}
	return dst
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
			// v9.0.5: when implFragment is set on an HTML-deliverable
			// project, build an isolated test bundle from the fragment
			// so the harness exercises just THIS object's code, not the
			// not-yet-built deliverable. The bundle is a minimal
			// <script>-wrapped HTML so the existing HTML extraction
			// path in harness.go works without changes.
			testTarget := *obj.Impl
			cleanupTempHTML := func() {}
			if obj.ImplFragment != nil && *obj.ImplFragment != "" && strings.HasSuffix(strings.ToLower(*obj.Impl), ".html") {
				bundlePath, cleanup, ferr := buildIsolatedTestBundle(objectID, *obj.ImplFragment)
				if ferr != nil {
					return "", fmt.Errorf("build isolated test bundle for %s: %w", objectID, ferr)
				}
				testTarget = bundlePath
				cleanupTempHTML = cleanup
			}
			defer cleanupTempHTML()
			implBody, err := os.ReadFile(testTarget)
			if err != nil {
				return "", fmt.Errorf("read impl %s: %w", testTarget, err)
			}
			t, ok := typecalc.ReadTests(objectID)
			if !ok || (len(t.Cases) == 0 && len(t.TestCode) == 0) {
				return "", fmt.Errorf("no synthesized tests for %s — call typecalc_synthesize_tests object_id=%q first", objectID, objectID)
			}
			langTag := typecalc.LangFromExt(extOf(testTarget))
			if langTag == typecalc.LangNone {
				return "", fmt.Errorf("cannot infer language from impl extension %q", testTarget)
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
				absImpl := testTarget
				if !filepathIsAbs(absImpl) && cwd != "" {
					absImpl = cwd + string(os.PathSeparator) + absImpl
				}
				absTrace := cwd + string(os.PathSeparator) + typecalc.RuntimeTracePath(objectID)
				// 2026-05-11 v8.7 — OUTPUT_PORTS must include Mutates,
				// not just Produces. v8.6 batch (pong-01, pong-05) had
				// objects modelled as mutates=[ball_data, score, ...]
				// with portObservation: {ball_data: "return.ball", ...}.
				// The function correctly returned the new value but
				// because OutputPorts was Produces-only (which was []),
				// the harness's snapshotPorts(OUTPUT_PORTS=[], ...) ran
				// over an empty list and recorded outputs={} on every
				// call. Downstream runtime-check then reported
				// runtime-output-missing for every mutates port. Agents
				// either spiraled into self-blame (pong-01: writing
				// useless consume edges, then context-bombing on a 6.8MB
				// grep) or mass-waivered with confabulated obstacle
				// reasons (pong-05). The mutates-pattern carve-out in
				// runtime_check.go v8.6 (mutates+consumes overlap via
				// observed outputs) was inert until this fix because
				// outputs was always empty for mutates ports.
				outputPorts := append([]string{}, obj.Produces...)
				outputPorts = appendUnique(outputPorts, obj.Mutates...)
				rendered, ok := harness.Render(harness.RenderInputs{
					Tests:           t,
					InputPorts:      obj.Consumes,
					OutputPorts:     outputPorts,
					ImplPath:        absImpl,
					TracePath:       absTrace,
					PortObservation: obj.PortObservation,
					ImplSymbol:      obj.ImplSymbol,
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
			// v9.0.5: hash the FRAGMENT (per-object source) when present,
			// not the temp test bundle (whose <script>-wrap noise would
			// drift on every test run).
			hashTarget := testTarget
			hashContent := string(implBody)
			if obj.ImplFragment != nil && *obj.ImplFragment != "" {
				if fbody, err := os.ReadFile(*obj.ImplFragment); err == nil {
					hashTarget = *obj.ImplFragment
					hashContent = string(fbody)
				}
			}
			symbolHash := computeSymbolHash(hashContent, hashTarget, obj.ImplSymbol, objectID)
			effectiveLang := string(typecalc.DetectEffectiveLang(string(implBody), langTag))
			// 2026-05-09 v8.6 — emit a short, greppable status line to
			// stderr so test results are observable in the agent log.
			// Previously only the tool CALL was banner-logged; the tool
			// RESULT lived only in the transcript JSON, making it
			// difficult to triage runs from the log alone (v8.5 audit
			// noted "Tested<Pass>" was hard to track for some agents).
			emitTestStatusLine(objectID, out)
			if out.State == typecalc.StateTestedPass {
				if recErr := typecalc.RecordEvidenceWithSymbol(objectID, "test", effectiveLang, true, rendered, implHash, symbolHash); recErr != nil {
					return "", recErr
				}
			} else if out.Kind == typecalc.KindInsufficient {
				if recErr := typecalc.RecordEvidenceWithSymbol(objectID, "insufficient", effectiveLang, false, out.Payload, implHash, symbolHash); recErr != nil {
					return "", recErr
				}
			} else if out.Kind == typecalc.KindTestError {
				// 2026-05-09 v7→v8: previously a TestError left no
				// kind=test evidence, only the upstream kind=compile.
				// The gate then reported "only compile evidence" even
				// though the test runner clearly executed (just with
				// some assertions failing). Now we always record the
				// attempt with ok=false so the gate can distinguish
				// "untested" from "tested but partially failed". Agent
				// can still target Tested<Pass> for full-green objects;
				// the TestError record just stops the misleading gate
				// language and lets reasonableness review weigh in on
				// borderline cases (6/8 passing is signal).
				if recErr := typecalc.RecordEvidenceWithSymbol(objectID, "test", effectiveLang, false, out.Payload, implHash, symbolHash); recErr != nil {
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
// emitTestStatusLine writes a one-line ANSI-decorated marker to
// stderr describing the typecalc_test outcome. Format is greppable
// (the status word is always one of TestedPass / TestError /
// Insufficient / Other) and mirrors the » tool-call banner style.
// Without this, the actual test verdict was only visible by reading
// the transcript JSON — every log triage required jq + transcript
// path, which is fragile in parallel runs.
func emitTestStatusLine(objectID string, out *typecalc.TypedValue) {
	if out == nil {
		return
	}
	status := "Other"
	color := "\x1b[36m" // cyan default
	if out.State == typecalc.StateTestedPass {
		status = "TestedPass"
		color = "\x1b[32m" // green
	} else if out.Kind == typecalc.KindTestError {
		status = "TestError"
		color = "\x1b[31m" // red
	} else if out.Kind == typecalc.KindInsufficient {
		status = "Insufficient"
		color = "\x1b[33m" // yellow
	}
	// Inline timestamp (avoid agent-package import → cycle).
	// Matches agent/log.go's Stamp() format for log consistency.
	ts := ""
	if os.Getenv("KCPOS_NO_TIMESTAMP") == "" {
		ts = "[" + time.Now().Format("15:04:05") + "] "
	}
	fmt.Fprintf(os.Stderr, "%s    %s↳ typecalc_test %s: %s\x1b[0m\n", ts, color, objectID, status)
}

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
