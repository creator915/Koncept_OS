package checkpoint

import (
	"path/filepath"
	"testing"
)

func tempCP(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "checkpoint.json")
}

func TestNew_StartsPending(t *testing.T) {
	c := New()
	c.RecomputeSummary()
	if c.Summary.FinalVerdict != VerdictPending {
		t.Errorf("new checkpoint should be PENDING, got %s", c.Summary.FinalVerdict)
	}
}

func TestAddItem_RejectsBadInput(t *testing.T) {
	p := tempCP(t)
	cases := []struct {
		name string
		fn   func() error
	}{
		{"bad id", func() error { return AddItem(p, "bad", "desc", "", SeverityMust, "") }},
		{"empty desc", func() error { return AddItem(p, "CHK-001", "", "", SeverityMust, "") }},
		{"bad severity", func() error { return AddItem(p, "CHK-001", "desc", "", "weird", "") }},
		{"waiver no reason", func() error { return AddItem(p, "CHK-001", "desc", "", SeverityWaiver, "") }},
	}
	for _, c := range cases {
		if err := c.fn(); err == nil {
			t.Errorf("%s: expected error, got nil", c.name)
		}
	}
}

func TestAddItem_DuplicateRejected(t *testing.T) {
	p := tempCP(t)
	if err := AddItem(p, "CHK-001", "first", "", SeverityMust, ""); err != nil {
		t.Fatal(err)
	}
	if err := AddItem(p, "CHK-001", "second", "", SeverityMust, ""); err == nil {
		t.Error("duplicate id should error")
	}
}

func TestFreeze_BlocksAdd(t *testing.T) {
	p := tempCP(t)
	if err := AddItem(p, "CHK-001", "desc", "", SeverityMust, ""); err != nil {
		t.Fatal(err)
	}
	if err := Freeze(p); err != nil {
		t.Fatal(err)
	}
	if err := AddItem(p, "CHK-002", "later", "", SeverityMust, ""); err == nil {
		t.Error("AddItem after Freeze should error")
	}
}

func TestFreeze_RefusesEmpty(t *testing.T) {
	p := tempCP(t)
	if err := Freeze(p); err == nil {
		t.Error("freezing empty checkpoint should error")
	}
}

func TestFreeze_Idempotent(t *testing.T) {
	p := tempCP(t)
	AddItem(p, "CHK-001", "desc", "", SeverityMust, "")
	if err := Freeze(p); err != nil {
		t.Fatal(err)
	}
	if err := Freeze(p); err != nil {
		t.Errorf("re-freezing should be no-op, got %v", err)
	}
}

func TestVerdict_PendingBeforeFreeze(t *testing.T) {
	p := tempCP(t)
	AddItem(p, "CHK-001", "desc", "", SeverityMust, "")
	c, _ := Load(p)
	if c.Summary.FinalVerdict != VerdictPending {
		t.Errorf("pre-freeze verdict should be PENDING, got %s", c.Summary.FinalVerdict)
	}
}

func TestVerdict_FailWhenMustUnfilled(t *testing.T) {
	p := tempCP(t)
	AddItem(p, "CHK-001", "must check", "", SeverityMust, "")
	AddItem(p, "CHK-002", "should check", "", SeverityShould, "")
	Freeze(p)
	c, _ := Load(p)
	if c.Summary.FinalVerdict != VerdictFail {
		t.Errorf("must-unfilled should FAIL, got %s", c.Summary.FinalVerdict)
	}
	if c.Summary.Failed != 1 {
		t.Errorf("expected 1 failed, got %d", c.Summary.Failed)
	}
}

func TestVerdict_PassWhenAllMustsFilled(t *testing.T) {
	p := tempCP(t)
	AddItem(p, "CHK-001", "must check", "", SeverityMust, "")
	AddItem(p, "CHK-002", "should check", "", SeverityShould, "") // unfilled, should-only
	Freeze(p)
	if err := Fill(p, "CHK-001", "src/foo.ts:42 Foo", ""); err != nil {
		t.Fatal(err)
	}
	c, _ := Load(p)
	if c.Summary.FinalVerdict != VerdictPass {
		t.Errorf("all musts filled (should unfilled) should PASS, got %s", c.Summary.FinalVerdict)
	}
	if c.Summary.Failed != 0 {
		t.Errorf("expected 0 failed, got %d", c.Summary.Failed)
	}
}

func TestVerdict_WaiverCountsAsResolved(t *testing.T) {
	p := tempCP(t)
	AddItem(p, "CHK-001", "must check", "", SeverityMust, "")
	AddItem(p, "CHK-002", "waived", "", SeverityWaiver, "infeasible to test")
	Freeze(p)
	Fill(p, "CHK-001", "src/foo.ts:42", "")
	c, _ := Load(p)
	if c.Summary.FinalVerdict != VerdictPass {
		t.Errorf("waiver should not block PASS, got %s", c.Summary.FinalVerdict)
	}
	if c.Summary.Waived != 1 {
		t.Errorf("expected 1 waived, got %d", c.Summary.Waived)
	}
}

func TestFill_RejectsUnknownItem(t *testing.T) {
	p := tempCP(t)
	if err := Fill(p, "CHK-999", "x", ""); err == nil {
		t.Error("unknown id should error")
	}
}

func TestFill_RejectsEmptyProof(t *testing.T) {
	p := tempCP(t)
	AddItem(p, "CHK-001", "desc", "", SeverityMust, "")
	if err := Fill(p, "CHK-001", "", ""); err == nil {
		t.Error("empty code+gameplay should error")
	}
}

func TestFill_AcceptsOnlyGameplayProof(t *testing.T) {
	p := tempCP(t)
	AddItem(p, "CHK-001", "desc", "", SeverityMust, "")
	if err := Fill(p, "CHK-001", "", "spawn / left:5 — K/proofs/CHK-001/final.png"); err != nil {
		t.Errorf("gameplayProof alone should be accepted: %v", err)
	}
}

func TestFill_RejectsWaiverItem(t *testing.T) {
	p := tempCP(t)
	AddItem(p, "CHK-001", "desc", "", SeverityWaiver, "reason")
	if err := Fill(p, "CHK-001", "src/x.ts", ""); err == nil {
		t.Error("filling a waiver item should error")
	}
}

func TestWaive_PostFreezeWorks(t *testing.T) {
	p := tempCP(t)
	AddItem(p, "CHK-001", "desc", "", SeverityMust, "")
	Freeze(p)
	if err := Waive(p, "CHK-001", "discovered untestable"); err != nil {
		t.Fatal(err)
	}
	c, _ := Load(p)
	if c.Items[0].Severity != SeverityWaiver {
		t.Errorf("severity should be waiver after Waive, got %s", c.Items[0].Severity)
	}
	if c.Summary.FinalVerdict != VerdictPass {
		t.Errorf("after waiving the only must, should PASS, got %s", c.Summary.FinalVerdict)
	}
}

func TestWaive_RequiresReason(t *testing.T) {
	p := tempCP(t)
	AddItem(p, "CHK-001", "desc", "", SeverityMust, "")
	Freeze(p)
	if err := Waive(p, "CHK-001", ""); err == nil {
		t.Error("Waive without reason should error")
	}
}

func TestPersistence_RoundTrip(t *testing.T) {
	p := tempCP(t)
	AddItem(p, "CHK-001", "desc1", "ui", SeverityMust, "")
	AddItem(p, "CHK-002", "desc2", "core", SeverityShould, "")
	Freeze(p)
	Fill(p, "CHK-001", "src/x.ts:10", "")

	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Frozen {
		t.Error("frozen state lost on round-trip")
	}
	if len(c.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(c.Items))
	}
	if c.Items[0].CodeProof != "src/x.ts:10" {
		t.Errorf("codeProof lost: %+v", c.Items[0])
	}
	if c.Items[0].Category != "ui" {
		t.Errorf("category lost")
	}
}
