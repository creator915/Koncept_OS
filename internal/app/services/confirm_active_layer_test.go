package services

import (
	"fmt"
	"os"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
)

// P1.2.3: Confirmed writes the CURRENT session's layer. MarkConfirmed
// flows through services.mutateGraph, so these guard that a sub-session
// confirm lands in the expansion and NEVER pollutes the top-level graph
// (the highest-risk migration per the rollout design doc, 风险#1).

func chdirTmp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	return dir
}

// Sub-session focused → confirm-style mutation lands in the expansion,
// top-level K/graph.json is NOT polluted.
func TestConfirm_SubSessionWrite_DoesNotPolluteTop(t *testing.T) {
	chdirTmp(t)
	sid := "s_deliv"
	sub := session.New(sid, "s_root", "expand", session.Input{})
	sub.ExpandsObject = "Deliv" // expansion session keyed on ExpandsObject
	if err := persistence.SaveSession(persistence.SessionDefaultDir, sub); err != nil {
		t.Fatal(err)
	}
	if err := persistence.SetFocus(persistence.SessionDefaultDir, sid); err != nil {
		t.Fatal(err)
	}

	// Exactly what MarkConfirmed does: mutate the object's status to
	// confirmed through services.mutateGraph.
	err := mutateGraph(func(g *graph.Graph) error {
		o := graph.NewObject("defs/Deliv.ts", "deliverable")
		o.Status = graph.StatusConfirmed
		g.Objects["Deliv"] = o
		return nil
	})
	if err != nil {
		t.Fatalf("mutateGraph: %v", err)
	}

	subG, _ := persistence.LoadExpansionGraphOrInit(sid)
	if o, ok := subG.Objects["Deliv"]; !ok || o.Status != graph.StatusConfirmed {
		t.Fatalf("confirmed object must live in the sub-session expansion, got %+v", subG.Objects)
	}
	topG, _ := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if _, leaked := topG.Objects["Deliv"]; leaked {
		t.Error("sub-session confirm POLLUTED the top-level K/graph.json (风险#1 regression)")
	}
}

// No focus / root → writes top-level, byte-identical to pre-layered
// behaviour (P0.2 取舍#2, the "加层不砸旧" guard).
func TestConfirm_NoFocus_WritesTopLevel(t *testing.T) {
	dir := chdirTmp(t)
	err := mutateGraph(func(g *graph.Graph) error {
		g.Objects["TopObj"] = graph.NewObject("defs/TopObj.ts", "top")
		return nil
	})
	if err != nil {
		t.Fatalf("mutateGraph: %v", err)
	}
	topG, _ := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if _, ok := topG.Objects["TopObj"]; !ok {
		t.Error("no-focus write must land in the top-level graph")
	}
	if _, err := os.Stat(fmt.Sprintf("%s/K/expansions", dir)); err == nil {
		t.Error("no expansion dir should be created when no sub-session is focused")
	}
}
