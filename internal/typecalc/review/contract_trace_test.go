package review

import (
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// minimalGraph returns a graph carrying a single object with the
// minimum fields needed for the gate to not branch into the
// HTML-deliverable / reconstruction-mode skip paths.
func minimalGraph(objID string) *graph.Graph {
	g := graph.NewGraph()
	implPath := "x.go"
	g.Objects[objID] = &graph.Object{
		Intent: "x", Impl: &implPath,
		Consumes: []string{}, Produces: []string{"out"}, Mutates: []string{},
	}
	return g
}

// statusOf returns the recorded outcome of a rule, or StatusUnknown
// if the rule didn't fire. Mirrors the legacy review-package idiom.
func statusOf(rep core.CheckReport, code string) core.RuleStatus {
	for _, r := range rep.Runs {
		if r.Code == code {
			return r.Status
		}
	}
	return core.StatusUnknown
}

// TestContractTraceCheck_NoSpec_Skip — object with no spec evidence:
// every rule skips with "no-spec". AggregateOK true.
func TestContractTraceCheck_NoSpec_Skip(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	g := minimalGraph("X")
	rep := ContractTraceCheck(g, "X")
	ok, _, _ := core.AggregateOK(rep, ContractTraceRuleCodes)
	if !ok {
		t.Errorf("AggregateOK should be true on no-spec skip path")
	}
	for _, code := range ContractTraceRuleCodes {
		if got := statusOf(rep,code); got != core.StatusSkipped {
			t.Errorf("rule %s: want Skipped, got %v", code, got)
		}
	}
}

// TestContractTraceCheck_EmptyContract_Grandfather — spec exists but
// Contract is empty: skip with "legacy-no-contract" (do NOT fail —
// this is the grandfather path so pre-Step-2 bundles still pass).
func TestContractTraceCheck_EmptyContract_Grandfather(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	_ = core.WriteSpec(&core.SpecEvidence{
		ObjectID: "X", Description: "no contract", SourceHash: "h1",
	})
	g := minimalGraph("X")
	rep := ContractTraceCheck(g, "X")
	ok, _, _ := core.AggregateOK(rep, ContractTraceRuleCodes)
	if !ok {
		t.Errorf("grandfather (empty Contract) should keep AggregateOK true")
	}
}

// TestContractTraceCheck_AllCovered_Pass — every non-optional clause
// cited by ≥1 case; every case ref resolves: both directional rules
// Pass; AggregateOK true.
func TestContractTraceCheck_AllCovered_Pass(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	_ = core.WriteSpec(&core.SpecEvidence{
		ObjectID: "X", Description: "d", SourceHash: "h",
		Contract: []core.ContractClause{
			{ID: "c1", Kind: "example", Body: "f(1)=1"},
			{ID: "c2", Kind: "invariant", Body: "idempotent"},
			{ID: "c3", Kind: "example", Body: "f(0)=0", Optional: true},
		},
	})
	_ = core.WriteTests(&core.TestsEvidence{
		ObjectID: "X", Lang: "Go", SpecHash: "h",
		Cases: []core.TestCase{
			{Name: "happy", Call: "f(1)", ContractRefs: []string{"c1"}},
			{Name: "idem", Call: "f(f(2))", ContractRefs: []string{"c2"}},
		},
	})
	g := minimalGraph("X")
	rep := ContractTraceCheck(g, "X")
	ok, _, failed := core.AggregateOK(rep, ContractTraceRuleCodes)
	if !ok {
		t.Errorf("expected pass, got failed rules: %v", failed)
	}
}

// TestContractTraceCheck_UncoveredNonOptional_Fail — a non-optional
// clause with no covering case fails contract-clause-uncovered. The
// optional one not being covered is OK.
func TestContractTraceCheck_UncoveredNonOptional_Fail(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	_ = core.WriteSpec(&core.SpecEvidence{
		ObjectID: "X", SourceHash: "h",
		Contract: []core.ContractClause{
			{ID: "c1", Kind: "example", Body: "covered"},
			{ID: "c2", Kind: "invariant", Body: "uncovered required"},
			{ID: "c3", Kind: "example", Body: "uncovered but optional", Optional: true},
		},
	})
	_ = core.WriteTests(&core.TestsEvidence{
		ObjectID: "X", Lang: "Go", SpecHash: "h",
		Cases: []core.TestCase{
			{Name: "only_c1", Call: "f()", ContractRefs: []string{"c1"}},
		},
	})
	g := minimalGraph("X")
	rep := ContractTraceCheck(g, "X")
	if got := statusOf(rep,"contract-clause-uncovered"); got != core.StatusFail {
		t.Fatalf("uncovered required clause should fail, got %v", got)
	}
	// The Fail issue should name c2 (not c3 — c3 is optional).
	for _, iss := range rep.Issues() {
		if iss.Code == "contract-clause-uncovered" {
			if !strings.Contains(iss.Message, "c2") {
				t.Errorf("issue should name c2: %s", iss.Message)
			}
			if strings.Contains(iss.Message, "c3") {
				t.Errorf("issue must NOT flag optional c3: %s", iss.Message)
			}
		}
	}
}

// TestContractTraceCheck_UnknownRef_Fail — case cites a ref that no
// clause defines: fails contract-ref-unknown. Catches stale refs
// after the agent re-describes (changes clause IDs) but forgets to
// re-synthesize tests.
func TestContractTraceCheck_UnknownRef_Fail(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	_ = core.WriteSpec(&core.SpecEvidence{
		ObjectID: "X", SourceHash: "h",
		Contract: []core.ContractClause{
			{ID: "c1", Kind: "example", Body: "x"},
		},
	})
	_ = core.WriteTests(&core.TestsEvidence{
		ObjectID: "X", Lang: "Go", SpecHash: "h",
		Cases: []core.TestCase{
			{Name: "happy", Call: "f()", ContractRefs: []string{"c1"}},
			{Name: "stale", Call: "g()", ContractRefs: []string{"c9", "c42"}},
		},
	})
	g := minimalGraph("X")
	rep := ContractTraceCheck(g, "X")
	if got := statusOf(rep,"contract-ref-unknown"); got != core.StatusFail {
		t.Fatalf("unknown ref should fail, got %v", got)
	}
	for _, iss := range rep.Issues() {
		if iss.Code == "contract-ref-unknown" {
			if !strings.Contains(iss.Message, "c9") || !strings.Contains(iss.Message, "c42") {
				t.Errorf("issue should name both bad refs: %s", iss.Message)
			}
			if !strings.Contains(iss.Message, "stale") {
				t.Errorf("issue should name the offending case: %s", iss.Message)
			}
		}
	}
}

// TestContractTraceCheck_ReconstructionMode_Skip — Characterization
// section with LockedCount>0 means the object is verified via
// behavioral equivalence (./probe vs run_local), not typed contracts.
// All rules skip; Step 5 will fold this into Contract clauses.
func TestContractTraceCheck_ReconstructionMode_Skip(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	_ = core.WriteSpec(&core.SpecEvidence{
		ObjectID: "X", SourceHash: "h",
		// Even with strict Contract present, reconstruction-mode skips
		// — the contract path is for typed objects, not CLI rebuilds.
		Contract: []core.ContractClause{{ID: "c1", Kind: "example", Body: "x"}},
	})
	// Write a characterization with LockedCount>0 by calling the helper
	// directly. The exact mechanism depends on core's char API; use
	// SaveBundle to inject one.
	b := core.LoadOrInitBundle("X")
	b.Characterization = &core.CharacterizationSection{LockedCount: 3}
	_ = core.SaveBundle(b)
	g := minimalGraph("X")
	rep := ContractTraceCheck(g, "X")
	ok, _, _ := core.AggregateOK(rep, ContractTraceRuleCodes)
	if !ok {
		t.Errorf("reconstruction-mode should skip all rules, AggregateOK should stay true")
	}
}
