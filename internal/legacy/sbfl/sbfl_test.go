package sbfl

import (
	"math"
	"testing"
)

// Classic SBFL sanity: the element executed by ALL failing runs and NO
// passing runs must rank #1. Spectrum:
//
//	t1 PASS  : a, b
//	t2 PASS  : a, c
//	t3 FAIL  : a, bug
//	t4 FAIL  : c, bug
//
// "bug" is in every failing run, no passing run → top suspicion.
func TestRank_BuggyElementRanksFirst(t *testing.T) {
	s := Spectrum{Runs: []TestRun{
		{Name: "t1", Passed: true, Executed: []string{"a", "b"}},
		{Name: "t2", Passed: true, Executed: []string{"a", "c"}},
		{Name: "t3", Passed: false, Executed: []string{"a", "bug"}},
		{Name: "t4", Passed: false, Executed: []string{"c", "bug"}},
	}}
	r := Rank(s, 2)
	if len(r) == 0 || r[0].Element != "bug" {
		t.Fatalf("expected 'bug' ranked #1, got %+v", r)
	}
	// bug: ef=2, ep=0, nf=2 → Ochiai = 2/sqrt((2+0)*(2+2)) = 2/sqrt(8).
	want := 2.0 / math.Sqrt(8)
	if math.Abs(r[0].Ochiai-want) > 1e-9 {
		t.Fatalf("Ochiai(bug) = %v, want %v", r[0].Ochiai, want)
	}
	// Bayesian: P(exec|fail)=2/2=1, P(exec|pass)=0/2=0 → 1.0.
	if math.Abs(r[0].Bayesian-1.0) > 1e-9 {
		t.Fatalf("Bayesian(bug) = %v, want 1.0", r[0].Bayesian)
	}
	// 'a' is executed by everything → low suspicion, must rank below bug.
	var aScore Score
	for _, sc := range r {
		if sc.Element == "a" {
			aScore = sc
		}
	}
	if aScore.Ochiai >= r[0].Ochiai {
		t.Fatalf("ubiquitous element 'a' must be less suspicious than 'bug': a=%v bug=%v", aScore.Ochiai, r[0].Ochiai)
	}
}

func TestRank_DStarExponentAndZeroDenominatorAreFinite(t *testing.T) {
	// Every run fails and every run executes "x": ep=0, nf=2, ef=2 →
	// DStar denominator ep+nf-ef = 0. Must be finite, not +Inf/NaN.
	s := Spectrum{Runs: []TestRun{
		{Name: "f1", Passed: false, Executed: []string{"x"}},
		{Name: "f2", Passed: false, Executed: []string{"x"}},
	}}
	r := Rank(s, 3)
	if len(r) != 1 || r[0].Element != "x" {
		t.Fatalf("unexpected ranking %+v", r)
	}
	if math.IsInf(r[0].DStar, 0) || math.IsNaN(r[0].DStar) {
		t.Fatalf("DStar must be finite when denominator is 0, got %v", r[0].DStar)
	}
	// star=3 → e_f^3 = 8.
	if r[0].DStar != math.Pow(2, 3) {
		t.Fatalf("DStar = %v, want %v", r[0].DStar, math.Pow(2, 3))
	}
}

func TestRank_DefaultStarIsTwo(t *testing.T) {
	s := Spectrum{Runs: []TestRun{
		{Name: "p", Passed: true, Executed: []string{"e"}},
		{Name: "f", Passed: false, Executed: []string{"e"}},
	}}
	// ef=1, ep=1, nf=1. DStar default star=2 → 1^2 / (1 + 1 - 1) = 1/1 = 1.
	r := Rank(s, 0)
	if r[0].DStar != 1.0 {
		t.Fatalf("default-star DStar = %v, want 1.0", r[0].DStar)
	}
}

func TestRank_DuplicateExecutionCountedOncePerRun(t *testing.T) {
	// "e" listed twice in one failing run must count as ef=1, not 2.
	s := Spectrum{Runs: []TestRun{
		{Name: "f", Passed: false, Executed: []string{"e", "e", "e"}},
		{Name: "p", Passed: true, Executed: []string{"e"}},
	}}
	r := Rank(s, 2)
	if r[0].Ef != 1 || r[0].Ep != 1 {
		t.Fatalf("dup execution must collapse per run: ef=%d ep=%d", r[0].Ef, r[0].Ep)
	}
}

// SBFL output must lift into a characterize.Oracle with the
// suspiciousness on its OWN confidence dimension (设计文档 4.2.4 / 原则 C).
func TestOracleFromScore_FillsSBFLDimensionConditionally(t *testing.T) {
	sc := Score{Element: "bug", Ochiai: 0.7, Bayesian: 0.6, Ef: 2, Ep: 0}
	o := OracleFromScore(sc, "ev-spectrum-1", []string{"A_runtime", "A_trace_set"})
	if o.Source != "SBFL" {
		t.Fatalf("source must be SBFL, got %q", o.Source)
	}
	if o.Confidence.SBFLSuspiciousness != 0.7 {
		t.Fatalf("SBFL suspiciousness must land on its own dimension, got %v", o.Confidence.SBFLSuspiciousness)
	}
	if o.Confidence.CoverageScore != 0 {
		t.Fatalf("SBFL must NOT touch the coverage dimension (no scalar merge), got %v", o.Confidence.CoverageScore)
	}
	if len(o.ConditionalOn) != 2 || len(o.EvidenceRefs) != 1 {
		t.Fatalf("Oracle must stay conditional-on assumptions + reference its evidence, got cond=%v ev=%v", o.ConditionalOn, o.EvidenceRefs)
	}
}
