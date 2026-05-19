package sessiontools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
)

// P1.3.1: session_start(id, parent, expands_object) — creates the empty
// sub-hypergraph, binds the active layer, records the expansion link,
// and refuses an unknown object / duplicate id.

func startExpFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	// Top graph with one object that can be expanded.
	g := graph.NewGraph()
	g.Objects["Target"] = graph.NewObject("defs/Target.ts", "to expand")
	if err := persistence.SaveGraph(persistence.GraphDefaultPath, g); err != nil {
		t.Fatal(err)
	}
	// A root session to parent the expansion session.
	if _, err := sessionCreateTool().Run(context.Background(),
		map[string]interface{}{"id": "s_root", "parent": "", "task": "root"}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSessionStart_Expansion_CreatesSubGraphAndBindsActiveLayer(t *testing.T) {
	dir := startExpFixture(t)
	out, err := sessionStartTool().Run(context.Background(), map[string]interface{}{
		"id": "s_exp", "parent": "s_root", "task": "expand Target",
		"expands_object": "Target",
	})
	if err != nil {
		t.Fatalf("session_start with valid expands_object must succeed: %v", err)
	}
	if !strings.Contains(out, "expands=Target") {
		t.Errorf("banner should note the expansion: %s", out)
	}
	// (1) empty sub-hypergraph file created.
	subPath := filepath.Join(dir, "K", "expansions", "s_exp", "graph.json")
	if _, statErr := os.Stat(subPath); statErr != nil {
		t.Fatalf("empty sub-hypergraph must be created at %s: %v", subPath, statErr)
	}
	// (2) active layer switched to the sub-graph (focus is s_exp).
	if got := persistence.ActiveGraphPathFromFocus(); got != persistence.ExpansionGraphPath("s_exp") {
		t.Errorf("active graph must switch to the expansion, got %q", got)
	}
	// (3) expansion link recorded on the session.
	s, lerr := persistence.LoadSession(persistence.SessionDefaultDir, "s_exp")
	if lerr != nil || s.ExpandsObject != "Target" {
		t.Errorf("session must record ExpandsObject=Target, got %+v err=%v", s, lerr)
	}
}

func TestSessionStart_Expansion_UnknownObjectRefused(t *testing.T) {
	dir := startExpFixture(t)
	_, err := sessionStartTool().Run(context.Background(), map[string]interface{}{
		"id": "s_bad", "parent": "s_root", "task": "x", "expands_object": "NoSuchObj",
	})
	if err == nil || !strings.Contains(err.Error(), "not an object in the top hypergraph") {
		t.Fatalf("expanding a non-existent object must be refused, got: %v", err)
	}
	// Session must NOT have been created (precondition checked first).
	if persistence.ExistsSession(persistence.SessionDefaultDir, "s_bad") {
		t.Error("refused expansion must not leave a session behind")
	}
	if _, e := os.Stat(filepath.Join(dir, "K", "expansions", "s_bad")); e == nil {
		t.Error("refused expansion must not create a sub-graph dir")
	}
}

func TestSessionStart_Expansion_DuplicateIDRefused(t *testing.T) {
	startExpFixture(t)
	args := map[string]interface{}{
		"id": "s_dup", "parent": "s_root", "task": "x", "expands_object": "Target",
	}
	if _, err := sessionStartTool().Run(context.Background(), args); err != nil {
		t.Fatalf("first start must succeed: %v", err)
	}
	if _, err := sessionStartTool().Run(context.Background(), args); err == nil ||
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate session id must be refused, got: %v", err)
	}
}
