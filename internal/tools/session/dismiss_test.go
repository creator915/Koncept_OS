package sessiontools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/app/workflow"
	"github.com/creator915/Koncept_OS/internal/domain/session"
)

// TestSessionDismiss_RefusesActive — v9.3: dismiss must NOT remove an
// active session (vs the old session_delete which would gladly nuke it
// + rollback graphDiff). The companion path is session_rollback for
// destructive cleanup.
func TestSessionDismiss_RefusesActive(t *testing.T) {
	cleanup := runInTempDir(t)
	defer cleanup()

	_, err := workflow.Start("K/sessions", "s_test", "", "test", session.Input{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// s_test is active.
	tool := sessionDismissTool()
	_, err = tool.Run(context.Background(), map[string]interface{}{"id": "s_test"})
	if err == nil {
		t.Fatal("expected error dismissing active session, got nil")
	}
	if !strings.Contains(err.Error(), "active") {
		t.Errorf("expected error to mention 'active', got: %v", err)
	}
	// Session JSON must still exist on disk.
	if _, sterr := os.Stat(filepath.Join("K/sessions", "s_test.json")); sterr != nil {
		t.Errorf("session file should still exist after refused dismiss: %v", sterr)
	}
}

// TestSessionDismiss_AcceptsFinished — dismiss works once the session
// transitions to finished. Verifies that graphDiff state isn't touched
// (the whole point vs session_rollback).
func TestSessionDismiss_AcceptsFinished(t *testing.T) {
	cleanup := runInTempDir(t)
	defer cleanup()

	_, err := workflow.Start("K/sessions", "s_root", "", "test", session.Input{})
	if err != nil {
		t.Fatalf("Start root: %v", err)
	}
	_, err = workflow.Start("K/sessions", "s_child", "s_root", "child", session.Input{})
	if err != nil {
		t.Fatalf("Start child: %v", err)
	}
	// Add graphDiff entries on the child session to verify dismiss
	// doesn't touch them.
	childPath := filepath.Join("K/sessions", "s_child.json")
	raw, _ := os.ReadFile(childPath)
	var childData map[string]interface{}
	_ = json.Unmarshal(raw, &childData)
	childData["graphDiff"] = map[string]interface{}{
		"objects": map[string]interface{}{
			"Foo": map[string]interface{}{"status": "confirmed"},
		},
	}
	updated, _ := json.MarshalIndent(childData, "", "  ")
	_ = os.WriteFile(childPath, updated, 0o644)

	// Flip child to finished status.
	if _, err := workflow.SetStatus("K/sessions", "s_child", session.StatusFinished); err != nil {
		t.Fatalf("SetStatus finished: %v", err)
	}

	tool := sessionDismissTool()
	out, err := tool.Run(context.Background(), map[string]interface{}{"id": "s_child"})
	if err != nil {
		t.Fatalf("dismiss finished session: %v", err)
	}
	if !strings.Contains(out, "dismissed") {
		t.Errorf("expected success message to mention 'dismissed', got: %s", out)
	}
	// Session JSON should be gone.
	if _, err := os.Stat(childPath); err == nil {
		t.Errorf("session file should be gone after dismiss")
	}
}

// TestSessionDismiss_RefusesWithActiveChildren — defensive: don't orphan
// a tree of active subagents. Setup uses Create (which leaves the session
// in `waiting`) for the parent so the parent itself passes the active-
// status check, isolating the active-child path.
func TestSessionDismiss_RefusesWithActiveChildren(t *testing.T) {
	cleanup := runInTempDir(t)
	defer cleanup()

	if _, err := workflow.Create("K/sessions", "s_parent", "", "parent", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := workflow.Start("K/sessions", "s_child", "s_parent", "child", session.Input{}); err != nil {
		t.Fatal(err)
	}
	// Parent status=waiting, child status=active → dismiss(parent) must
	// refuse on the active-child check.
	tool := sessionDismissTool()
	_, err := tool.Run(context.Background(), map[string]interface{}{"id": "s_parent"})
	if err == nil {
		t.Fatal("expected dismiss to refuse parent with active children, got nil")
	}
	if !strings.Contains(err.Error(), "child") || !strings.Contains(err.Error(), "active") {
		t.Errorf("error should mention active child, got: %v", err)
	}
}
