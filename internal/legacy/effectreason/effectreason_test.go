package effectreason

import (
	"testing"

	"github.com/creator915/Koncept_OS/internal/legacy/effectgraph"
)

// graph: change → caller (return) → top (return); change → ledger (state).
func fixture() *effectgraph.Graph {
	g := effectgraph.New("b0")
	for _, id := range []string{"change", "caller", "top", "ledger", "auditor"} {
		g.AddNode(&effectgraph.Node{ID: id})
	}
	_ = g.AddEdge(effectgraph.Edge{From: "change", To: "caller", Kind: effectgraph.EdgeDataFlow, Channel: "return"})
	_ = g.AddEdge(effectgraph.Edge{From: "caller", To: "top", Kind: effectgraph.EdgeDataFlow, Channel: "return"})
	_ = g.AddEdge(effectgraph.Edge{From: "change", To: "ledger", Kind: effectgraph.EdgeState, Channel: "ledger"})
	_ = g.AddEdge(effectgraph.Edge{From: "ledger", To: "auditor", Kind: effectgraph.EdgeState, Channel: "ledger"})
	return g
}

func TestForwardSketch_TransitiveWithChannelAndDistance(t *testing.T) {
	imp := ForwardSketch(fixture(), "change", nil)
	got := map[string]Impact{}
	for _, i := range imp {
		got[i.Node] = i
	}
	if got["caller"].Via != ChannelReturn || got["caller"].Distance != 1 {
		t.Fatalf("caller via=%s dist=%d, want return/1", got["caller"].Via, got["caller"].Distance)
	}
	if got["top"].Distance != 2 {
		t.Fatalf("transitive node 'top' must be distance 2, got %d", got["top"].Distance)
	}
	if got["ledger"].Via != ChannelSharedState {
		t.Fatalf("ledger must be reached via shared-state, got %s", got["ledger"].Via)
	}
	if got["auditor"].Distance != 2 {
		t.Fatalf("auditor reached transitively through state, dist want 2 got %d", got["auditor"].Distance)
	}
}

// A "but that would be stupid" rule pruning shared-state cuts traversal
// PAST ledger: ledger is recorded with PrunedBy, auditor is NOT reached.
func TestForwardSketch_StupidRulePrunesAndIsRecorded(t *testing.T) {
	rule := StupidRule{
		ID: "R_ledger_internal", Statement: "ledger is never mutated outside the txn module",
		PrunesVia: ChannelSharedState, AssumptionID: "A_ledger_encapsulated",
	}
	imp := ForwardSketch(fixture(), "change", []StupidRule{rule})
	var ledger *Impact
	for i := range imp {
		if imp[i].Node == "ledger" {
			ledger = &imp[i]
		}
		if imp[i].Node == "auditor" {
			t.Fatalf("auditor must NOT be reached — the prune should cut past ledger")
		}
	}
	if ledger == nil || ledger.PrunedBy != "R_ledger_internal" {
		t.Fatalf("ledger must be recorded as pruned (audit, not dropped), got %+v", ledger)
	}
}

// Backward search must NOT honor stupid rules (the violated assumption
// may be exactly the cause).
func TestBackwardSketch_FindsCausesIgnoringStupidRules(t *testing.T) {
	b := BackwardSketch(fixture(), "auditor")
	reached := map[string]bool{}
	for _, i := range b {
		reached[i.Node] = true
	}
	if !reached["ledger"] || !reached["change"] {
		t.Fatalf("backward from auditor must reach ledger and change, got %v", reached)
	}
}

func TestSixStepInspection_MechanizesCallersAndStateMods(t *testing.T) {
	s := SixStepInspection(fixture(), "change")
	if len(s[1]) != 1 || s[1][0] != "change" {
		t.Fatalf("step1 must be the change node, got %v", s[1])
	}
	// step3: DataFlow out of change → caller.
	if len(s[3]) != 1 || s[3][0] != "caller" {
		t.Fatalf("step3 (value users) want [caller], got %v", s[3])
	}
	// step6: State out of change → ledger.
	if len(s[6]) != 1 || s[6][0] != "ledger" {
		t.Fatalf("step6 (global/static mods) want [ledger], got %v", s[6])
	}
	// steps 4/5 honestly empty (need type hierarchy / alias model).
	if s[4] != nil || s[5] != nil {
		t.Fatalf("steps 4/5 must be honestly empty, not faked: %v %v", s[4], s[5])
	}
}

func TestViolated_RefutedAssumptionTriggersDowngrade(t *testing.T) {
	r := StupidRule{ID: "R", AssumptionID: "A_ledger_encapsulated"}
	if Violated(r, map[string]bool{"A_other": true}) {
		t.Fatal("must not flag when the rule's assumption is not refuted")
	}
	if !Violated(r, map[string]bool{"A_ledger_encapsulated": true}) {
		t.Fatal("refuting the rule's assumption MUST flag it (违反触发降级)")
	}
}
