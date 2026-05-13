package services

import (
	"context"
	"strings"
	"testing"
)

// TestDescribeTool_SpecShape verifies the JSON-schema spec sent to the
// LLM is well-formed and required fields are correct. The LLM only sees
// the spec, so a malformed schema breaks the agent before the tool ever
// runs.
func TestDescribeTool_SpecShape(t *testing.T) {
	tool := typecalcDescribeTool()
	if tool.Spec.Function.Name != "typecalc_describe" {
		t.Fatalf("name=%q", tool.Spec.Function.Name)
	}
	required, _ := tool.Spec.Function.Parameters["required"].([]string)
	if len(required) != 1 || required[0] != "object_id" {
		t.Fatalf("expected required=[object_id], got %v", required)
	}
}

func TestReviewTool_SpecShape(t *testing.T) {
	tool := typecalcReviewTool()
	if tool.Spec.Function.Name != "typecalc_review" {
		t.Fatalf("name=%q", tool.Spec.Function.Name)
	}
	required, _ := tool.Spec.Function.Parameters["required"].([]string)
	if len(required) != 1 || required[0] != "object_id" {
		t.Fatalf("expected required=[object_id], got %v", required)
	}
	// Post-id-only redesign: review takes only object_id. test_code /
	// test_log args are gone — they're read from disk (the kind=test
	// evidence's `log` field, plus <id>.tests.json for testCode).
	props, _ := tool.Spec.Function.Parameters["properties"].(map[string]interface{})
	if _, ok := props["object_id"]; !ok {
		t.Fatalf("missing property object_id")
	}
	if _, ok := props["test_code"]; ok {
		t.Fatalf("test_code arg should have been removed (id-only redesign)")
	}
	if _, ok := props["test_log"]; ok {
		t.Fatalf("test_log arg should have been removed (id-only redesign)")
	}
}

// TestDescribeTool_RejectsMissingObjectID exercises the most common
// agent mistake — calling the tool without object_id — and verifies
// the error is explicit, not a panic or generic failure.
func TestDescribeTool_RejectsMissingObjectID(t *testing.T) {
	tool := typecalcDescribeTool()
	_, err := tool.Run(context.Background(), map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error when object_id missing")
	}
	if !strings.Contains(err.Error(), "object_id required") {
		t.Fatalf("error too generic: %v", err)
	}
}

func TestReviewTool_RejectsMissingObjectID(t *testing.T) {
	tool := typecalcReviewTool()
	_, err := tool.Run(context.Background(), map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error when object_id missing")
	}
	if !strings.Contains(err.Error(), "object_id required") {
		t.Fatalf("error too generic: %v", err)
	}
}

// TestBuiltinsRegistry_IncludesNewTools verifies that Tools() now exposes
// the new entries — without this, the agent loop never sees them and
// the gate rule would always fail because the agent has no way to
// produce accepted evidence.
func TestBuiltinsRegistry_IncludesNewTools(t *testing.T) {
	tools := TypecalcTools()
	for _, name := range []string{"typecalc_describe", "typecalc_review"} {
		if _, ok := tools[name]; !ok {
			t.Fatalf("registry missing %q", name)
		}
	}
	// And the existing four are still there.
	for _, name := range []string{
		"typecalc_compile", "typecalc_test",
		"typecalc_probe_plan", "typecalc_apply_feedback",
	} {
		if _, ok := tools[name]; !ok {
			t.Fatalf("registry lost existing %q", name)
		}
	}
}
