package typecalc

import (
	"testing"

	"github.com/creator915/Koncept_OS/internal/graph"
)

func TestProbePlanFromGraph_TopologicalOrder(t *testing.T) {
	g := graph.NewGraph()
	g.Attributes["raw"] = graph.NewAttribute("defs/raw.ts", "raw input")
	g.Attributes["mid"] = graph.NewAttribute("defs/mid.ts", "intermediate")
	g.Attributes["out"] = graph.NewAttribute("defs/out.ts", "final output")

	stage1 := graph.NewObject("defs/Stage1.ts", "raw → mid")
	stage1.Consumes = []string{"raw"}
	stage1.Produces = []string{"mid"}
	g.Objects["Stage1"] = stage1

	stage2 := graph.NewObject("defs/Stage2.ts", "mid → out")
	stage2.Consumes = []string{"mid"}
	stage2.Produces = []string{"out"}
	g.Objects["Stage2"] = stage2

	plan, err := ProbePlanFromGraph(g)
	if err != nil {
		t.Fatal(err)
	}
	// "mid" is the only intermediate (produced + consumed inside the graph).
	// "raw" has no producer in the graph, "out" has no consumer.
	if len(plan.Points) != 1 {
		t.Fatalf("expected 1 probe point, got %d: %+v", len(plan.Points), plan.Points)
	}
	if plan.Points[0].Attribute != "mid" {
		t.Fatalf("expected mid, got %q", plan.Points[0].Attribute)
	}
	if plan.Points[0].Producer != "Stage1" {
		t.Fatalf("expected producer Stage1, got %q", plan.Points[0].Producer)
	}
	if plan.Points[0].TopoIndex != 0 {
		t.Fatalf("Stage1 should be index 0, got %d", plan.Points[0].TopoIndex)
	}
}

func TestProbePlanFromGraph_EmptyGraph(t *testing.T) {
	plan, err := ProbePlanFromGraph(graph.NewGraph())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Points) != 0 {
		t.Fatalf("empty graph: %+v", plan)
	}
}

func TestLocateFaultFromObservations_Heuristic(t *testing.T) {
	g := graph.NewGraph()
	plan := &ProbePlanData{
		Points: []ProbePoint{
			{Attribute: "a", Producer: "P1", TopoIndex: 0},
			{Attribute: "b", Producer: "P2", TopoIndex: 1},
		},
	}
	obs := &ProbeResultData{
		Observations: []ProbeObservation{
			{Attribute: "a", Value: "1", Note: ""},
			{Attribute: "b", Value: "999", Note: "diverges from expected 100"},
		},
	}
	out := LocateFaultFromObservations(g, plan, obs)
	if out.Kind != KindFaultLocated {
		t.Fatalf("got %s", out.Tag())
	}
	d, _ := DecodeProbePlan(NewProbePlan(plan)) // smoke
	_ = d
}
