package router

import "testing"

// P2.3: the frozen observed type-flow — match / no-match / no-ambiguity.

func TestFlow_Match_FrozenEdges(t *testing.T) {
	ok := [][2]string{
		{"StartConfirm", "Compiled<Object>"},
		{"StartConfirm", "CompileError<Object>"},
		{"StartConfirm", "Obstacle<Object,Reason>"},
		{"Compiled<Object>", "Described<Object>"},
		{"Described<Object>", "Synthesized<Object>"},
		{"Described<Object>", "Tested<Object,Pass>"}, // HTML fast-path branch
		{"Synthesized<Object>", "Tested<Object,Pass>"},
		{"Tested<Object,Pass>", "Reviewed<Object,Pass>"},
		{"Tested<Object,Pass>", "ReviewFailed<Object>"},
		{"Reviewed<Object,Pass>", "Confirmed<Object>"},
		{"Request<Object,Test>", "TestError<Object>"},
		{"Request<Object,Review>", "Reviewed<Object,Pass>"},
	}
	for _, e := range ok {
		if !FlowAllows(e[0], e[1]) {
			t.Errorf("frozen edge must MATCH: %s → %s", e[0], e[1])
		}
	}
}

func TestFlow_NoMatch_IllegalTransitions(t *testing.T) {
	bad := [][2]string{
		{"StartConfirm", "Confirmed<Object>"},        // cannot skip the chain
		{"Compiled<Object>", "StartConfirm"},         // no going backward
		{"Reviewed<Object,Pass>", "Compiled<Object>"},// no backward
		{"Confirmed<Object>", "Obstacle<Object,Reason>"}, // terminal has no edge
		{"Obstacle<Object,Reason>", "Compiled<Object>"},  // terminal has no edge
		{"Described<Object>", "Confirmed<Object>"},   // cannot skip test+review
		{"Unknown<Type>", "Compiled<Object>"},        // unknown source
		{"Compiled<Object>", "Whatever"},             // unknown target
	}
	for _, e := range bad {
		if FlowAllows(e[0], e[1]) {
			t.Errorf("illegal transition must NOT match: %s → %s", e[0], e[1])
		}
	}
}

func TestFlow_Terminals_HaveNoSuccessors(t *testing.T) {
	for _, term := range []string{"Confirmed<Object>", "Obstacle<Object,Reason>"} {
		if !FlowIsTerminal(term) {
			t.Errorf("%s must be terminal", term)
		}
		if s := FlowSuccessors(term); s != nil {
			t.Errorf("terminal %s must have no successors, got %v", term, s)
		}
	}
	if FlowIsTerminal("Compiled<Object>") {
		t.Error("Compiled is not terminal")
	}
}

func TestFlow_NoAmbiguity_SingleRulePerSource(t *testing.T) {
	if !FlowSourcesUnique() {
		t.Fatal("ambiguity: a source type appears in more than one flow rule (violates single-dispatch)")
	}
}

func TestFlow_EveryNonTerminalSourceProgresses(t *testing.T) {
	for _, r := range ObservedTypeFlow {
		if len(r.To) == 0 {
			t.Errorf("source %s has no successors but is not terminal", r.From)
		}
		// Every rule must offer an Obstacle escape (fail-closed routing).
		hasObstacle := false
		for _, to := range r.To {
			if to == "Obstacle<Object,Reason>" {
				hasObstacle = true
			}
		}
		if !hasObstacle {
			t.Errorf("source %s must always allow an Obstacle branch", r.From)
		}
	}
}

// The declared happy spine must be entirely composed of real frozen
// edges — it can never drift from ObservedTypeFlow.
func TestFlow_CanonicalSpineIsAllFrozenEdges(t *testing.T) {
	if len(CanonicalSpine) < 5 {
		t.Fatalf("spine too short: %v", CanonicalSpine)
	}
	for i := 0; i+1 < len(CanonicalSpine); i++ {
		from, to := CanonicalSpine[i], CanonicalSpine[i+1]
		if !FlowAllows(from, to) {
			t.Errorf("spine edge not in frozen flow: %s → %s", from, to)
		}
	}
	if last := CanonicalSpine[len(CanonicalSpine)-1]; !FlowIsTerminal(last) {
		t.Errorf("spine must end at a terminal, got %s", last)
	}
	if CanonicalSpine[0] != "StartConfirm" {
		t.Errorf("spine must start at StartConfirm, got %s", CanonicalSpine[0])
	}
}
