package sessiontools

import (
	"context"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
)

// P1.3.GATE end-to-end: session-start (expand) → graph-write a node into
// the sub-hypergraph → mark it confirmed (stands in for the
// compile/test/Confirmed chain, exercised separately in P1.2) →
// session-finish (gate passes, parent propagated) → a SECOND expansion
// rolled back leaves the top graph unpolluted. One test ties
// P1.1+P1.2+P1.3 together.
func TestExpansionLifecycle_StartCreateConfirmFinishRollback(t *testing.T) {
	startExpFixture(t) // top: object Target; s_root
	// Two expandable parent objects.
	g, _ := persistence.LoadGraph(persistence.GraphDefaultPath)
	g.Attributes["out_a"] = graph.NewAttribute("defs/out_a.ts", "Target output")
	tg := g.Objects["Target"]
	tg.Produces = []string{"out_a"}
	g.Objects["Target"] = tg
	g.Objects["Target2"] = graph.NewObject("defs/Target2.ts", "second")
	if err := persistence.SaveGraph(persistence.GraphDefaultPath, g); err != nil {
		t.Fatal(err)
	}
	snapshotTarget2 := func() string {
		x, _ := persistence.LoadGraph(persistence.GraphDefaultPath)
		b := x.Objects["Target2"]
		return b.Status + "|" + func() string {
			if b.Expansion == nil {
				return "<nil>"
			}
			return *b.Expansion
		}()
	}
	before2 := snapshotTarget2()

	// start s_exp expanding Target; graph-write a node into the sub-graph
	// via the focused active layer (P1.1.2/P1.1.4 binding).
	if _, err := sessionStartTool().Run(context.Background(), map[string]interface{}{
		"id": "s_exp", "parent": "s_root", "task": "expand Target", "expands_object": "Target",
	}); err != nil {
		t.Fatal(err)
	}
	putSub(t, func(sg *graph.Graph) {
		sg.Attributes["out_a"] = graph.NewAttribute("defs/out_a.ts", "child out")
		o := graph.NewObject("defs/Make.ts", "produces out_a")
		o.Status = graph.StatusConfirmed // stands in for compile→test→Confirmed
		o.Produces = []string{"out_a"}
		sg.Objects["Make"] = o
	})
	out, err := sessionStatusTool().Run(context.Background(),
		map[string]interface{}{"id": "s_exp", "status": "finished"})
	if err != nil {
		t.Fatalf("expansion finish must pass the gate: %v", err)
	}
	if !strings.Contains(out, "finished") {
		t.Errorf("unexpected finish output: %s", out)
	}
	top, _ := persistence.LoadGraph(persistence.GraphDefaultPath)
	if top.Objects["Target"].Expansion == nil || *top.Objects["Target"].Expansion != "s_exp" {
		t.Fatal("session-finish must propagate Expansion to the parent")
	}
	if top.Objects["Target"].Status != graph.StatusConfirmed {
		t.Fatal("session-finish must confer confirmed on the parent")
	}

	// A second expansion that we roll back — Target2 must be untouched.
	if _, err := sessionStartTool().Run(context.Background(), map[string]interface{}{
		"id": "s_exp2", "parent": "s_root", "task": "expand Target2", "expands_object": "Target2",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessionRollbackTool().Run(context.Background(),
		map[string]interface{}{"id": "s_exp2"}); err != nil {
		t.Fatal(err)
	}
	if after2 := snapshotTarget2(); after2 != before2 {
		t.Errorf("rolled-back expansion polluted Target2: before=%q after=%q", before2, after2)
	}
}
