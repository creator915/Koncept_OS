package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
)

// Handler 3 landing: spawn_subagent with expands_object must create the
// child session as an EXPANSION session (ExpandsObject set + empty
// K/expansions/<sid>/graph.json), so the layered-hypergraph lifecycle is
// actually driven by the real path-B per-object subagent flow (which is
// what was previously dormant).

type stubRunner struct{ got SubAgentRequest }

func (s *stubRunner) Run(ctx context.Context, req SubAgentRequest) (string, error) {
	s.got = req
	return "child done", nil
}

func chdirTmpTools(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	return dir
}

func topGraphWith(t *testing.T, objID string) {
	t.Helper()
	g := graph.NewGraph()
	g.Objects[objID] = graph.NewObject("defs/"+objID+".ts", objID)
	if err := persistence.SaveGraph(persistence.GraphDefaultPath, g); err != nil {
		t.Fatal(err)
	}
}

func TestSpawnSubagent_ExpandsObject_CreatesExpansionSession(t *testing.T) {
	dir := chdirTmpTools(t)
	topGraphWith(t, "Target")
	tool := subAgentTool(&stubRunner{})

	out, err := tool.Run(context.Background(), map[string]interface{}{
		"task":           "implement Target",
		"session_id":     "s_child",
		"expands_object": "Target",
	})
	if err != nil {
		t.Fatalf("spawn with valid expands_object must succeed: %v", err)
	}
	if !strings.Contains(out, "child done") {
		t.Errorf("expected child runner output, got: %s", out)
	}
	s, lerr := persistence.LoadSession(persistence.SessionDefaultDir, "s_child")
	if lerr != nil {
		t.Fatalf("child session must exist: %v", lerr)
	}
	if s.ExpandsObject != "Target" {
		t.Errorf("child session must be an EXPANSION session (ExpandsObject=Target), got %q", s.ExpandsObject)
	}
	if _, e := os.Stat(filepath.Join(dir, "K", "expansions", "s_child", "graph.json")); e != nil {
		t.Errorf("empty sub-hypergraph must be created: %v", e)
	}
}

func TestSpawnSubagent_ExpandsObject_UnknownRefused(t *testing.T) {
	dir := chdirTmpTools(t)
	topGraphWith(t, "Target")
	tool := subAgentTool(&stubRunner{})

	_, err := tool.Run(context.Background(), map[string]interface{}{
		"task": "x", "session_id": "s_bad", "expands_object": "NoSuchObj",
	})
	if err == nil || !strings.Contains(err.Error(), "not an object in the top hypergraph") {
		t.Fatalf("expanding a non-existent object must be refused, got: %v", err)
	}
	if persistence.ExistsSession(persistence.SessionDefaultDir, "s_bad") {
		t.Error("refused expansion must not leave a session behind")
	}
	if _, e := os.Stat(filepath.Join(dir, "K", "expansions", "s_bad")); e == nil {
		t.Error("refused expansion must not create a sub-graph dir")
	}
}

// Guard: a plain spawn (no expands_object) must STILL create an
// ordinary child session (NOT an expansion) — don't over-engage.
func TestSpawnSubagent_NoExpandsObject_PlainSession(t *testing.T) {
	chdirTmpTools(t)
	tool := subAgentTool(&stubRunner{})
	if _, err := tool.Run(context.Background(), map[string]interface{}{
		"task": "plain", "session_id": "s_plain",
	}); err != nil {
		t.Fatalf("plain spawn must succeed: %v", err)
	}
	s, lerr := persistence.LoadSession(persistence.SessionDefaultDir, "s_plain")
	if lerr != nil {
		t.Fatal(lerr)
	}
	if s.ExpandsObject != "" {
		t.Errorf("plain subagent must NOT be an expansion session, got ExpandsObject=%q", s.ExpandsObject)
	}
}
