package router

import (
	"strings"
	"testing"
)

// P2.4: Format Checker — each rule mirrors a real Phase-1 handler
// rejection. Well-formed input ⇒ empty; malformed ⇒ a reason list.

func TestFormat_GraphCreateObject_WellFormed(t *testing.T) {
	rs := CheckFormat("graph_create_object", map[string]interface{}{
		"id": "Foo", "intent": "does foo", "storyPoints": 2,
		"storyRationale": "single loop with check",
	})
	if len(rs) != 0 {
		t.Errorf("well-formed object must pass, got %v", rs)
	}
}

func TestFormat_GraphCreateObject_MissingIdAndIntent(t *testing.T) {
	rs := CheckFormat("graph_create_object", map[string]interface{}{
		"storyPoints": 2, "storyRationale": "single loop with check",
	})
	if len(rs) < 2 {
		t.Fatalf("missing id+intent must yield ≥2 reasons, got %v", rs)
	}
}

func TestFormat_GraphCreateObject_BadStoryPoints(t *testing.T) {
	rs := CheckFormat("graph_create_object", map[string]interface{}{
		"id": "Foo", "intent": "x", "storyPoints": 4,
		"storyRationale": "ten chars ok here",
	})
	if !hasReason(rs, "storyPoints must be Fibonacci") {
		t.Errorf("non-Fibonacci storyPoints must be rejected, got %v", rs)
	}
}

func TestFormat_GraphCreateObject_ShortRationale(t *testing.T) {
	rs := CheckFormat("graph_create_object", map[string]interface{}{
		"id": "Foo", "intent": "x", "storyPoints": 3, "storyRationale": "short",
	})
	if !hasReason(rs, "storyRationale required") {
		t.Errorf("short rationale must be rejected, got %v", rs)
	}
}

func TestFormat_GraphCreateObject_FloatStoryPointsAccepted(t *testing.T) {
	// JSON numbers arrive as float64 — the checker must tolerate that
	// exactly like the real handler does.
	rs := CheckFormat("graph_create_object", map[string]interface{}{
		"id": "Foo", "intent": "x", "storyPoints": float64(5),
		"storyRationale": "five point rationale",
	})
	if len(rs) != 0 {
		t.Errorf("float64 storyPoints=5 must pass, got %v", rs)
	}
}

func TestFormat_GraphCreateAttribute_RequiresIdIntent(t *testing.T) {
	if rs := CheckFormat("graph_create_attribute", map[string]interface{}{"id": "x", "intent": "y"}); len(rs) != 0 {
		t.Errorf("valid attribute must pass, got %v", rs)
	}
	if rs := CheckFormat("graph_create_attribute", map[string]interface{}{"id": ""}); len(rs) < 2 {
		t.Errorf("missing id+intent must yield ≥2 reasons, got %v", rs)
	}
}

func TestFormat_MergeObject_NoHandSetConfirmed(t *testing.T) {
	rs := CheckFormat("graph_merge_object", map[string]interface{}{
		"id": "Foo", "patch": `{"status":"confirmed"}`,
	})
	if !hasReason(rs, "NOT hand-settable") {
		t.Errorf("hand-set confirmed must be rejected (①), got %v", rs)
	}
	// whitespace-tolerant
	rs2 := CheckFormat("graph_merge_object", map[string]interface{}{
		"id": "Foo", "patch": `{ "status" : "confirmed" }`,
	})
	if !hasReason(rs2, "NOT hand-settable") {
		t.Errorf("whitespace patch must still be caught, got %v", rs2)
	}
}

func TestFormat_MergeObject_OtherStatusAllowed(t *testing.T) {
	rs := CheckFormat("graph_merge_object", map[string]interface{}{
		"id": "Foo", "patch": `{"status":"implementing"}`,
	})
	if len(rs) != 0 {
		t.Errorf("status=implementing must NOT trip the confirmed guard, got %v", rs)
	}
}

func TestFormat_SessionStart_IdShape(t *testing.T) {
	if rs := CheckFormat("session_start", map[string]interface{}{"id": "weather_proc"}); len(rs) != 0 {
		t.Errorf("bare name (s_ auto-prepended) must pass, got %v", rs)
	}
	if rs := CheckFormat("session_start", map[string]interface{}{"id": "S_Bad-ID"}); len(rs) == 0 {
		t.Error("malformed session id must be rejected")
	}
}

func TestFormat_SessionStart_EmptyExpandsObjectRejected(t *testing.T) {
	rs := CheckFormat("session_start", map[string]interface{}{
		"id": "s_x", "expands_object": "  ",
	})
	if !hasReason(rs, "expands_object") {
		t.Errorf("present-but-empty expands_object must be rejected, got %v", rs)
	}
	// absent expands_object is fine (plain session)
	if rs := CheckFormat("session_start", map[string]interface{}{"id": "s_x"}); len(rs) != 0 {
		t.Errorf("plain session_start must pass, got %v", rs)
	}
}

func TestFormat_UnknownOp_NoRules(t *testing.T) {
	if rs := CheckFormat("not_a_handler", map[string]interface{}{}); len(rs) != 0 {
		t.Errorf("unknown op must be a no-op (no invented rules), got %v", rs)
	}
}

func hasReason(rs []string, sub string) bool {
	for _, r := range rs {
		if strings.Contains(r, sub) {
			return true
		}
	}
	return false
}
