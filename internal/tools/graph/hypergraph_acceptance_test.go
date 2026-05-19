package graphtools

import (
	"os"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
)

// P1.1.GATE end-to-end (read-only): expand a node → focus the
// sub-session → create a node in the sub-hypergraph (graph-write lands
// in the expansion layer) → BEFORE any rollback, graph_show_expanded /
// graph_validate_deep see across the layer boundary, and the top-level
// graph is NOT polluted. Exercises P1.1.1+1.1.2+1.1.3+1.1.4 together.
func TestHypergraph_ExpandCreateRead_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	// Top graph: object Parent, expanded into session s_phys.
	sid := "s_phys"
	top := graph.NewGraph()
	p := graph.NewObject("defs/Parent.ts", "parent")
	p.Expansion = &sid
	top.Objects["Parent"] = p
	if err := persistence.SaveGraph(persistence.GraphDefaultPath, top); err != nil {
		t.Fatal(err)
	}

	// Focus the sub-session so graph-write targets the expansion.
	sub := session.New(sid, "s_root", "expand Parent", session.Input{})
	sub.ExpandsObject = "Parent" // expansion session keyed on ExpandsObject
	if err := persistence.SaveSession(persistence.SessionDefaultDir, sub); err != nil {
		t.Fatal(err)
	}
	if err := persistence.SetFocus(persistence.SessionDefaultDir, sid); err != nil {
		t.Fatal(err)
	}

	// Create a node inside the sub-hypergraph.
	_ = run(t, graphCreateObjectTool(), map[string]interface{}{
		"id": "ChildStep", "intent": "fine-grained step", "storyPoints": 2,
		"storyRationale": "single bounded loop",
	})

	// It landed in the expansion, NOT the top-level graph.
	subG, _ := persistence.LoadExpansionGraphOrInit(sid)
	if _, ok := subG.Objects["ChildStep"]; !ok {
		t.Fatal("ChildStep not written to the expansion layer")
	}
	topBack, _ := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if _, leaked := topBack.Objects["ChildStep"]; leaked {
		t.Fatal("expansion write polluted the top-level graph")
	}

	// Read across the boundary (pre-rollback).
	se := run(t, graphShowExpandedTool(), map[string]interface{}{"id": "Parent"})
	if !strings.Contains(se, "object Parent") ||
		!strings.Contains(se, "↳ expansion: "+sid) ||
		!strings.Contains(se, "object ChildStep") {
		t.Fatalf("show-expanded must cross into the sub-hypergraph: %s", se)
	}
	vd := run(t, graphValidateDeepTool(), map[string]interface{}{})
	if !strings.Contains(vd, "[top]") || !strings.Contains(vd, "[expansion "+sid+"]") {
		t.Fatalf("validate-deep must cover both layers: %s", vd)
	}
}
