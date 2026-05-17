package characterize

import "testing"

func TestComputeNextAction_Rung1BlockingEscalationPreemptsAll(t *testing.T) {
	// An object also lacks an Oracle (rung 2 would fire) — but a
	// blocking escalation must preempt it (Part 7.4 strict ordering).
	a := ComputeNextAction(AgentState{
		BlockingEscalation: "nondeterminism escalation-candidate on Rng awaiting customer",
		Objects:            []ObjectStatus{{ID: "Rng", HighStake: true, HasCharOracle: false}},
	})
	if a.Kind != ActionEscalateToCustomer || a.Rung != 1 {
		t.Fatalf("rung 1 must preempt rung 2, got %+v", a)
	}
}

func TestComputeNextAction_Rung2CharacterizesHighStakeWithoutOracle(t *testing.T) {
	a := ComputeNextAction(AgentState{
		Objects: []ObjectStatus{
			{ID: "Helper", HighStake: false, HasCharOracle: false}, // low stake — skipped
			{ID: "Billing", HighStake: true, HasCharOracle: false},  // target
		},
	})
	if a.Kind != ActionCharacterize || a.ObjectID != "Billing" || a.Rung != 2 {
		t.Fatalf("expected Characterize(Billing) at rung 2, got %+v", a)
	}
}

func TestComputeNextAction_Rung7ContinuesGoalWhenAllOraclesPresent(t *testing.T) {
	a := ComputeNextAction(AgentState{
		Objects:        []ObjectStatus{{ID: "Billing", HighStake: true, HasCharOracle: true}},
		GoalIncomplete: true,
	})
	if a.Kind != ActionContinueGoal || a.Rung != 7 {
		t.Fatalf("expected ContinueGoal at rung 7, got %+v", a)
	}
}

func TestComputeNextAction_Rung8TerminatesWhenNothingOutstanding(t *testing.T) {
	a := ComputeNextAction(AgentState{
		Objects: []ObjectStatus{{ID: "Billing", HighStake: true, HasCharOracle: true}},
	})
	if a.Kind != ActionTerminate || a.Rung != 8 {
		t.Fatalf("expected Terminate at rung 8, got %+v", a)
	}
}

// Honest-deferral contract: the rungs the MVP cannot evaluate are
// SURFACED, not silently skipped (workflow-level analogue of Part 10.2).
func TestComputeNextAction_UnevaluatedRungsAreSurfaced(t *testing.T) {
	u := UnevaluatedRungs()
	for _, rung := range []int{3, 4, 5, 6} {
		if _, ok := u[rung]; !ok {
			t.Fatalf("rung %d must be reported as deferred, got %v", rung, u)
		}
	}
	if len(u) != 4 {
		t.Fatalf("exactly rungs 3-6 are deferred in MVP, got %v", u)
	}
}
