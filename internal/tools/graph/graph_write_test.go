package graphtools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
)

// P1.1.4: graph-write — integrity (dedup / dangling-ref), in-graph
// auto-偏序 via parentAttr, active-layer targeting, and the ① no-hand-set
// invariant must all survive the active-layer rewrite of mutateGraph.

func freshGraphCwd(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	return dir
}

// Integrity: duplicate id is refused (pre-existing domain guard must
// still fire through the active-layer path).
func TestGraphWrite_DedupRejected(t *testing.T) {
	freshGraphCwd(t)
	_ = run(t, graphCreateAttributeTool(), map[string]interface{}{"id": "a1", "intent": "first"})
	_, err := graphCreateAttributeTool().Run(context.Background(),
		map[string]interface{}{"id": "a1", "intent": "dup"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate id must be refused, got: %v", err)
	}
}

// Integrity: linking a non-existent object/attribute is refused.
func TestGraphWrite_DanglingRefRejected(t *testing.T) {
	freshGraphCwd(t)
	_, err := graphLinkConsumeTool().Run(context.Background(),
		map[string]interface{}{"object": "NoObj", "attribute": "no_attr"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("dangling link must be refused, got: %v", err)
	}
}

// Auto-偏序: create-attribute with parentAttr records id <: parentAttr
// WITHOUT a separate graph_link_refine call.
func TestGraphWrite_ParentAttrAutoOrder(t *testing.T) {
	freshGraphCwd(t)
	_ = run(t, graphCreateAttributeTool(), map[string]interface{}{"id": "velocity", "intent": "coarse"})
	out := run(t, graphCreateAttributeTool(), map[string]interface{}{
		"id": "velocity_x", "intent": "refined", "parentAttr": "velocity",
	})
	if !strings.Contains(out, "auto-偏序") {
		t.Errorf("banner should note auto-偏序: %s", out)
	}
	g, err := persistence.LoadGraph(persistence.GraphDefaultPath)
	if err != nil {
		t.Fatal(err)
	}
	vx := g.Attributes["velocity_x"]
	if vx == nil {
		t.Fatal("velocity_x not created")
	}
	found := false
	for _, p := range vx.Refines {
		if p == "velocity" {
			found = true
		}
	}
	if !found {
		t.Errorf("velocity_x must auto-refine velocity, Refines=%v", vx.Refines)
	}
}

// Auto-偏序 atomicity: a bad parentAttr refuses creation AND leaves
// nothing on disk (no orphan attribute).
func TestGraphWrite_ParentAttrMissing_Atomic(t *testing.T) {
	freshGraphCwd(t)
	_, err := graphCreateAttributeTool().Run(context.Background(), map[string]interface{}{
		"id": "child", "intent": "x", "parentAttr": "no_such_parent",
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing parentAttr must refuse, got: %v", err)
	}
	g, _ := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if _, exists := g.Attributes["child"]; exists {
		t.Error("failed parentAttr link must NOT leave an orphan attribute on disk")
	}
}

// Active-layer: with a sub-session focused, writes land in
// K/expansions/<sid>/graph.json and DO NOT pollute the top-level graph.
func TestGraphWrite_ActiveLayerIsolation(t *testing.T) {
	dir := freshGraphCwd(t)
	sid := "s_child"
	sub := session.New(sid, "s_root", "expand", session.Input{})
	sub.ExpandsObject = "SubObj" // expansion session keyed on ExpandsObject
	if err := persistence.SaveSession(persistence.SessionDefaultDir, sub); err != nil {
		t.Fatal(err)
	}
	if err := persistence.SetFocus(persistence.SessionDefaultDir, sid); err != nil {
		t.Fatal(err)
	}

	_ = run(t, graphCreateObjectTool(), map[string]interface{}{
		"id": "SubObj", "intent": "child obj", "storyPoints": 2,
		"storyRationale": "single loop with one check",
	})

	// Landed in the expansion layer.
	if _, err := os.Stat(filepath.Join(dir, "K", "expansions", sid, "graph.json")); err != nil {
		t.Fatalf("write must land in the sub-session expansion: %v", err)
	}
	subG, _ := persistence.LoadExpansionGraphOrInit(sid)
	if _, ok := subG.Objects["SubObj"]; !ok {
		t.Error("SubObj missing from expansion graph")
	}
	// Top-level NOT polluted.
	topG, _ := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if _, leaked := topG.Objects["SubObj"]; leaked {
		t.Error("sub-session write leaked into the top-level K/graph.json")
	}
}

// ① regression: the no-hand-set invariant must still hard-refuse
// status=confirmed via graph_merge_object after the mutateGraph rewrite.
func TestGraphWrite_NoHandSetConfirmed_StillRefused(t *testing.T) {
	freshGraphCwd(t)
	_, err := graphMergeObjectTool().Run(context.Background(), map[string]interface{}{
		"id": "AnyObj", "patch": `{"status":"confirmed"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "NOT hand-settable") {
		t.Fatalf("① no-hand-set must survive P1.1.4, got: %v", err)
	}
}
