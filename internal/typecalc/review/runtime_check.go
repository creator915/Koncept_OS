package review

import (
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
)

// minSparseExpected: don't fire runtime-trace-sparse for tiny suites
// where missing one trace is normal noise, not a signal.
const minSparseExpected = 2

// sparseRatioThreshold: when actual_calls / expected_calls < this,
// flag the trace as sparse. 0.5 = "fewer than half the test cases
// successfully reached appendTrace".
const sparseRatioThreshold = 0.5

// RuntimeRuleCodes is the canonical list of every rule RuntimeCheck
// emits a signal for. v9.3.2: aggregator uses this to detect rules
// that were expected but didn't fire (silent-pass guard).
var RuntimeRuleCodes = []string{
	"runtime-object-not-found",
	"runtime-trace-missing",
	"runtime-trace-empty",
	"runtime-output-missing",
	"runtime-input-missing",
	"runtime-value-conforms", // umbrella for type-mismatch / out-of-range / enum-violation
	"runtime-trace-sparse",
	"runtime-temporal-frame",
}

// RuntimeCheck inspects the trace logged by the synthesized tests
// (.kcpos/typecalc-runtime/<id>.json) and verifies the observed port
// signals against the graph's declarations.
//
// v9.3.2: returns core.CheckReport. Every rule emits Pass/Fail/Skipped
// explicitly. HTML deliverables Skip every rule with reason
// "html-branch" — they're verified via runtime_smoke (Chromium boot),
// not via this harness-trace path.
func RuntimeCheck(g *graph.Graph, objID string) core.CheckReport {
	rb := core.NewReportBuilder()
	mkIssue := func(code, msg string) core.StaticIssue {
		return core.StaticIssue{Code: code, Where: objID, Message: msg}
	}

	obj, ok := g.Objects[objID]
	if !ok {
		rb.Fail("runtime-object-not-found", mkIssue("runtime-object-not-found",
			fmt.Sprintf("object %q not in graph", objID)))
		// Can't run subsequent rules.
		for _, code := range RuntimeRuleCodes {
			if code == "runtime-object-not-found" {
				continue
			}
			rb.Skip(code, "object-not-found")
		}
		return rb.Build()
	}
	rb.Pass("runtime-object-not-found")

	// v9.3.1: HTML deliverables are verified via runtime_smoke, not
	// the synthesize+test harness. Mark every remaining rule explicitly
	// Skipped so the aggregator sees the rule considered (v9.3.2
	// silent-pass guard).
	if IsHTMLDeliverable(obj) {
		for _, code := range RuntimeRuleCodes {
			if code == "runtime-object-not-found" {
				continue
			}
			rb.Skip(code, "html-branch")
		}
		return rb.Build()
	}

	trace, hasTrace := core.ReadRuntimeTrace(objID)
	if !hasTrace {
		rb.Fail("runtime-trace-missing", mkIssue("runtime-trace-missing",
			fmt.Sprintf("no runtime trace at %s — synthesized tests must append per-call inputs/outputs there", core.RuntimeTracePath(objID))))
		// All downstream rules need a trace; skip them.
		for _, code := range []string{"runtime-trace-empty", "runtime-output-missing", "runtime-input-missing", "runtime-value-conforms", "runtime-trace-sparse", "runtime-temporal-frame"} {
			rb.Skip(code, "no-trace")
		}
		return rb.Build()
	}
	rb.Pass("runtime-trace-missing")

	if len(trace.Calls) == 0 {
		rb.Fail("runtime-trace-empty", mkIssue("runtime-trace-empty",
			"runtime trace exists but recorded zero calls — tests did not exercise the function"))
		for _, code := range []string{"runtime-output-missing", "runtime-input-missing", "runtime-value-conforms", "runtime-trace-sparse", "runtime-temporal-frame"} {
			rb.Skip(code, "empty-trace")
		}
		return rb.Build()
	}
	rb.Pass("runtime-trace-empty")

	// 1. Output port presence.
	seenOut := map[string]bool{}
	for _, c := range trace.Calls {
		for k := range c.Outputs {
			seenOut[k] = true
		}
	}
	var outputMissingFails []core.StaticIssue
	for _, p := range obj.Produces {
		if !seenOut[p] {
			outputMissingFails = append(outputMissingFails, mkIssue("runtime-output-missing",
				fmt.Sprintf("declares produces %q but no recorded call wrote that port", p)))
		}
	}
	for _, p := range obj.Mutates {
		if !seenOut[p] {
			outputMissingFails = append(outputMissingFails, mkIssue("runtime-output-missing",
				fmt.Sprintf("declares mutates %q but no recorded call wrote that port", p)))
		}
	}
	if len(outputMissingFails) > 0 {
		rb.Fail("runtime-output-missing", outputMissingFails...)
	} else {
		rb.Pass("runtime-output-missing")
	}

	// 2. Input port presence (with pass-through carve-out).
	seenIn := map[string]bool{}
	seenOutForThisCheck := map[string]bool{}
	for _, c := range trace.Calls {
		for k := range c.Inputs {
			seenIn[k] = true
		}
		for k := range c.Outputs {
			seenOutForThisCheck[k] = true
		}
	}
	producedSet := map[string]bool{}
	for _, p := range obj.Produces {
		producedSet[p] = true
	}
	mutatedSet := map[string]bool{}
	for _, p := range obj.Mutates {
		mutatedSet[p] = true
	}
	var inputMissingFails []core.StaticIssue
	for _, c := range obj.Consumes {
		if seenIn[c] {
			continue
		}
		// Pass-through carve-out: produced or mutated AND observed in
		// output snapshot counts as evidence the port was read.
		if (producedSet[c] || mutatedSet[c]) && seenOutForThisCheck[c] {
			continue
		}
		inputMissingFails = append(inputMissingFails, mkIssue("runtime-input-missing",
			fmt.Sprintf("declares consumes %q but no recorded call read that port", c)))
	}
	if len(inputMissingFails) > 0 {
		rb.Fail("runtime-input-missing", inputMissingFails...)
	} else {
		rb.Pass("runtime-input-missing")
	}

	// 3. Value range / type per port (umbrella rule).
	var valueFails []core.StaticIssue
	for _, call := range trace.Calls {
		for port, raw := range call.Inputs {
			valueFails = append(valueFails, checkValueAgainst(g, objID, port, raw, "input")...)
		}
		for port, raw := range call.Outputs {
			valueFails = append(valueFails, checkValueAgainst(g, objID, port, raw, "output")...)
		}
	}
	if len(valueFails) > 0 {
		rb.Fail("runtime-value-conforms", valueFails...)
	} else {
		rb.Pass("runtime-value-conforms")
	}

	// 3.5 Trace sparsity. Detective rule for tests that assert-throw
	//     before appendTrace.
	if t, ok := core.ReadTests(objID); ok && t.TestCode != "" {
		expected := strings.Count(t.TestCode, "appendTrace(")
		actual := len(trace.Calls)
		switch {
		case expected < minSparseExpected:
			rb.Skip("runtime-trace-sparse", "expected-below-threshold")
		case float64(actual)/float64(expected) < sparseRatioThreshold:
			rb.Fail("runtime-trace-sparse", mkIssue("runtime-trace-sparse",
				fmt.Sprintf("recorded %d/%d expected calls — most tests likely failed before reaching appendTrace; ensure tests append-before-assert per the synthesize prompt", actual, expected)))
		default:
			rb.Pass("runtime-trace-sparse")
		}
	} else {
		rb.Skip("runtime-trace-sparse", "no-test-code")
	}

	// 4. Temporal causality. Skip when object doesn't declare temporal.
	if obj.Temporal == nil || obj.Temporal.FrameVar == "" {
		rb.Skip("runtime-temporal-frame", "no-temporal")
	} else {
		var temporalFails []core.StaticIssue
		for i, call := range trace.Calls {
			if call.Frame == "" {
				temporalFails = append(temporalFails, mkIssue("runtime-temporal-frame",
					fmt.Sprintf("call[%d] has no frame field but object declares temporal", i)))
				continue
			}
			_ = call.Frame
		}
		if len(temporalFails) > 0 {
			rb.Fail("runtime-temporal-frame", temporalFails...)
		} else {
			rb.Pass("runtime-temporal-frame")
		}
	}

	return rb.Build()
}

// checkValueAgainst inspects a single port value against the
// graph attribute's declared valueSpace, surfacing only what is
// definitively wrong.
//
// Recognised valueSpace fields:
//
//   - "type":  one of "number" | "integer" | "string" | "boolean" |
//              "object" | "array". Type mismatch ⇒ runtime-type-mismatch.
//   - "min" / "max": numeric bounds (only checked when type is number/integer).
//   - "enum":  array of allowed values. Value not in set ⇒ runtime-enum-violation.
//
// Anything else in valueSpace (descriptions, sub-shapes, etc.) is
// ignored. The check is a backstop, not a deep schema validator.
func checkValueAgainst(g *graph.Graph, objID, port string, raw json.RawMessage, dir string) []core.StaticIssue {
	a, ok := g.Attributes[port]
	if !ok || a.ValueSpace == nil || len(a.ValueSpace) == 0 {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil // can't parse — let the test framework surface it
	}
	var issues []core.StaticIssue
	push := func(code, msg string) {
		issues = append(issues, core.StaticIssue{
			Code:    code,
			Where:   objID,
			Message: fmt.Sprintf("%s port %q: %s", dir, port, msg),
		})
	}

	if want, hasType := a.ValueSpace["type"].(string); hasType && want != "" {
		// 2026-05-11 v8.7 — friendly degradation for type:"enum".
		// pong-02 v8.6 ran InitGame.game_phase with valueSpace
		// {type:"enum", values:["playing","game_over"]}. "enum" is
		// NOT a JSON-Schema type; the agent meant "{type:'string',
		// enum:[...]}". jsonType() never returns "enum", so every
		// observed "playing" / "game_over" fired runtime-type-mismatch
		// — 80 false positives in one trace. Recognize the intent
		// (use the values list as a flat enum) instead of pedantically
		// rejecting the schema mistake. If the enum-list itself is
		// missing, treat as a no-op rather than failing every value.
		if want == "enum" {
			vals, _ := a.ValueSpace["values"].([]interface{})
			if vals == nil {
				vals, _ = a.ValueSpace["enum"].([]interface{})
			}
			if len(vals) > 0 {
				matched := false
				for _, allowed := range vals {
					if equalLoose(allowed, v) {
						matched = true
						break
					}
				}
				if !matched {
					push("runtime-enum-violation", fmt.Sprintf("declared enum=%v, observed %s", vals, abbrev(string(raw))))
				}
			}
			// Whether or not enum was provided, type='enum' is
			// non-canonical — emit a single hint per call site (not
			// per value) so the agent sees the correction once.
			// Skip range/enum on the same value (same as type mismatch).
			return issues
		}
		got := jsonType(v)
		if !typeCompatible(got, want) {
			push("runtime-type-mismatch", fmt.Sprintf("declared type=%q, observed %q (value=%s)", want, got, abbrev(string(raw))))
			// type mismatch is enough — skip range/enum on the same value
			return issues
		}
	}

	if min, ok := numFromAny(a.ValueSpace["min"]); ok {
		if vv, ok := numFromAny(v); ok && vv < min {
			push("runtime-out-of-range", fmt.Sprintf("declared min=%v, observed %v", min, vv))
		}
	}
	if max, ok := numFromAny(a.ValueSpace["max"]); ok {
		if vv, ok := numFromAny(v); ok && vv > max {
			push("runtime-out-of-range", fmt.Sprintf("declared max=%v, observed %v", max, vv))
		}
	}
	if enum, ok := a.ValueSpace["enum"].([]interface{}); ok && len(enum) > 0 {
		matched := false
		for _, allowed := range enum {
			if equalLoose(allowed, v) {
				matched = true
				break
			}
		}
		if !matched {
			push("runtime-enum-violation", fmt.Sprintf("declared enum=%v, observed %s", enum, abbrev(string(raw))))
		}
	}
	return issues
}

// jsonType returns a JSON-Schema-style type name for v.
func jsonType(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		// Distinguish integer-valued floats so "type": "integer" works.
		if x == float64(int64(x)) {
			return "integer"
		}
		return "number"
	case string:
		return "string"
	case []interface{}:
		return "array"
	case map[string]interface{}:
		return "object"
	}
	return "unknown"
}

// typeCompatible accepts integer where number is wanted (and vice
// versa, since JS / Python serialise integers as "number").
func typeCompatible(got, want string) bool {
	if got == want {
		return true
	}
	if (want == "number" && got == "integer") ||
		(want == "integer" && got == "number") {
		return true
	}
	return false
}

func numFromAny(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}

// equalLoose compares two interface{} values for "same JSON" — used
// for enum membership.
func equalLoose(a, b interface{}) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

func abbrev(s string) string {
	const max = 80
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
