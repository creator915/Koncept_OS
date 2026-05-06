package session

import (
	"path/filepath"
	"testing"
)

func TestStart_AtomicCreateActiveFocus(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "K", "sessions")

	s, err := Start(dir, "s_x", "", "test task", Input{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if s.Status != StatusActive {
		t.Errorf("expected status active after Start, got %s", s.Status)
	}
	cur, _ := GetFocus(dir)
	if cur != "s_x" {
		t.Errorf("expected focus on s_x after Start, got %q", cur)
	}
}

func TestStart_DuplicateFails(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "K", "sessions")
	if _, err := Start(dir, "s_x", "", "task", Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(dir, "s_x", "", "task", Input{}); err == nil {
		t.Errorf("starting an already-existing session should error")
	}
}

func TestStart_BadParentRollsBack(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "K", "sessions")
	_, err := Start(dir, "s_child", "s_missing", "task", Input{})
	if err == nil {
		t.Errorf("Start with bad parent should error")
	}
	if Exists(dir, "s_child") {
		t.Errorf("failed Start should have rolled back; s_child should not exist")
	}
	cur, _ := GetFocus(dir)
	if cur != "" {
		t.Errorf("focus should be unset after failed Start, got %q", cur)
	}
}
