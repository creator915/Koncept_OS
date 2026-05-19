package router

import (
	"regexp"
	"strings"
)

// Phase 2.4 — Format Checker.
//
// Per the doc: "从 Handler 实际拒绝的输入中提取检查规则". These rules are
// NOT invented — each mirrors a real precondition a Phase-1 handler
// already enforces, so the checker can pre-screen an input BEFORE the
// handler and return the SAME class of reason list:
//
//   graph_create_object   — id+intent required; storyPoints ∈ Fibonacci;
//                            storyRationale ≥10  (tools/graph/graph.go)
//   graph_create_attribute— id+intent required           (graph.go)
//   graph_merge_object    — patch must NOT hand-set status=confirmed
//                            (graph.go no-hand-set guard, ① invariant)
//   session_start         — id must match s_<lc>; expands_object, when
//                            present, must be non-empty (session.ValidateID
//                            + tools/session/session.go)
//
// Output shape is the project-wide []reason (same as GateReport.Issues /
// ExpansionFinishReasons) — never a single opaque error.

var (
	fcSessionID  = regexp.MustCompile(`^s_[a-z][a-z0-9_]*$`)
	fcFib        = map[int]bool{1: true, 2: true, 3: true, 5: true, 8: true, 13: true}
)

// CheckFormat pre-screens args for handler op; empty result = well-formed.
func CheckFormat(op string, args map[string]interface{}) []string {
	var rs []string
	str := func(k string) string { s, _ := args[k].(string); return strings.TrimSpace(s) }

	switch op {
	case "graph_create_object":
		if str("id") == "" {
			rs = append(rs, "graph_create_object: id required")
		}
		if str("intent") == "" {
			rs = append(rs, "graph_create_object: intent required")
		}
		sp, ok := toInt(args["storyPoints"])
		if !ok || !fcFib[sp] {
			rs = append(rs, "graph_create_object: storyPoints must be Fibonacci 1/2/3/5/8/13")
		}
		if len(str("storyRationale")) < 10 {
			rs = append(rs, "graph_create_object: storyRationale required (≥10 chars)")
		}
	case "graph_create_attribute":
		if str("id") == "" {
			rs = append(rs, "graph_create_attribute: id required")
		}
		if str("intent") == "" {
			rs = append(rs, "graph_create_attribute: intent required")
		}
	case "graph_merge_object":
		if patchSetsConfirmed(str("patch")) {
			rs = append(rs, "graph_merge_object: status=confirmed is NOT hand-settable (① process-justice invariant)")
		}
	case "session_start":
		id := str("id")
		norm := id
		if !strings.HasPrefix(norm, "s_") {
			norm = "s_" + norm
		}
		if id == "" || !fcSessionID.MatchString(norm) {
			rs = append(rs, "session_start: id must normalize to s_<lowercase_name>")
		}
		if _, present := args["expands_object"]; present && str("expands_object") == "" {
			rs = append(rs, "session_start: expands_object, when given, must be non-empty")
		}
	}
	return rs
}

func toInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	}
	return 0, false
}

// patchSetsConfirmed detects a JSON object patch that sets
// "status":"confirmed" (whitespace-tolerant), the one mutation the
// merge-object handler hard-refuses.
func patchSetsConfirmed(patch string) bool {
	p := strings.ReplaceAll(patch, " ", "")
	return strings.Contains(p, `"status":"confirmed"`)
}
