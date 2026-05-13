package workflow

import (
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"path/filepath"
	"testing"
)

// TestFindRoot covers the chain-spawn bug fix (v9.3): walking up the
// parent chain from any session always lands on the project root. This
// is what spawn_subagent uses to determine the parent for an auto-
// created session, fixing the v9.0.6/v9.2 sibling-becomes-descendant
// regression.
func TestFindRoot(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "sessions")
	mustStart := func(id, parent string) {
		t.Helper()
		if _, err := Start(dir, id, parent, "test", session.Input{}); err != nil {
			t.Fatalf("Start %s: %v", id, err)
		}
	}

	// Build a chain root → mid → leaf, plus a sibling at root level.
	mustStart("root", "")
	mustStart("mid", "root")
	mustStart("leaf", "mid")
	mustStart("sibling", "")

	cases := []struct {
		name    string
		from    string
		wantRoot string
	}{
		{"empty input", "", ""},
		{"root itself", "root", "root"},
		{"depth-1 child", "mid", "root"},
		{"depth-2 grandchild", "leaf", "root"},
		{"separate root", "sibling", "sibling"},
		{"unknown session", "ghost", "ghost"}, // best-effort: returns what we tried
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := persistence.FindRoot(dir, tc.from)
			if err != nil {
				t.Fatalf("persistence.FindRoot(%q): %v", tc.from, err)
			}
			if got != tc.wantRoot {
				t.Errorf("persistence.FindRoot(%q) = %q, want %q", tc.from, got, tc.wantRoot)
			}
		})
	}
}
