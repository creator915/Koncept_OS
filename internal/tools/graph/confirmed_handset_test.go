package graphtools

import (
	"context"
	"os"
	"strings"
	"testing"
)

// ① regression guard. status=confirmed is NOT hand-settable via
// graph_merge_object — it is conferred ONLY by the confirm_object
// verification chain after the behavioral-equivalence oracle passes.
// A patch trying to set it must be HARD-refused (no evidence-file
// escape). Do not "fix" a failure here by reinstating the hand path —
// that re-opens "凭感觉盖章" (the v8.5 hole this redesign closes).
func TestGraphMergeObject_RejectsHandSetConfirmed(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	tool := graphMergeObjectTool()
	_, err := tool.Run(context.Background(), map[string]interface{}{
		"id":    "AnyObject",
		"patch": `{"status":"confirmed"}`,
	})
	if err == nil {
		t.Fatal("expected hard-refusal of hand-set status=confirmed")
	}
	if !strings.Contains(err.Error(), "NOT hand-settable") {
		t.Fatalf("error must explain the process-justice invariant, got: %v", err)
	}

	// Non-confirmed status patches are still allowed through this guard
	// (it must not over-block the normal declared→implementing flow).
	_, err2 := tool.Run(context.Background(), map[string]interface{}{
		"id":    "AnyObject",
		"patch": `{"status":"implementing"}`,
	})
	if err2 != nil && strings.Contains(err2.Error(), "NOT hand-settable") {
		t.Fatalf("status=implementing must NOT trip the confirmed guard, got: %v", err2)
	}
}
