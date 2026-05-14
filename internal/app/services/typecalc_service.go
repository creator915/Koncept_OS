package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
	"github.com/creator915/Koncept_OS/internal/typecalc/feedback"
	"github.com/creator915/Koncept_OS/internal/typecalc/harness"
	"github.com/creator915/Koncept_OS/internal/typecalc/lang"
	"github.com/creator915/Koncept_OS/internal/typecalc/probe"
)

// v9.0.5 buildIsolatedTestBundle (HTML+fragment → temp HTML test wrapper)
// was removed in v9.3. HTML deliverables are now verified by runtime_smoke
// (not the vm.Script harness), so the wrapper has no callers — see
// typecalc_test's HTML hard-error guard and confirm_object's HTML branch.

// Evidence-write logic and HTML/JS detection live in
// internal/typecalc/evidence.go and are reused here as
// core.RecordEvidence / core.DetectEffectiveLang /
// core.HasInlineScript. The previous file-local copies were
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
func typecalcCompileTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: true, // subprocess + per-id evidence write; safe to parallelize
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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
			g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
			if err != nil {
				return "", err
			}
			obj, ok := g.Objects[objectID]
			if !ok {
				return "", fmt.Errorf("object %q not found in K/graph.json", objectID)
			}
			if obj.Impl == nil && obj.ImplContent == "" {
				return "", fmt.Errorf("object %q has no impl path or implContent set", objectID)
			}
			var implBody string
			var compileTarget string
			// v10: prefer ImplContent directly from graph (source of truth).
			// Fall back to file-based reading for backward compatibility.
			if obj.ImplContent != "" {
				implBody = obj.ImplContent
				compileTarget = "(implContent)" // for error messages
			} else {
				// v9.0.5: when implFragment is set, the per-object code lives
				// there (e.g. K/frags/Foo.js), and obj.Impl is the assembled
				// deliverable (e.g. index.html) which won't have THIS object's
				// code until session_build runs in R2. Compile against the
				// fragment so child sessions get meaningful evidence during
				// their confirm_object loop, not against an empty / not-yet-
				// built deliverable.
				compileTarget = *obj.Impl
				if obj.ImplFragment != nil && *obj.ImplFragment != "" {
					compileTarget = *obj.ImplFragment
				}
				var err error
				implBodyBytes, err := os.ReadFile(compileTarget)
				if err != nil {
					return "", fmt.Errorf("read impl %s: %w", compileTarget, err)
				}
				implBody = string(implBodyBytes)
			}
			// Language: prefer ImplLang (v10) if set, else infer from file extension.
			var langTag core.Lang
			if obj.ImplLang != "" {
				langTag = core.Lang(obj.ImplLang)
			} else {
				langTag = core.LangFromExt(extOf(compileTarget))
			}
			if langTag == core.LangNone {
				return "", fmt.Errorf("cannot infer language from impl extension %q (set implLang in graph or use a recognized extension)", compileTarget)
			}
			tv := core.New(core.KindCode, string(implBody)).
				WithState(core.StateUncompiled).
				WithLang(langTag)
			env := &core.RuleEnv{WorkDir: "."}
			out, err := lang.CompileLanguageInvoker(ctx, env, tv)
			if err != nil {
				return "", err
			}
			rendered, _ := renderTypedValue(out)
			implHash := core.HashSource(string(implBody))
			symbolHash := computeSymbolHash(string(implBody), compileTarget, obj.ImplSymbol, objectID)
			if out.State == core.StateCompiled {
				effectiveLang := string(core.DetectEffectiveLang(string(implBody), langTag))
				if recErr := core.RecordEvidenceWithSymbol(objectID, "compile", effectiveLang, true, rendered, implHash, symbolHash); recErr != nil {
					return "", recErr
				}
			} else if out.Kind == core.KindInsufficient {
				effectiveLang := string(core.DetectEffectiveLang(string(implBody), langTag))
				if recErr := core.RecordEvidenceWithSymbol(objectID, "insufficient", effectiveLang, false, out.Payload, implHash, symbolHash); recErr != nil {
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
	if h, ok := core.SymbolFragmentHash(implContent, implPath, symbol); ok {
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
func typecalcTestTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: true, // subprocess + per-id trace + per-id evidence; needs unique scratch dirs
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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
			g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
			if err != nil {
				return "", err
			}
			obj, ok := g.Objects[objectID]
			if !ok {
				return "", fmt.Errorf("object %q not found in K/graph.json", objectID)
			}
			if obj.Impl == nil && obj.ImplContent == "" {
				return "", fmt.Errorf("object %q has no impl path or implContent set", objectID)
			}
			// v9.3: HTML deliverables are verified by runtime_smoke, not
			// the synth/test chain. See typecalc_synthesize_tests for the
			// rationale (v92 batch loops on HTML+vm.Script mismatch). If
			// implFragment is set, the agent should call confirm_object —
			// that routes through the JS-fragment path automatically.
			// Direct typecalc_test on an HTML impl is a category error.
			if obj.Impl != nil && strings.HasSuffix(strings.ToLower(*obj.Impl), ".html") {
				return "", fmt.Errorf("typecalc_test: object %q has impl=%s (HTML deliverable). HTML objects are verified by runtime_smoke — call `runtime_smoke object_id=%s` instead. (If you have an extracted JS fragment in implFragment, invoke confirm_object to use it.)", objectID, *obj.Impl, objectID)
			}
			var implBody string
			var testTarget string
			var cleanupTempHTML func()
			// v10: prefer ImplContent from graph (source of truth).
			// Write to a temp file for the harness (which needs a file path).
			if obj.ImplContent != "" {
				implBody = obj.ImplContent
				testTarget = writeTempImpl(objectID, obj.ImplContent)
				cleanupTempHTML = func() {
					os.Remove(testTarget)
				}
			} else {
				testTarget = *obj.Impl
				implBodyBytes, err := os.ReadFile(testTarget)
				if err != nil {
					return "", fmt.Errorf("read impl %s: %w", testTarget, err)
				}
				implBody = string(implBodyBytes)
				cleanupTempHTML = func() {}
			}
			defer cleanupTempHTML()
			t, ok := core.ReadTests(objectID)
			if !ok || (len(t.Cases) == 0 && len(t.TestCode) == 0) {
				return "", fmt.Errorf("no synthesized tests for %s — call typecalc_synthesize_tests object_id=%q first", objectID, objectID)
			}
			langTag := core.LangFromExt(extOf(testTarget))
			if langTag == core.LangNone {
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
				absTrace := cwd + string(os.PathSeparator) + core.RuntimeTracePath(objectID)
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
			compiled := core.New(core.KindCode, string(implBody)).
				WithState(core.StateCompiled).
				WithLang(langTag)
			suite := core.New(core.KindTestSuite, testSource).WithLang(langTag)
			env := &core.RuleEnv{WorkDir: "."}
			out, err := lang.TestRunInvoker(ctx, env, compiled, suite)
			if err != nil {
				return "", err
			}
			rendered, _ := renderTypedValue(out)
			implHash := core.HashSource(string(implBody))
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
			effectiveLang := string(core.DetectEffectiveLang(string(implBody), langTag))
			// 2026-05-09 v8.6 — emit a short, greppable status line to
			// stderr so test results are observable in the agent log.
			// Previously only the tool CALL was banner-logged; the tool
			// RESULT lived only in the transcript JSON, making it
			// difficult to triage runs from the log alone (v8.5 audit
			// noted "Tested<Pass>" was hard to track for some agents).
			emitTestStatusLine(objectID, out)
			if out.State == core.StateTestedPass {
				if recErr := core.RecordEvidenceWithSymbol(objectID, "test", effectiveLang, true, rendered, implHash, symbolHash); recErr != nil {
					return "", recErr
				}
			} else if out.Kind == core.KindInsufficient {
				if recErr := core.RecordEvidenceWithSymbol(objectID, "insufficient", effectiveLang, false, out.Payload, implHash, symbolHash); recErr != nil {
					return "", recErr
				}
			} else if out.Kind == core.KindTestError {
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
				if recErr := core.RecordEvidenceWithSymbol(objectID, "test", effectiveLang, false, out.Payload, implHash, symbolHash); recErr != nil {
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
func typecalcProbePlanTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: true, // pure read of K/graph.json
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "typecalc_probe_plan",
				Description: "Generate a ProbePlan from the current K/graph.json topology — the ordered list of intermediate attributes to observe for fault localization (§3 plan_probes). Use after an integration-test failure.",
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
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
func typecalcApplyFeedbackTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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
func emitTestStatusLine(objectID string, out *core.TypedValue) {
	if out == nil {
		return
	}
	status := "Other"
	color := "\x1b[36m" // cyan default
	if out.State == core.StateTestedPass {
		status = "TestedPass"
		color = "\x1b[32m" // green
	} else if out.Kind == core.KindTestError {
		status = "TestError"
		color = "\x1b[31m" // red
	} else if out.Kind == core.KindInsufficient {
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
func renderTypedValue(tv *core.TypedValue) (string, error) {
	if tv == nil {
		return "", fmt.Errorf("nil typed value")
	}
	body, err := json.MarshalIndent(tv, "", "  ")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("TYPE: %s\n%s", tv.Tag(), string(body)), nil
}

// writeTempImpl writes implContent to a temp file with an appropriate
// extension based on the object's ImplLang. Returns the temp file path.
// Used when running tests with ImplContent (v10 source-of-truth path) —
// the test harness needs a file path to execute against.
func writeTempImpl(objectID, implContent string) string {
	ext := ".txt"
	switch {
	case strings.Contains(implContent, "function "):
		ext = ".js"
	case strings.HasPrefix(strings.TrimSpace(implContent), "def "):
		ext = ".py"
	case strings.HasPrefix(strings.TrimSpace(implContent), "func "):
		ext = ".go"
	}
	f, err := os.CreateTemp("", "kcpos-"+objectID+"-*"+ext)
	if err != nil {
		// Fallback: write to a persistent temp location
		f, err = os.Create("/tmp/kcpos-" + objectID + ext)
	}
	if err != nil {
		return ""
	}
	f.WriteString(implContent)
	f.Close()
	return f.Name()
}
