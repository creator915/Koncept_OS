package snapshot

import "strings"

// lessonHeuristic pattern-matches an obstacle reason string against a
// curated table of known failure modes (gathered from PB-30 batches
// #1-#8) and returns a stable key + diagnostic + dos-and-donts.
//
// Returns ("", "", nil) when no pattern matches — caller treats
// this as "heuristic doesn't know, consider LLM fallback".
//
// Patterns are matched by substring containment, top-down. Order
// matters when a more-specific pattern could be subsumed by a
// general one (e.g. "method-use-rule" is more specific than
// "gate FAIL").
func lessonHeuristic(reason string) (key string, whatWentWrong string, dosAndDonts []string) {
	if reason == "" {
		return "", "", nil
	}
	lower := strings.ToLower(reason)

	// Order matters: most-specific first.
	switch {
	case strings.Contains(lower, "vacuous-oracle-guard"):
		return "vacuous-oracle",
			"The equivalence oracle refused to run because the agent's binary was missing or identical to the reference. " +
				"This means the agent skipped compile (or compile didn't produce a distinct executable). The oracle " +
				"would have produced a fake 100% match comparing the reference against itself; the guard correctly " +
				"refused.",
			[]string{
				"Before calling confirm_object, ensure write_file has produced source + compile.sh.",
				"Call the `compile` tool to build /workspace/executable from compile.sh.",
				"Verify the new executable's hash differs from /workspace/executable.ref before relying on the oracle.",
			}

	case strings.Contains(lower, "method-use-rule") && strings.Contains(lower, "artifact changed"):
		return "method-use-rule-drift",
			"A previously-confirmed object had its impl source edited after the characterization lock was set, " +
				"but the lock was not refreshed. The gate's Feathers Method Use Rule correctly refused to ship a " +
				"build whose locked behavior may not reflect the artifact actually being shipped.",
			[]string{
				"After editing an impl file of a confirmed object, immediately call confirm_object on it again.",
				"The chain will re-run the equivalence oracle and write a fresh CharacterizationSection with the new CodeHash.",
				"Note: write_file's Phase 3 auto-invalidate (P3 fix from 2026-05-21) should demote the object automatically — if this message still surfaces, the source was edited via a path that bypassed write_file.",
			}

	case strings.Contains(lower, "method-use-rule") && strings.Contains(lower, "zero behavior"):
		return "method-use-rule-empty-lock",
			"The characterization succeeded but locked ZERO behavior — every probe was unlocked. A lock that locks " +
				"nothing is not a behavior-preservation net.",
			[]string{
				"Re-run characterize with probes that produce observable output (port observation must be populated).",
				"If this symbol is genuinely non-characterizable (interactive TUI, side-effect-only), escalate via Obstacle rather than ship the empty lock.",
			}

	case strings.Contains(lower, "reconstruction mode") && strings.Contains(lower, "declared only"):
		return "monolithic-decomposition",
			"The agent declared a single monolithic graph object covering the whole executable. Per the Phase-2.C " +
				"design and Phase 4 P4 fix (2026-05-21), reconstruction-mode tasks require ≥2 testable sub-functions " +
				"so each can be driven through the per-object oracle within the handler iteration budget.",
			[]string{
				"Re-architect: identify 3-5 testable sub-functions (e.g. ParseArgs, DoWork, FormatOutput) from README/man/probe observations.",
				"Each sub-function should have concrete inputs/outputs reflected via consumes/produces edges.",
				"Avoid declaring top-level main() or orchestrator loops as graph objects — model them as plain glue code.",
			}

	case strings.Contains(lower, "produces+mutates ports") && strings.Contains(lower, "cap="):
		// Added 2026-05-25 after PB-30 batch 0522 tty-clock burned 3
		// retries on this exact failure with `heuristic:no-match` —
		// the generic lesson didn't teach the agent to keep object
		// granularity ≤4 ports, so attempts 2 and 3 re-declared the
		// same monolithic ProvideClockConfig (11 ports) each time.
		return "object-too-many-ports",
			"H_graph_declare refused to advance because an object's produces+mutates port total exceeded the " +
				"per-object cap (=4). Monolithic objects with many output ports make typecalc_synthesize_tests " +
				"stream tens of KB per call (5-9 min wall time) and exhaust handler_max_iter. The lesson the " +
				"previous attempt failed to extract: split the wide object into smaller pieces with ≤4 output " +
				"ports each, OR group related scalar outputs into a single structured port.",
			[]string{
				"Identify the offending object from the failure message (it names the object + port count).",
				"REWRITE the graph_create_object for that object: replace its many scalar produces with ONE structured produce (e.g. a `config` attribute whose value carries all 19 flags as nested fields).",
				"OR split the object into ≤4-port sub-objects: e.g. one object per logical group of flags (display flags / behavior flags / runtime flags).",
				"Do NOT re-declare the same wide object hoping the gate will change its mind — the cap is hard. The fix MUST happen at graph_create_object call time, before graph_preflight.",
				"After re-declaration, every object's `produces` + `mutates` total must be ≤4 OR confirm_object will be inaccessible.",
			}

	case strings.Contains(lower, "compile-not-enough"):
		return "compile-not-enough",
			"The gate refused to accept compile-only evidence for a language outside the test-runner whitelist. " +
				"Either the language has no in-tree test runner OR the language has one but kind=test evidence was " +
				"never recorded.",
			[]string{
				"Check if the chain's equiv_oracle ran (Characterization section present with LockedCount > 0). " +
					"If yes, the [compile-not-enough] rule should bypass per the 2026-05-21 reconstruction-mode fix — " +
					"if it still fires, equiv_oracle didn't run or recorded zero locks.",
				"Run typecalc_test if the lang IS test-runner-supported; the chain should do this automatically — " +
					"if it didn't, investigate the synthesize step.",
			}

	case strings.Contains(lower, "typecalc-test-required"):
		return "typecalc-test-required",
			"The gate requires kind=test evidence for this object's language but only compile evidence was " +
				"recorded. The chain's synthesize → test step did not produce test evidence.",
			[]string{
				"Call typecalc_synthesize_tests to regenerate the test suite.",
				"Inspect the synthesized tests; if they don't call the impl correctly, fix portObservation or " +
					"the impl's signature.",
				"Run typecalc_test to confirm kind=test evidence is recorded.",
			}

	case strings.Contains(lower, "runtime-trace-missing"):
		return "runtime-trace-missing",
			"The review step found no runtime trace — synthesized tests ran but didn't record per-call " +
				"inputs/outputs to the bundle's RuntimeTrace section. Either appendTrace was not called or the " +
				"tests failed before reaching it.",
			[]string{
				"Re-synthesize tests; ensure each case calls appendTrace(inputs, outputs) BEFORE assertions.",
				"For C tests, use the inline appendTrace() declaration the runner provides.",
				"If reconstruction mode is active (./probe present + LockedCount>0), review should bypass this " +
					"check entirely per the 2026-05-21 fix — if it still fires, equiv_oracle didn't record a lock.",
			}

	case strings.Contains(lower, "no in-tree compile invoker"),
		strings.Contains(lower, "no in-tree test runner"):
		return "unsupported-language",
			"The language declared on the impl has no compile or test invoker in internal/typecalc/lang/. " +
				"v9.2 removed the waiver escape, so the gate WILL refuse to confirm.",
			[]string{
				"Restructure the impl into a supported language (Go / TypeScript / JavaScript / Python / Rust / C / HTML).",
				"For reconstruction-mode tasks: the language is locked by SPEC.md and must match the reference binary type.",
				"If extending kcpos to support a new lang: add CompileLanguageInvoker + TestRunInvoker in internal/typecalc/lang/.",
			}

	case strings.Contains(lower, "did not reach confirmed after sub-agent loop"):
		return "confirm-sub-agent-exhausted",
			"The H_confirm_one sub-agent ran its full handler_max_iter budget (default 60) without driving the " +
				"object to confirmed. Common cause: the object is too coarse (storyPoints too high or impl logic " +
				"too complex for one sub-agent loop).",
			[]string{
				"Inspect .kcpos/typecalc/<id>.json's accepted section to see the last gate verdict.",
				"If the object is monolithic, split it via graph_split_object into smaller pieces.",
				"If the impl is wrong, edit it (the Phase 3 auto-invalidate will re-trigger characterize on next confirm).",
				"If the synthesized tests don't match the contract, regenerate via typecalc_synthesize_tests.",
			}

	case strings.Contains(lower, "gate fail"):
		return "gate-fail-generic",
			"The session_gate_check refused to mark root finished. Specific rules are listed in the obstacle " +
				"reason above; address each.",
			[]string{
				"Read each '✗ [rule-name]' line in the obstacle reason — it names the failing rule and the fix.",
				"Common offenders: [accepted-evidence-required] (run typecalc_review), [attrs-backfilled] (call graph_merge_attribute on every produced/mutated attribute), [method-use-rule] (re-characterize edited impls).",
			}

	case strings.Contains(lower, "per-step inactivity timeout"):
		return "step-timeout",
			"A chain dep (compile / describe / synthesize / test / review / characterize) didn't return within " +
				"the 8-minute budget. Usually means the LLM stream stalled.",
			[]string{
				"Check the agent's run.log for the last visible action before the timeout — that step likely had a stuck LLM call.",
				"Re-run the same operation; the LLM transport's retries usually clear transient stalls.",
				"If the timeout fires repeatedly on the same step, the chain dep may have a real bug — file an issue.",
			}
	}

	return "", "", nil
}
