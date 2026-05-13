package graphtools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/creator915/Koncept_OS/internal/app/workflow"
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
)

// Tests for the withTempFocus closure that powers the optional session_id
// parameter on graph_merge_* tools. End-to-end tool invocation is hard to
// test without a live agent loop, but the focus save/restore behavior is
// the load-bearing part — and pure Go.

func chdirTempProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "K", "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	return dir
}

func TestWithTempFocus_NoOpWhenIDEmpty(t *testing.T) {
	chdirTempProject(t)
	if _, err := workflow.Create(persistence.SessionDefaultDir, "s_a", "", "", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if err := persistence.SetFocus(persistence.SessionDefaultDir, "s_a"); err != nil {
		t.Fatal(err)
	}
	restore, err := withTempFocus("")
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	cur, _ := persistence.GetFocus(persistence.SessionDefaultDir)
	if cur != "s_a" {
		t.Errorf("focus changed unexpectedly: %s", cur)
	}
}

func TestWithTempFocus_SwapsAndRestores(t *testing.T) {
	chdirTempProject(t)
	for _, id := range []string{"s_root", "s_child"} {
		if _, err := workflow.Create(persistence.SessionDefaultDir, id, "", "", session.Input{}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	if err := persistence.SetFocus(persistence.SessionDefaultDir, "s_root"); err != nil {
		t.Fatal(err)
	}

	restore, err := withTempFocus("s_child")
	if err != nil {
		t.Fatal(err)
	}
	mid, _ := persistence.GetFocus(persistence.SessionDefaultDir)
	if mid != "s_child" {
		t.Errorf("expected focus s_child during merge, got %s", mid)
	}
	restore()
	after, _ := persistence.GetFocus(persistence.SessionDefaultDir)
	if after != "s_root" {
		t.Errorf("expected focus restored to s_root, got %s", after)
	}
}

func TestWithTempFocus_RestoresEmptyWhenNoneFocused(t *testing.T) {
	chdirTempProject(t)
	if _, err := workflow.Create(persistence.SessionDefaultDir, "s_x", "", "", session.Input{}); err != nil {
		t.Fatal(err)
	}
	// Nothing focused — withTempFocus should still restore "no focus".
	restore, err := withTempFocus("s_x")
	if err != nil {
		t.Fatal(err)
	}
	restore()
	after, _ := persistence.GetFocus(persistence.SessionDefaultDir)
	if after != "" {
		t.Errorf("expected focus cleared, got %q", after)
	}
}
