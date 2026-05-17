package ledger

import (
	"path/filepath"
	"testing"
)

func TestLedger_AppendIsImmutableAndRoundTrips(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ledger.json")
	l, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.RecordAssumption("root", "A_env", map[string]string{"stmt": "env stable"}, 0); err != nil {
		t.Fatal(err)
	}
	// A revision supersedes — but the original fact stays in history.
	if _, err := l.RecordAssumption("root", "A_env", map[string]string{"stmt": "env stable v2"}, 1); err != nil {
		t.Fatal(err)
	}
	l2, err := Open(p) // reload from disk — client can read without the agent
	if err != nil {
		t.Fatal(err)
	}
	if len(l2.Facts) != 2 {
		t.Fatalf("immutable history must keep BOTH the original and the revision, got %d", len(l2.Facts))
	}
	if l2.Facts[1].Supersedes != 1 {
		t.Fatalf("revision must point at the superseded seq, got %d", l2.Facts[1].Supersedes)
	}
}

func TestLedger_ForkInheritsParentAssumptions(t *testing.T) {
	l, _ := Open(filepath.Join(t.TempDir(), "l.json"))
	_ = l.SetBranchAssumptions("root", []string{"A_compiler", "A_runtime"})
	child, err := l.Fork("root", "b_compiler_probe", "Compiler", "layer escalation")
	if err != nil {
		t.Fatal(err)
	}
	if len(child.ActiveAssumptions) != 2 || child.WorkingLayer != "Compiler" {
		t.Fatalf("fork must inherit parent assumptions + take new layer, got %+v", child)
	}
	if child.Parent != "root" {
		t.Fatalf("lineage broken: parent=%q", child.Parent)
	}
}

func TestLedger_OracleForProperty_LatestNonSupersededInLineage(t *testing.T) {
	l, _ := Open(filepath.Join(t.TempDir(), "l.json"))
	_, _ = l.Fork("root", "b1", "BusinessLogic", "work")
	// root has an older oracle for P; b1 supersedes it with a newer one.
	rootO, _ := l.RecordOracle("root", "O1", "P_balance", []string{"A1"}, []string{"E1"}, "v1", 0)
	if _, err := l.RecordOracle("b1", "O2", "P_balance", []string{"A1"}, []string{"E1"}, "v2", rootO.Seq); err != nil {
		t.Fatal(err)
	}
	got, ok := l.OracleForProperty("b1", "P_balance")
	if !ok || got.EntityID != "O2" {
		t.Fatalf("branch b1 must see the superseding oracle O2, got %+v ok=%v", got, ok)
	}
	// root, not seeing b1's revision, still resolves to its own O1.
	gotRoot, _ := l.OracleForProperty("root", "P_balance")
	if gotRoot.EntityID != "O1" {
		t.Fatalf("root must still resolve its own oracle O1, got %s", gotRoot.EntityID)
	}
}

func TestLedger_BranchesSharingAssumption(t *testing.T) {
	l, _ := Open(filepath.Join(t.TempDir(), "l.json"))
	_ = l.SetBranchAssumptions("root", []string{"A_shared"})
	_, _ = l.Fork("root", "b1", "", "")
	_, _ = l.Fork("root", "b2", "", "")
	_ = l.SetBranchAssumptions("b2", []string{"A_other"})
	share := map[string]bool{}
	for _, b := range l.BranchesSharingAssumption("A_shared") {
		share[b] = true
	}
	if !share["root"] || !share["b1"] || share["b2"] {
		t.Fatalf("reverse query wrong: root/b1 share A_shared, b2 does not; got %v", share)
	}
}

// Evidence invalidation propagates to every Oracle that referenced it
// (设计文档 Part 2.4b) — and the invalidated oracle drops out of
// OracleForProperty, while the history keeps every fact.
func TestLedger_EvidenceInvalidationPropagates(t *testing.T) {
	l, _ := Open(filepath.Join(t.TempDir(), "l.json"))
	_, _ = l.RecordOracle("root", "Oa", "P1", []string{"A1"}, []string{"E_bad"}, "x", 0)
	_, _ = l.RecordOracle("root", "Ob", "P2", []string{"A2"}, []string{"E_ok"}, "y", 0)

	affected, err := l.InvalidateEvidence("E_bad", "reference impl had a bug")
	if err != nil {
		t.Fatal(err)
	}
	if len(affected) != 1 || affected[0] != "Oa" {
		t.Fatalf("only the oracle referencing E_bad must be flagged, got %v", affected)
	}
	if _, ok := l.OracleForProperty("root", "P1"); ok {
		t.Fatal("invalidated oracle must drop out of OracleForProperty")
	}
	if _, ok := l.OracleForProperty("root", "P2"); !ok {
		t.Fatal("unaffected oracle must remain resolvable")
	}
	// History preserved: 2 oracle facts + 1 invalidation fact.
	if len(l.Facts) != 3 {
		t.Fatalf("facts are never deleted — want 3 (2 oracle + 1 invalidate), got %d", len(l.Facts))
	}
}
