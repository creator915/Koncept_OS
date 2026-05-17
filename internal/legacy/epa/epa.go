// Package epa implements Error Propagation Analysis — the forward-
// reasoning half of the design document's black-box engine (屎山代码
// 维护Agent设计文档 v1.0 Part 4.1, Hiller/Jhumka/Suri). It is the dual
// of SBFL: SBFL reasons backward (failures → suspected cause), EPA
// reasons forward (a fault at M_src → does it reach M_tgt) — 设计文档
// Part 4.2.4 / 6.7.
//
// Two measures, verbatim from Part 4.1.1:
//
//	Error Permeability  P_e(M) = P(output erroneous | input erroneous)
//	Error Exposure      E_e(M) ≈ P(M itself emits an error under workload)
//
// Low P_e ⇒ M is a BARRIER (absorbs/validates errors); high P_e ⇒ M is
// a CONDUCTOR. The Oracle that comes out carries error_permeability =
// 1 - P_e on its OWN confidence dimension (设计文档 4.1.3 / 原则 C — no
// scalar merge).
//
// Layering (same honest split as package sbfl): this is the pure
// estimator over fault-injection OBSERVATIONS. Actually perturbing a
// running artifact along EffectGraph DataFlow edges is the injection
// adapter — the next layer, not a deferral. Part 4.3: SBFL first
// (cheap), EPA only on high-suspicion nodes (expensive) — so EPA is
// driven by sbfl.Rank output, wired by the caller.
package epa

import (
	"time"

	"github.com/creator915/Koncept_OS/internal/legacy/characterize"
	"github.com/creator915/Koncept_OS/internal/legacy/effectgraph"
)

// EdgeInjection is the observation for one DataFlow edge M_src→M_tgt
// (设计文档 Part 4.1.2): we perturbed M_src's output `Injections`
// times and M_tgt's output deviated `Deviations` times.
type EdgeInjection struct {
	From       string
	To         string
	Injections int
	Deviations int
}

// NodeInjection is the observation for one node's own error exposure:
// perturb M's input `Injections` times, M's output deviated
// `Deviations` times (proxy for "M is a bug source").
type NodeInjection struct {
	Node       string
	Injections int
	Deviations int
}

// Permeability is P_e for one edge plus the barrier/conductor verdict.
type Permeability struct {
	From, To string
	Pe       float64 // P(output err | input err)
	Barrier  bool    // Pe below BarrierThreshold ⇒ absorbs errors
}

// Exposure is E_e for one node.
type Exposure struct {
	Node string
	Ee   float64
}

// BarrierThreshold: at/below this P_e a module is treated as an error
// barrier (设计文档 4.1.1 "低 P_e：错误的屏障"). 0.5 is the neutral
// split; callers can re-derive with their own threshold via Classify.
const BarrierThreshold = 0.5

// EdgePermeability computes P_e for each injected edge. Zero injections
// ⇒ Pe 0 and NOT a barrier claim (honest: unmeasured ≠ safe).
func EdgePermeability(obs []EdgeInjection) []Permeability {
	out := make([]Permeability, 0, len(obs))
	for _, e := range obs {
		p := Permeability{From: e.From, To: e.To}
		if e.Injections > 0 {
			p.Pe = float64(e.Deviations) / float64(e.Injections)
			p.Barrier = p.Pe <= BarrierThreshold
		}
		out = append(out, p)
	}
	return out
}

// NodeExposure computes E_e for each node.
func NodeExposure(obs []NodeInjection) []Exposure {
	out := make([]Exposure, 0, len(obs))
	for _, n := range obs {
		ex := Exposure{Node: n.Node}
		if n.Injections > 0 {
			ex.Ee = float64(n.Deviations) / float64(n.Injections)
		}
		out = append(out, ex)
	}
	return out
}

// PlanInjections turns an EffectGraph into the edge list EPA should
// inject along: every DataFlow edge (设计文档 Part 4.1.2 "对 effect
// graph 上的每条 DataFlow edge"). Contract/State/Environmental edges
// are out of EPA's fault-injection scope by construction.
func PlanInjections(g *effectgraph.Graph) []EdgeInjection {
	df := g.DataFlowEdges()
	plan := make([]EdgeInjection, 0, len(df))
	for _, e := range df {
		plan = append(plan, EdgeInjection{From: e.From, To: e.To})
	}
	return plan
}

// OracleFromPermeability lifts an edge's P_e into a characterize.Oracle
// (设计文档 4.1.3). The property is "M_tgt is NOT contaminated when
// M_src errs"; confidence.error_permeability = 1 - P_e (low P_e ⇒ high
// confidence the barrier holds). Stays conditional on the runtime/env
// assumptions and references the injection record as evidence.
func OracleFromPermeability(p Permeability, evidenceRef string, conditionalOn []string) characterize.Oracle {
	verdict := "conductor (does not absorb upstream errors)"
	if p.Barrier {
		verdict = "barrier (absorbs/validates upstream errors)"
	}
	return characterize.Oracle{
		ID:       "oracle-epa-" + p.From + "->" + p.To,
		Property: p.To + " vs error from " + p.From + ": " + verdict + " — statistical (this fault-injection experiment), not a guarantee",
		Confidence: characterize.ConfidenceVector{
			ErrorPermeability: 1.0 - p.Pe,
		},
		ConditionalOn: conditionalOn,
		EvidenceRefs:  []string{evidenceRef},
		Source:        "EPA", // 设计文档 OracleSource::EPA
		EvaluatedAt:   time.Now().UTC(),
	}
}

// OracleFromExposure lifts a node's E_e into an Oracle on the
// error_exposure dimension (a proxy for "this node is a bug source").
func OracleFromExposure(e Exposure, evidenceRef string, conditionalOn []string) characterize.Oracle {
	return characterize.Oracle{
		ID:       "oracle-epa-exposure-" + e.Node,
		Property: e.Node + " error-exposure under the injected workload (proxy for being a fault origin)",
		Confidence: characterize.ConfidenceVector{
			ErrorExposure: e.Ee,
		},
		ConditionalOn: conditionalOn,
		EvidenceRefs:  []string{evidenceRef},
		Source:        "EPA",
		EvaluatedAt:   time.Now().UTC(),
	}
}
