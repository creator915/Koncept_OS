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

// TestContractTraceCheck_UnifiedCharacterizationClause_Skip (Step 5,
// 2026-05-22) — the unified path: when spec.Contract carries a
// Kind="characterization" clause (mirror written by
// WriteCharacterization), the reconstruction-mode skip fires even if
// the standalone CharacterizationSection isn't read. Forward-compat
// for when CharacterizationSection is eventually retired.
func TestContractTraceCheck_UnifiedCharacterizationClause_Skip(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	// Strict contract — would normally fail uncovered/unknown — but
	// the characterization clause should pre-empt every check.
	_ = core.WriteSpec(&core.SpecEvidence{
		ObjectID: "X", SourceHash: "h",
		Contract: []core.ContractClause{
			{ID: "c1", Kind: "example", Body: "uncovered_required"},
			{ID: "char-equiv-X", Kind: "characterization", Body: "impl matches reference on 50/50", Source: "char:suite=equiv-X"},
		},
	})
	// Note: no Characterization section written — only the mirror path.
	g := minimalGraph("X")
	rep := ContractTraceCheck(g, "X")
	ok, _, failed := core.AggregateOK(rep, ContractTraceRuleCodes)
	if !ok {
		t.Errorf("unified-path characterization clause should skip-all; got failures: %v", failed)
	}
}

// TestWriteCharacterization_MirrorsToContract (Step 5) — writing a
// CharacterizationSection automatically populates spec.Contract with
// a Kind="characterization" clause whose ID is derived from SuiteID
// (so repeated writes overwrite rather than accumulate).
func TestWriteCharacterization_MirrorsToContract(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	sec := &core.CharacterizationSection{
		SuiteID:        "equiv-Foo",
		OracleProperty: "impl matches reference probe",
		LockedCount:    7,
		UnlockedCount:  0,
	}
	if err := core.WriteCharacterization("Foo", sec); err != nil {
		t.Fatalf("write: %v", err)
	}
	spec, ok := core.ReadSpec("Foo")
	if !ok {
		t.Fatal("expected Spec section to be auto-created by mirror")
	}
	if len(spec.Contract) != 1 {
		t.Fatalf("expected 1 mirrored clause, got %d: %+v", len(spec.Contract), spec.Contract)
	}
	c := spec.Contract[0]
	if c.Kind != "characterization" {
		t.Errorf("mirror kind: %q want characterization", c.Kind)
	}
	if c.ID != "char-equiv-Foo" {
		t.Errorf("mirror ID should derive from SuiteID: %q", c.ID)
	}
	if !strings.Contains(c.Body, "locked=7") {
		t.Errorf("body should carry counts: %q", c.Body)
	}
	if c.Source != "char:suite=equiv-Foo" {
		t.Errorf("source should cite suite: %q", c.Source)
	}

	// Second write with updated counts must OVERWRITE the same clause
	// ID, not accumulate.
	sec.LockedCount = 10
	if err := core.WriteCharacterization("Foo", sec); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	spec2, _ := core.ReadSpec("Foo")
	if len(spec2.Contract) != 1 {
		t.Errorf("re-write must dedup by ID, not append: got %d clauses", len(spec2.Contract))
	}
	if !strings.Contains(spec2.Contract[0].Body, "locked=10") {
		t.Errorf("re-write should update body: %q", spec2.Contract[0].Body)
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
