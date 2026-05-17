package typing

import "testing"

func TestPropose_MaterializesTypingAssumptionsAndScores(t *testing.T) {
	o := Opportunity{
		ID:       "transfer_amount",
		Category: CatRefinement,
		Scope:    "transfer(amount)",
		Invariant: "amount > 0 at every call site",
		Introduces: []string{
			"amount should be a positive integer",
			"transfer may fail; failure semantic is a Money error",
		},
		CallerChurn: 3,
		BugClasses:  2,
	}
	p := Propose(o, 2.0, "task-7")
	if len(p.Assumptions) != 2 {
		t.Fatalf("each introduced premise must become an assumption, got %d", len(p.Assumptions))
	}
	for _, a := range p.Assumptions {
		if a.Layer != "Application" || a.Status != "Active" {
			t.Fatalf("typing assumptions are Active@Application, got %+v", a)
		}
	}
	// Score = stake×bugClasses / (callerChurn + #assumptions) = 2*2/(3+2).
	if want := 2.0 * 2.0 / 5.0; p.Score != want {
		t.Fatalf("score = %v, want %v", p.Score, want)
	}
}

func TestPropose_MissingStakeDoesNotZeroScore(t *testing.T) {
	p := Propose(Opportunity{ID: "x", BugClasses: 1, CallerChurn: 0}, 0, "")
	if p.Score <= 0 {
		t.Fatalf("missing stake must default to neutral (1), not zero the score; got %v", p.Score)
	}
}

// Part 5.4 / 原则 C: type Oracle is confidence 1.0 ONLY because it
// carries the typing assumptions + the compiler assumption in
// conditional_on. The 1.0 is not free-floating.
func TestOracleFromProposal_HighConfidenceIsConditional(t *testing.T) {
	p := Propose(Opportunity{
		ID: "uid", Category: CatNewtype, Scope: "lookup(id)",
		Invariant:  "UserId is never passed where OrderId is expected",
		Introduces: []string{"id is a UserId, not a bare string"},
	}, 1, "task-1")
	o := OracleFromProposal(p, "A_compiler", "ev-typecheck-1")

	if o.Source != "Type" {
		t.Fatalf("source must be Type, got %q", o.Source)
	}
	if o.Confidence.StatisticalScore != 1.0 || o.Confidence.IndependenceScore != 1.0 {
		t.Fatalf("type oracle is compiler-guarded 1.0/1.0, got %+v", o.Confidence)
	}
	// Must be conditional on the typing assumption AND A_compiler.
	if len(o.ConditionalOn) != 2 {
		t.Fatalf("conditional_on must be {typing assumption, A_compiler}, got %v", o.ConditionalOn)
	}
	hasCompiler := false
	for _, c := range o.ConditionalOn {
		if c == "A_compiler" {
			hasCompiler = true
		}
	}
	if !hasCompiler {
		t.Fatalf("1.0 confidence MUST be conditional on the compiler being correct, got %v", o.ConditionalOn)
	}
	if len(o.EvidenceRefs) != 1 {
		t.Fatalf("type oracle must reference the type-check evidence, got %v", o.EvidenceRefs)
	}
}

func TestUnreachableByTyping_IsHonestlyDeclared(t *testing.T) {
	u := UnreachableByTyping()
	if len(u) < 5 {
		t.Fatalf("Part 5.3 honest-limits list must be surfaced, got %v", u)
	}
}
