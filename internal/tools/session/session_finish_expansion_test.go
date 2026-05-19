package sessiontools

import (
	"context"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
)

// P1.3.2: session-finish gate for an expansion session — sub-graph all
// confirmed + validate + children finished + produce/consume
// correspondence (偏序). Pass ⇒ parent.Expansion+confirmed. Fail ⇒ a
// LIST of reasons.

// finishFixture: top object Target (produces out_a) + declared attr
// out_a; root s_root; expansion session s_exp on Target (sub-graph
// empty, focused).
func finishFixture(t *testing.T) {
	t.Helper()
	startExpFixture(t) // top has object "Target"; s_root created
	g, _ := persistence.LoadGraph(persistence.GraphDefaultPath)
	g.Attributes["out_a"] = graph.NewAttribute("defs/out_a.ts", "parent output")
	tg := g.Objects["Target"]
	tg.Produces = []string{"out_a"}
	g.Objects["Target"] = tg
	if err := persistence.SaveGraph(persistence.GraphDefaultPath, g); err != nil {
		t.Fatal(err)
	}
	if _, err := sessionStartTool().Run(context.Background(), map[string]interface{}{
		"id": "s_exp", "parent": "s_root", "task": "expand", "expands_object": "Target",
	}); err != nil {
		t.Fatal(err)
	}
}

func finish(t *testing.T) (string, error) {
	t.Helper()
	return sessionStatusTool().Run(context.Background(),
		map[string]interface{}{"id": "s_exp", "status": "finished"})
}

func putSub(t *testing.T, mutate func(g *graph.Graph)) {
	t.Helper()
	sub, _ := persistence.LoadExpansionGraphOrInit("s_exp")
	mutate(sub)
	if err := persistence.SaveExpansionGraph("s_exp", sub); err != nil {
		t.Fatal(err)
	}
}

func TestExpansionFinish_RejectsUnconfirmedSubObject(t *testing.T) {
	finishFixture(t)
	putSub(t, func(g *graph.Graph) {
		g.Objects["Make"] = graph.NewObject("defs/Make.ts", "x") // status=declared
	})
	_, err := finish(t)
	if err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("unconfirmed sub-object must block finish, got: %v", err)
	}
	if !strings.Contains(err.Error(), "reason(s)") {
		t.Errorf("failure must be a reason LIST, got: %v", err)
	}
}

func TestExpansionFinish_RejectsMissingProduceCorrespondence(t *testing.T) {
	finishFixture(t)
	putSub(t, func(g *graph.Graph) {
		o := graph.NewObject("defs/Make.ts", "makes the wrong thing")
		o.Status = graph.StatusConfirmed
		o.Produces = []string{"something_else"}
		g.Objects["Make"] = o
	})
	_, err := finish(t)
	if err == nil || !strings.Contains(err.Error(), "产销对应") {
		t.Fatalf("missing produce-correspondence (偏序) must block finish, got: %v", err)
	}
}

func TestExpansionFinish_RejectsUnfinishedChild(t *testing.T) {
	finishFixture(t)
	putSub(t, func(g *graph.Graph) {
		o := graph.NewObject("defs/Make.ts", "ok")
		o.Status = graph.StatusConfirmed
		o.Produces = []string{"out_a"}
		g.Objects["Make"] = o
	})
	// A waiting child of s_exp.
	if _, err := sessionCreateTool().Run(context.Background(), map[string]interface{}{
		"id": "s_kid", "parent": "s_exp", "task": "child",
	}); err != nil {
		t.Fatal(err)
	}
	_, err := finish(t)
	if err == nil || !strings.Contains(err.Error(), "child session") ||
		!strings.Contains(err.Error(), "not finished") {
		t.Fatalf("unfinished child must block finish, got: %v", err)
	}
}

func TestExpansionFinish_SuccessPropagatesToParent(t *testing.T) {
	finishFixture(t)
	putSub(t, func(g *graph.Graph) {
		// Sub-hypergraph is self-contained: declare its own out_a so
		// reference-integrity is clean.
		g.Attributes["out_a"] = graph.NewAttribute("defs/out_a.ts", "child output")
		o := graph.NewObject("defs/Make.ts", "produces out_a")
		o.Status = graph.StatusConfirmed
		o.Produces = []string{"out_a"}
		g.Objects["Make"] = o
	})
	out, err := finish(t)
	if err != nil {
		t.Fatalf("clean expansion must finish: %v", err)
	}
	if !strings.Contains(out, "finished") {
		t.Errorf("expected finished status, got: %s", out)
	}
	top, _ := persistence.LoadGraph(persistence.GraphDefaultPath)
	tg := top.Objects["Target"]
	if tg.Expansion == nil || *tg.Expansion != "s_exp" {
		t.Errorf("parent must get Expansion=s_exp, got %v", tg.Expansion)
	}
	if tg.Status != graph.StatusConfirmed {
		t.Errorf("parent must be conferred confirmed by the gate, got %q", tg.Status)
	}
}

func TestExpansionFinish_ReasonsAreAList(t *testing.T) {
	finishFixture(t)
	// Two distinct violations: unconfirmed object AND no produce-corr.
	putSub(t, func(g *graph.Graph) {
		g.Objects["Bad"] = graph.NewObject("defs/Bad.ts", "declared, wrong output")
	})
	_, err := finish(t)
	if err == nil {
		t.Fatal("expected gate failure")
	}
	bullets := strings.Count(err.Error(), "\n  - ")
	if bullets < 2 {
		t.Errorf("reasons must be an enumerated LIST of ≥2, got %d in: %v", bullets, err)
	}
}
