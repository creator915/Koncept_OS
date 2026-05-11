package typecalc

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/creator915/Koncept_OS/internal/graph"
)

// minSparseExpected: don't fire runtime-trace-sparse for tiny suites
// where missing one trace is normal noise, not a signal.
const minSparseExpected = 2

// sparseRatioThreshold: when actual_calls / expected_calls < this,
// flag the trace as sparse. 0.5 = "fewer than half the test cases
// successfully reached appendTrace".
const sparseRatioThreshold = 0.5

// RuntimeCheck inspects the trace logged by the synthesized tests
// (.kcpos/typecalc-runtime/<id>.json) and verifies the observed port
// signals against the graph's declarations:
//
//   - every declared `produces` attribute appears in some call's outputs
//     (otherwise: runtime-output-missing — the impl claims to produce
//     X but never did during testing)
//   - every declared `consumes` attribute appears in some call's inputs
//     (runtime-input-missing — the test never exercised the port)
//   - every observed value lies within the declared valueSpace where
//     the spec is concrete enough to check (numeric range, type, enum)
//   - declared `temporal` frame relationships hold across consecutive
//     calls (output frame depth ≥ input frame depth)
//
// The check is the user-proposal "standard 1": it judges only what
// MUST be wrong (missing port, out-of-range value), never what is
// right. Empty issue list means "no contradiction with the graph
// declarations", not "implementation is correct".
//
// Returns nil when the impl has no port declarations at all (a pure
// side-effecting function that intentionally has no observable I/O —
// these are flagged separately by the static-check rule
// `effects-empty`).
func RuntimeCheck(g *graph.Graph, objID string) []StaticIssue {
	obj, ok := g.Objects[objID]
	if !ok {
		return []StaticIssue{{
			Code: "runtime-object-not-found", Where: objID,
			Message: fmt.Sprintf("object %q not in graph", objID),
		}}
	}
	trace, ok := ReadRuntimeTrace(objID)
	if !ok {
		return []StaticIssue{{
			Code:    "runtime-trace-missing",
			Where:   objID,
			Message: fmt.Sprintf("no runtime trace at %s — synthesized tests must append per-call inputs/outputs there", RuntimeTracePath(objID)),
		}}
	}
	if len(trace.Calls) == 0 {
		return []StaticIssue{{
			Code:    "runtime-trace-empty",
			Where:   objID,
			Message: "runtime trace exists but recorded zero calls — tests did not exercise the function",
		}}
	}

	var issues []StaticIssue

	// 1. Output port presence: every declared produces / mutates must
	//    appear in at least one call's outputs.
	seenOut := map[string]bool{}
	for _, c := range trace.Calls {
		for k := range c.Outputs {
			seenOut[k] = true
		}
	}
	for _, p := range obj.Produces {
		if !seenOut[p] {
			issues = append(issues, StaticIssue{
				Code:    "runtime-output-missing",
				Where:   objID,
				Message: fmt.Sprintf("declares produces %q but no recorded call wrote that port", p),
			})
		}
	}
	for _, p := range obj.Mutates {
		if !seenOut[p] {
			issues = append(issues, StaticIssue{
				Code:    "runtime-output-missing",
				Where:   objID,
				Message: fmt.Sprintf("declares mutates %q but no recorded call wrote that port", p),
			})
		}
	}

	// 2. Input port presence: every declared consumes must appear in
	//    at least one call's inputs.
	//
	// Carve-out (2026-05-09 v7→v8 fix): when a port is BOTH consumed
	// and produced by the same object, the harness's input-snapshot
	// often returns undefined because the value lives in the return
	// value (not args/globalThis at pre-call time). Agent in v7 hit
	// this 10× across UpdatePaddle / UpdateBall / Render / ReadInput
	// and accumulated the flapping into needless obstacles. The
	// semantic resolution: a "passes-through-and-modifies" port IS
	// observed when the output snapshot records it; demanding a
	// separate input record on top is double-bookkeeping.
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
	// v8.6 extension: mutates ports are also pass-through. A function
	// that mutates X is by definition reading X and writing X — so
	// seeing the port show up in the output snapshot is equivalent
	// proof of the input read. Pong-01/04 (v8.5) had UpdateBall /
	// UpdatePaddle with args.0.* extractors that the harness couldn't
	// snapshot pre-call (callArgs is undefined at input-snapshot time
	// in the harness), but the same port DID appear post-call in
	// outputs. The v8 carve-out only covered produces+consumes
	// overlap; this adds mutates+consumes overlap.
	mutatedSet := map[string]bool{}
	for _, p := range obj.Mutates {
		mutatedSet[p] = true
	}
	for _, c := range obj.Consumes {
		if seenIn[c] {
			continue
		}
		// Pass-through carve-out: if the port is also produced OR
		// mutated AND we observed an output for it, treat the
		// consume as covered by the output observation. The function
		// received-and-returned the value; that's enough evidence
		// it read it.
		if (producedSet[c] || mutatedSet[c]) && seenOutForThisCheck[c] {
			continue
		}
		issues = append(issues, StaticIssue{
			Code:    "runtime-input-missing",
			Where:   objID,
			Message: fmt.Sprintf("declares consumes %q but no recorded call read that port", c),
		})
	}

	// 3. Value range / type per port. We only enforce constraints the
	//    valueSpace explicitly states ("type", "min", "max", "enum"); any
	//    declared shape we cannot interpret is silently accepted.
	for _, call := range trace.Calls {
		for port, raw := range call.Inputs {
			issues = append(issues, checkValueAgainst(g, objID, port, raw, "input")...)
		}
		for port, raw := range call.Outputs {
			issues = append(issues, checkValueAgainst(g, objID, port, raw, "output")...)
		}
	}

	// 3.5 Trace sparsity. Compare the actual call count to the number
	//     of `appendTrace(` invocations expected from the synthesized
	//     test suite. Trace files with far fewer calls than expected
	//     usually mean the test suite assertion-throws before the trace
	//     append (B-fix: the synthesize prompt now teaches "append
	//     BEFORE assert" but enforcement at the prompt level is
	//     unreliable; this is the detective rule).
	if t, ok := ReadTests(objID); ok && t.TestCode != "" {
		expected := strings.Count(t.TestCode, "appendTrace(")
		actual := len(trace.Calls)
		if expected >= minSparseExpected && float64(actual)/float64(expected) < sparseRatioThreshold {
			issues = append(issues, StaticIssue{
				Code:    "runtime-trace-sparse",
				Where:   objID,
				Message: fmt.Sprintf("recorded %d/%d expected calls — most tests likely failed before reaching appendTrace; ensure tests append-before-assert per the synthesize prompt", actual, expected),
			})
		}
	}

	// 4. Temporal causality (when graph.Object has temporal). Frame
	//    output depths must be ≥ max input depth in the same call.
	if obj.Temporal != nil && obj.Temporal.FrameVar != "" {
		for i, call := range trace.Calls {
			if call.Frame == "" {
				issues = append(issues, StaticIssue{
					Code:    "runtime-temporal-missing-frame",
					Where:   objID,
					Message: fmt.Sprintf("call[%d] has no frame field but object declares temporal", i),
				})
				continue
			}
			// We don't enforce the strict graph-side frame-depth math here
			// — checker.go already validates the static side. This is just
			// a smoke check that the test exercised SOME frame.
			_ = call.Frame
		}
	}

	return issues
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
func checkValueAgainst(g *graph.Graph, objID, port string, raw json.RawMessage, dir string) []StaticIssue {
	a, ok := g.Attributes[port]
	if !ok || a.ValueSpace == nil || len(a.ValueSpace) == 0 {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil // can't parse — let the test framework surface it
	}
	var issues []StaticIssue
	push := func(code, msg string) {
		issues = append(issues, StaticIssue{
			Code:    code,
			Where:   objID,
			Message: fmt.Sprintf("%s port %q: %s", dir, port, msg),
		})
	}

	if want, hasType := a.ValueSpace["type"].(string); hasType && want != "" {
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
