package sessiontools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
)

// P1.3.3: session-rollback for an expansion session — delete the whole
// K/expansions/<id>/, revert parent to expansion=null/declared, cascade
// to child expansion sessions, and pollute NOTHING else in the top graph.

func rollback(t *testing.T, id string) {
	t.Helper()
	if _, err := sessionRollbackTool().Run(context.Background(),
		map[string]interface{}{"id": id}); err != nil {
		t.Fatalf("rollback %s: %v", id, err)
	}
}

func topJSON(t *testing.T) string {
	t.Helper()
	g, err := persistence.LoadGraph(persistence.GraphDefaultPath)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(g)
	return string(b)
}

func TestExpansionRollback_RemovesSubGraphAndRevertsParent(t *testing.T) {
	finishFixture(t)
	putSub(t, func(g *graph.Graph) {
		g.Attributes["out_a"] = graph.NewAttribute("defs/out_a.ts", "child output")
		o := graph.NewObject("defs/Make.ts", "produces out_a")
		o.Status = graph.StatusConfirmed
		o.Produces = []string{"out_a"}
		g.Objects["Make"] = o
	})
	if _, err := finish(t); err != nil {
		t.Fatalf("precondition finish: %v", err)
	}
	// Sanity: finish propagated.
	if top, _ := persistence.LoadGraph(persistence.GraphDefaultPath); top.Objects["Target"].Expansion == nil {
		t.Fatal("precondition: finish should have set Target.Expansion")
	}

	rollback(t, "s_exp")

	if _, err := os.Stat(filepath.Join("K", "expansions", "s_exp")); !os.IsNotExist(err) {
		t.Errorf("K/expansions/s_exp must be deleted, stat err=%v", err)
	}
	top, _ := persistence.LoadGraph(persistence.GraphDefaultPath)
	tg := top.Objects["Target"]
	if tg.Expansion != nil {
		t.Errorf("parent Expansion must be cleared, got %v", *tg.Expansion)
	}
	if tg.Status != graph.StatusDeclared {
		t.Errorf("parent must revert to declared, got %q", tg.Status)
	}
}

// Zero-pollution: the top graph after rollback must be byte-identical to
// its pre-expansion snapshot (full, exact revert — only the two parent
// fields ever moved, and they moved back).
func TestExpansionRollback_NoParentPollution(t *testing.T) {
	finishFixture(t)
	snapshot := topJSON(t) // Target declared, Expansion nil, produces out_a

	putSub(t, func(g *graph.Graph) {
		g.Attributes["out_a"] = graph.NewAttribute("defs/out_a.ts", "child output")
		o := graph.NewObject("defs/Make.ts", "produces out_a")
		o.Status = graph.StatusConfirmed
		o.Produces = []string{"out_a"}
		g.Objects["Make"] = o
	})
	if _, err := finish(t); err != nil {
		t.Fatalf("precondition finish: %v", err)
	}
	rollback(t, "s_exp")

	if got := topJSON(t); got != snapshot {
		t.Errorf("top graph not exactly reverted:\n pre = %s\n post= %s", snapshot, got)
	}
}

func TestExpansionRollback_CascadesToChildExpansion(t *testing.T) {
	startExpFixture(t) // top has Target; s_root created
	// Add a second expandable object.
	g, _ := persistence.LoadGraph(persistence.GraphDefaultPath)
	g.Objects["Target2"] = graph.NewObject("defs/Target2.ts", "second")
	if err := persistence.SaveGraph(persistence.GraphDefaultPath, g); err != nil {
		t.Fatal(err)
	}
	if _, err := sessionStartTool().Run(context.Background(), map[string]interface{}{
		"id": "s_exp", "parent": "s_root", "task": "p", "expands_object": "Target",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessionStartTool().Run(context.Background(), map[string]interface{}{
		"id": "s_kid", "parent": "s_exp", "task": "c", "expands_object": "Target2",
	}); err != nil {
		t.Fatal(err)
	}
	// Both sub-graph dirs exist.
	for _, d := range []string{"s_exp", "s_kid"} {
		if _, err := os.Stat(filepath.Join("K", "expansions", d)); err != nil {
			t.Fatalf("precondition: %s expansion dir missing: %v", d, err)
		}
	}

	rollback(t, "s_exp") // recursion rolls back s_kid first, then s_exp

	for _, d := range []string{"s_exp", "s_kid"} {
		if _, err := os.Stat(filepath.Join("K", "expansions", d)); !os.IsNotExist(err) {
			t.Errorf("cascade must delete K/expansions/%s, stat err=%v", d, err)
		}
		if persistence.ExistsSession(persistence.SessionDefaultDir, d) {
			t.Errorf("cascade must delete session %s", d)
		}
	}
}
