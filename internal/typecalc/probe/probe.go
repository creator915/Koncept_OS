// Package probe implements §3 plan_probes / locate_fault — the probe-
// based fault-localization step that runs after an integration test fails.
// ProbePlanFromGraph derives the topologically-ordered list of intermediate
// attributes to observe; LocateFaultViaLLM (or the heuristic fallback)
// classifies which producer's output first diverged.
//
// Lives outside the typecalc core so the dependency on graph stays out of
// the foundation package. Imported by tools/typecalc.go for the
// typecalc_probe_plan agent tool.
package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/creator915/Koncept_OS/internal/graph"
	"github.com/creator915/Koncept_OS/internal/typecalc"
)

// ProbePoint identifies a single insertion point for the probe-based fault
// localization (§3 plan_probes). Each intermediate attribute is a probe
// point — observe its value when running the integration test, then walk
// backwards along the refinement order to localize the fault.
type ProbePoint struct {
	Attribute string   `json:"attribute"`
	Producer  string   `json:"producer"` // object id producing this attribute
	Consumers []string `json:"consumers"`
	// TopoIndex is the position of the producer in topological order.
	// Lower-indexed probes are upstream — observing them first lets you
	// short-circuit on early divergence.
	TopoIndex int `json:"topoIndex"`
}

// PlanData is the structured payload of a KindProbePlan TypedValue.
// Matches §3 ProbePlan<AttrPath[]>.
type PlanData struct {
	Points []ProbePoint `json:"points"`
}

// PlanFromGraph generates a ProbePlan by topologically sorting the graph's
// objects (producer-before-consumer). Only intermediate attributes — those
// both produced and consumed within the graph — become probe points. Pure
// inputs (consumed but never produced internally) and pure outputs
// (produced but never consumed) are skipped because observing them gives
// no localization information.
func PlanFromGraph(g *graph.Graph) (*PlanData, error) {
	if g == nil {
		return &PlanData{}, nil
	}
	produces := map[string][]string{} // attribute → producer object ids
	consumes := map[string][]string{} // attribute → consumer object ids
	objIDs := make([]string, 0, len(g.Objects))
	for id, obj := range g.Objects {
		objIDs = append(objIDs, id)
		for _, attr := range obj.Produces {
			produces[attr] = append(produces[attr], id)
		}
		for _, attr := range obj.Consumes {
			consumes[attr] = append(consumes[attr], id)
		}
	}
	// Topological sort: producer before consumer.
	indeg := make(map[string]int, len(objIDs))
	successors := make(map[string][]string, len(objIDs))
	for _, prodID := range objIDs {
		prodObj := g.Objects[prodID]
		for _, attr := range prodObj.Produces {
			for _, consID := range consumes[attr] {
				if consID == prodID {
					continue
				}
				successors[prodID] = append(successors[prodID], consID)
				indeg[consID]++
			}
		}
	}
	queue := []string{}
	for _, id := range objIDs {
		if indeg[id] == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	topoIndex := map[string]int{}
	order := 0
	for len(queue) > 0 {
		head := queue[0]
		queue = queue[1:]
		topoIndex[head] = order
		order++
		var nextRound []string
		for _, succ := range successors[head] {
			indeg[succ]--
			if indeg[succ] == 0 {
				nextRound = append(nextRound, succ)
			}
		}
		sort.Strings(nextRound)
		queue = append(queue, nextRound...)
	}
	for _, id := range objIDs {
		if _, ok := topoIndex[id]; !ok {
			topoIndex[id] = -1
		}
	}
	plan := &PlanData{}
	for attr := range produces {
		cs := consumes[attr]
		if len(cs) == 0 {
			continue
		}
		producers := produces[attr]
		sort.Slice(producers, func(i, j int) bool {
			return topoIndex[producers[i]] < topoIndex[producers[j]]
		})
		sort.Strings(cs)
		plan.Points = append(plan.Points, ProbePoint{
			Attribute: attr,
			Producer:  producers[0],
			Consumers: cs,
			TopoIndex: topoIndex[producers[0]],
		})
	}
	sort.Slice(plan.Points, func(i, j int) bool {
		if plan.Points[i].TopoIndex != plan.Points[j].TopoIndex {
			return plan.Points[i].TopoIndex < plan.Points[j].TopoIndex
		}
		return plan.Points[i].Attribute < plan.Points[j].Attribute
	})
	return plan, nil
}

// NewPlan wraps PlanData into a typecalc.TypedValue.
func NewPlan(d *PlanData) *typecalc.TypedValue {
	raw, _ := json.Marshal(d)
	return &typecalc.TypedValue{Kind: typecalc.KindProbePlan, Payload: string(raw)}
}

// DecodePlan extracts PlanData from a TypedValue.
func DecodePlan(tv *typecalc.TypedValue) (*PlanData, error) {
	if tv == nil || tv.Kind != typecalc.KindProbePlan {
		return nil, fmt.Errorf("DecodePlan: wrong kind (got %v)", tv)
	}
	var d PlanData
	if err := json.Unmarshal([]byte(tv.Payload), &d); err != nil {
		return nil, fmt.Errorf("decode PlanData: %w", err)
	}
	return &d, nil
}

// Observation is a single (probe-point, observed-value) record.
type Observation struct {
	Attribute string `json:"attribute"`
	Value     string `json:"value"`
	Note      string `json:"note,omitempty"`
}

// ResultData is the structured payload of a KindProbeResult.
type ResultData struct {
	Plan         []ProbePoint  `json:"plan"`
	Observations []Observation `json:"observations"`
}

// NewResult wraps observations into a typed value.
func NewResult(d *ResultData) *typecalc.TypedValue {
	raw, _ := json.Marshal(d)
	return &typecalc.TypedValue{Kind: typecalc.KindProbeResult, Payload: string(raw)}
}

// DecodeResult extracts ResultData.
func DecodeResult(tv *typecalc.TypedValue) (*ResultData, error) {
	if tv == nil || tv.Kind != typecalc.KindProbeResult {
		return nil, fmt.Errorf("DecodeResult: wrong kind (got %v)", tv)
	}
	var d ResultData
	if err := json.Unmarshal([]byte(tv.Payload), &d); err != nil {
		return nil, fmt.Errorf("decode ResultData: %w", err)
	}
	return &d, nil
}

// FaultLocatedDetail is the structured payload of a KindFaultLocated.
// Matches §3 FaultLocated<ModuleId, AttrPath, Reason>.
type FaultLocatedDetail struct {
	ModuleID string `json:"moduleId"`
	AttrPath string `json:"attrPath"`
	Reason   string `json:"reason"`
}

// NewFaultLocated wraps a fault-localization verdict.
func NewFaultLocated(moduleID, attrPath, reason string) *typecalc.TypedValue {
	d := FaultLocatedDetail{ModuleID: moduleID, AttrPath: attrPath, Reason: reason}
	raw, _ := json.Marshal(d)
	return &typecalc.TypedValue{Kind: typecalc.KindFaultLocated, Payload: string(raw)}
}

// LocateFaultFromObservations is the heuristic implementation of §3
// locate_fault used as a fallback when no LLM is available. It walks
// observations in topological order and returns the first attribute
// whose Note marks divergence.
func LocateFaultFromObservations(g *graph.Graph, plan *PlanData, obs *ResultData) *typecalc.TypedValue {
	if plan == nil || obs == nil {
		return NewFaultLocated("", "", "no plan or observations")
	}
	byAttr := map[string]Observation{}
	for _, o := range obs.Observations {
		byAttr[o.Attribute] = o
	}
	sort.Slice(plan.Points, func(i, j int) bool {
		return plan.Points[i].TopoIndex < plan.Points[j].TopoIndex
	})
	for _, p := range plan.Points {
		o, ok := byAttr[p.Attribute]
		if !ok {
			continue
		}
		if o.Note == "" {
			continue
		}
		return NewFaultLocated(p.Producer, p.Attribute, o.Note)
	}
	return NewFaultLocated("", "", "no observation flagged divergence")
}

// LocateFaultViaLLM is the strict §3 implementation of locate_fault: actor
// is ActorLLM. It assembles the inputs (descriptions of every probe
// point's producer + signatures + observed values) and asks the LLM to
// pick the offending module.
//
// If env or env.LLMInvoker is nil, falls back to LocateFaultFromObservations
// — keeping callers without LLM access (unit tests, headless tools)
// functional.
func LocateFaultViaLLM(ctx context.Context, env *typecalc.RuleEnv, g *graph.Graph,
	descriptions map[string]string, signatures map[string]string,
	plan *PlanData, obs *ResultData,
) (*typecalc.TypedValue, error) {
	if env == nil || env.LLMInvoker == nil {
		return LocateFaultFromObservations(g, plan, obs), nil
	}
	if plan == nil || obs == nil {
		return NewFaultLocated("", "", "no plan or observations"), nil
	}
	expected := typecalc.SumType{
		{Kind: typecalc.KindFaultLocated},
		{Kind: typecalc.KindCannotReproduce},
	}
	prompt := buildLocateFaultPrompt(plan, obs, descriptions, signatures)
	raw, err := env.LLMInvoker(ctx, env, prompt, expected)
	if err != nil {
		return nil, fmt.Errorf("LLM invoker for locate_fault: %w", err)
	}
	out, err := typecalc.ParseLLMOutput(raw, expected)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// buildLocateFaultPrompt formats a structured prompt for the locate_fault
// LLM invocation.
func buildLocateFaultPrompt(plan *PlanData, obs *ResultData,
	descriptions map[string]string, signatures map[string]string,
) string {
	var b strings.Builder
	b.WriteString("Rule: locate_fault\n")
	b.WriteString("Inputs: ProbeResult observations + module descriptions/signatures.\n")
	b.WriteString("Walk attribute paths in topological order. From the LAST observation\n")
	b.WriteString("backwards to the FIRST that diverges from expectation, identify the\n")
	b.WriteString("attribute whose value first goes wrong — its producer is the fault.\n\n")
	b.WriteString("Output format:\n")
	b.WriteString("  TYPE: FaultLocated\n  {\"moduleId\":\"...\",\"attrPath\":\"...\",\"reason\":\"...\"}\n")
	b.WriteString("Or, if no clean attribution is possible:\n")
	b.WriteString("  TYPE: CannotReproduce\n  {\"reason\":\"...\"}\n\n")
	b.WriteString("Probe points (topological order):\n")
	sort.Slice(plan.Points, func(i, j int) bool {
		return plan.Points[i].TopoIndex < plan.Points[j].TopoIndex
	})
	for _, p := range plan.Points {
		fmt.Fprintf(&b, "  [%d] attribute=%q producer=%q consumers=%v\n",
			p.TopoIndex, p.Attribute, p.Producer, p.Consumers)
		if d := descriptions[p.Producer]; d != "" {
			fmt.Fprintf(&b, "      description: %s\n", typecalc.Trim(d, 200))
		}
		if s := signatures[p.Producer]; s != "" {
			fmt.Fprintf(&b, "      signature:   %s\n", typecalc.Trim(s, 200))
		}
	}
	b.WriteString("\nObservations:\n")
	for _, o := range obs.Observations {
		note := o.Note
		if note == "" {
			note = "(no divergence flagged)"
		}
		fmt.Fprintf(&b, "  attribute=%q value=%s note=%q\n", o.Attribute, typecalc.Trim(o.Value, 80), note)
	}
	return b.String()
}
