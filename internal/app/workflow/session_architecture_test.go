package workflow

import (
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"path/filepath"
	"strings"
	"testing"
)

// Fix 4 (architecture-non-empty): root sessions need a non-empty
// Architecture description before they can finish.

func TestSetArchitecture_PersistsAndLoadsBack(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "K", "sessions")
	if _, err := Create(sessionDir, "s_root", "", "", session.Input{}); err != nil {
		t.Fatal(err)
	}
	desc := "Sub-modules:\n- A: input parsing\n- B: rendering\n\nIntermediate vars:\n- raw_buf: bytes from stdin"
	if _, err := SetArchitecture(sessionDir, "s_root", desc); err != nil {
		t.Fatal(err)
	}
	loaded, err := persistence.LoadSession(sessionDir, "s_root")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Output.Architecture != desc {
		t.Errorf("architecture not persisted; got %q, want %q", loaded.Output.Architecture, desc)
	}
}

func TestGate_ArchitectureNonEmpty_FailsOnEmpty(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "K", "sessions")
	if _, err := Create(sessionDir, "s_root", "", "", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_root", session.StatusActive); err != nil {
		t.Fatal(err)
	}
	cwdRestore := mustChdir(t, dir)
	defer cwdRestore()

	r, _ := CheckGate(sessionDir, "", "", "s_root")
	found := false
	for _, issue := range r.Issues {
		if strings.Contains(issue, "architecture-non-empty") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected [architecture-non-empty] for empty architecture, got: %v", r.Issues)
	}
}

func TestGate_ArchitectureNonEmpty_PassesWhenSet(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "K", "sessions")
	if _, err := Create(sessionDir, "s_root", "", "", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_root", session.StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := SetArchitecture(sessionDir, "s_root", "non-empty"); err != nil {
		t.Fatal(err)
	}
	cwdRestore := mustChdir(t, dir)
	defer cwdRestore()

	r, _ := CheckGate(sessionDir, "", "", "s_root")
	for _, issue := range r.Issues {
		if strings.Contains(issue, "architecture-non-empty") {
			t.Fatalf("rule should not fire when architecture is set, got: %v", r.Issues)
		}
	}
}

func TestGate_ArchitectureNonEmpty_OnlyFiresForRoot(t *testing.T) {
	// Non-root sessions shouldn't be required to have architecture.
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "K", "sessions")
	if _, err := Create(sessionDir, "s_root", "", "", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(sessionDir, "s_child", "s_root", "", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(sessionDir, "s_child", session.StatusActive); err != nil {
		t.Fatal(err)
	}
	cwdRestore := mustChdir(t, dir)
	defer cwdRestore()

	r, _ := CheckGate(sessionDir, "", "", "s_child")
	for _, issue := range r.Issues {
		if strings.Contains(issue, "architecture-non-empty") {
			t.Fatalf("non-root should not require architecture, got: %v", r.Issues)
		}
	}
}
