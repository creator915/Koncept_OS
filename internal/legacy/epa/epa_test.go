package epa

import (
	"math"
	"testing"

	"github.com/creator915/Koncept_OS/internal/legacy/effectgraph"
)

func TestEdgePermeability_BarrierVsConductor(t *testing.T) {
	r := EdgePermeability([]EdgeInjection{
		{From: "raw", To: "validate", Injections: 100, Deviations: 5},  // absorbs → barrier
		{From: "validate", To: "compute", Injections: 100, Deviations: 95}, // passes → conductor
		{From: "x", To: "y", Injections: 0, Deviations: 0},                  // unmeasured
	})
	if math.Abs(r[0].Pe-0.05) > 1e-9 || !r[0].Barrier {
		t.Fatalf("low P_e edge must be a barrier, got %+v", r[0])
	}
	if math.Abs(r[1].Pe-0.95) > 1e-9 || r[1].Barrier {
		t.Fatalf("high P_e edge must be a conductor, got %+v", r[1])
	}
	// Honest: zero injections ⇒ Pe 0 but NOT a barrier claim.
	if r[2].Pe != 0 || r[2].Barrier {
		t.Fatalf("unmeasured edge must not claim barrier (unmeasured != safe), got %+v", r[2])
	}
}

func TestNodeExposure(t *testing.T) {
	e := NodeExposure([]NodeInjection{
		{Node: "buggy", Injections: 50, Deviations: 40},
		{Node: "robust", Injections: 50, Deviations: 1},
	})
	if math.Abs(e[0].Ee-0.8) > 1e-9 {
		t.Fatalf("E_e(buggy) = %v, want 0.8", e[0].Ee)
	}
	if e[1].Ee >= e[0].Ee {
		t.Fatalf("robust node must have lower exposure than buggy")
	}
}

// EPA injects ONLY along DataFlow edges of the EffectGraph (设计文档
// Part 4.1.2). Contract/State edges are out of scope by construction.
func TestPlanInjections_OnlyDataFlowEdges(t *testing.T) {
	g := effectgraph.New("b0")
	g.AddNode(&effectgraph.Node{ID: "a"})
	g.AddNode(&effectgraph.Node{ID: "b"})
	g.AddNode(&effectgraph.Node{ID: "g"})
	_ = g.AddEdge(effectgraph.Edge{From: "a", To: "b", Kind: effectgraph.EdgeDataFlow})
	_ = g.AddEdge(effectgraph.Edge{From: "a", To: "g", Kind: effectgraph.EdgeState})
	plan := PlanInjections(g)
	if len(plan) != 1 || plan[0].From != "a" || plan[0].To != "b" {
		t.Fatalf("EPA must plan injections only on DataFlow edges, got %+v", plan)
	}
}

// P_e lifts to an Oracle on its OWN dimension as 1 - P_e (设计文档
// 4.1.3 / 原则 C): a strong barrier (low P_e) ⇒ high error_permeability
// confidence; nothing bleeds into other dimensions.
func TestOracleFromPermeability_ConfidenceIsOneMinusPe(t *testing.T) {
	o := OracleFromPermeability(Permeability{From: "raw", To: "validate", Pe: 0.05, Barrier: true},
		"ev-epa-1", []string{"A_runtime"})
	if o.Source != "EPA" {
		t.Fatalf("source must be EPA, got %q", o.Source)
	}
	if math.Abs(o.Confidence.ErrorPermeability-0.95) > 1e-9 {
		t.Fatalf("error_permeability must be 1-P_e=0.95, got %v", o.Confidence.ErrorPermeability)
	}
	if o.Confidence.SBFLSuspiciousness != 0 || o.Confidence.CoverageScore != 0 {
		t.Fatalf("EPA must not touch other confidence dimensions, got %+v", o.Confidence)
	}
	if len(o.ConditionalOn) != 1 || len(o.EvidenceRefs) != 1 {
		t.Fatalf("Oracle must stay conditional + evidence-backed, got %+v", o)
	}
}
