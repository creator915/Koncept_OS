package core

import (
	"testing"
)

func TestReportBuilder_RecordsAllThreeStatuses(t *testing.T) {
	b := NewReportBuilder()
	b.Pass("rule-a")
	b.Fail("rule-b", StaticIssue{Code: "rule-b", Message: "boom"})
	b.Skip("rule-c", "html-branch")

	r := b.Build()
	if len(r.Runs) != 3 {
		t.Fatalf("want 3 runs, got %d: %+v", len(r.Runs), r.Runs)
	}
	cov := r.Coverage()
	if cov["rule-a"] != StatusPass {
		t.Errorf("rule-a expected Pass, got %v", cov["rule-a"])
	}
	if cov["rule-b"] != StatusFail {
		t.Errorf("rule-b expected Fail, got %v", cov["rule-b"])
	}
	if cov["rule-c"] != StatusSkipped {
		t.Errorf("rule-c expected Skipped, got %v", cov["rule-c"])
	}
	if iss := r.Issues(); len(iss) != 1 || iss[0].Code != "rule-b" {
		t.Errorf("Issues() should surface only Fail: %+v", iss)
	}
}

func TestReportBuilder_FirstEmissionWins(t *testing.T) {
	b := NewReportBuilder()
	b.Pass("rule-a")
	b.Fail("rule-a", StaticIssue{Code: "rule-a", Message: "second call"})
	if cov := b.Build().Coverage(); cov["rule-a"] != StatusPass {
		t.Errorf("first emit should stick: got %v", cov["rule-a"])
	}
}

func TestAggregateOK_AllPassOrSkipped(t *testing.T) {
	b := NewReportBuilder()
	b.Pass("a")
	b.Pass("b")
	b.Skip("c", "carve-out")

	ok, missing, failed := AggregateOK(b.Build(), []string{"a", "b", "c"})
	if !ok || len(missing) != 0 || len(failed) != 0 {
		t.Errorf("expected ok=true, missing=[], failed=[]; got ok=%v missing=%v failed=%v", ok, missing, failed)
	}
}

func TestAggregateOK_FailRule(t *testing.T) {
	b := NewReportBuilder()
	b.Pass("a")
	b.Fail("b", StaticIssue{Code: "b"})

	ok, _, failed := AggregateOK(b.Build(), []string{"a", "b"})
	if ok {
		t.Error("expected ok=false when a rule fails")
	}
	if len(failed) != 1 || failed[0] != "b" {
		t.Errorf("expected failed=[b], got %v", failed)
	}
}

func TestAggregateOK_MissingRuleFails(t *testing.T) {
	// THE point of v9.3.2: a rule that was expected to fire but didn't
	// register anything is treated as Fail, not implicit Pass. This is
	// the structural protection against silent fail-open.
	b := NewReportBuilder()
	b.Pass("a")
	// Note: rule "b" was expected but never emitted.

	ok, missing, _ := AggregateOK(b.Build(), []string{"a", "b"})
	if ok {
		t.Error("expected ok=false when a rule is expected but unregistered (silent fail-open guard)")
	}
	if len(missing) != 1 || missing[0] != "b" {
		t.Errorf("expected missing=[b], got %v", missing)
	}
}

func TestAggregateOK_ExtraRunsTolerated(t *testing.T) {
	b := NewReportBuilder()
	b.Pass("a")
	b.Pass("b")
	b.Pass("extra-defensive-rule") // not in expected — OK, just tolerated

	ok, _, _ := AggregateOK(b.Build(), []string{"a", "b"})
	if !ok {
		t.Error("expected ok=true; extra runs beyond expected should not cause failure")
	}
}
