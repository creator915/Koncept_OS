package review

import (
	"fmt"
	"sort"
	"strings"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// ContractTraceRuleCodes is the canonical list of every rule
// ContractTraceCheck emits a signal for. Mirrors StaticRuleCodes /
// RuntimeRuleCodes; AggregateOK uses this to detect silent-skip bugs.
//
// Rule codes (Step 4 of contract landing, 2026-05-25):
//
//   - "contract-empty": reserved for future tightening — fires Fail
//     when describe HAS RUN but emitted no clauses. Currently always
//     Pass/Skip; flipping to Fail is the migration trigger after
//     grandfather period ends.
//   - "contract-clause-uncovered": every non-optional clause must be
//     cited by at least one test case's ContractRefs. Fails with the
//     uncovered clause IDs so the agent knows what to add tests for.
//   - "contract-ref-unknown": every contractRef in test cases must
//     resolve to a clause ID in the spec's Contract. Catches stale
//     refs (clause removed via re-describe but tests not regenerated).
var ContractTraceRuleCodes = []string{
	"contract-empty",
	"contract-clause-uncovered",
	"contract-ref-unknown",
}

// ContractTraceCheck enforces bidirectional traceability between
// SpecEvidence.Contract clauses and TestsEvidence.Cases[*].ContractRefs.
//
// Skip conditions (grandfather and not-applicable paths — all yield
// AggregateOK true so confirm_object is not blocked):
//
//   - No spec on disk         → all rules Skip "no-spec" (object hasn't
//                                been described yet; static_check's
//                                stale-spec rule covers this gap)
//   - Spec but empty Contract → all rules Skip "legacy-no-contract"
//                                (pre-Step-2 bundle, or describe failed
//                                to extract clauses; agent's next
//                                describe-cycle will populate)
//   - No tests on disk        → all rules Skip "no-tests" (synth hasn't
//                                run yet; the missing-tests gate fires
//                                separately)
//   - Reconstruction-mode     → all rules Skip "reconstruction-mode-
//                                characterization" (./probe rebuild
//                                tasks verify via behavioral equivalence,
//                                not typed contracts; Step 5 will fold
//                                this path into Contract clauses)
//   - HTML deliverable        → all rules Skip "html-branch" (verified
//                                via runtime_smoke, not the synth chain)
//
// Once any skip reason no longer applies, the strict rules activate
// without further code change. This is the structural shape required
// for the migration to not be a hard cutover.
//
// g is the graph (needed for HTML / reconstruction-mode detection);
// pass nil to skip those branch checks and run pure traceability.
func ContractTraceCheck(g *graph.Graph, objID string) core.CheckReport {
	rb := core.NewReportBuilder()
	mkIssue := func(code, msg string) core.StaticIssue {
		return core.StaticIssue{Code: code, Where: objID, Message: msg}
	}
	skipAll := func(reason string) core.CheckReport {
		for _, code := range ContractTraceRuleCodes {
			rb.Skip(code, reason)
		}
		return rb.Build()
	}

	// Branch-skip: HTML deliverables go through runtime_smoke, not the
	// synth/test/contract chain. Same reasoning as runtime_check.go's
	// HTML skip.
	if g != nil {
		if obj, ok := g.Objects[objID]; ok && IsHTMLDeliverable(obj) {
			return skipAll("html-branch")
		}
	}

	// Branch-skip: reconstruction-mode (./probe black-box rebuild).
	// Two equivalent triggers (Step 5, 2026-05-25):
	//   - legacy: CharacterizationSection.LockedCount > 0
	//   - unified: spec.Contract contains ≥1 Kind="characterization"
	//     clause (mirror written by WriteCharacterization)
	// Either suffices — the gate skips for both so the migration off
	// the standalone CharacterizationSection can roll forward without
	// breaking confirm.
	if char, ok := core.ReadCharacterization(objID); ok && char != nil && char.LockedCount > 0 {
		return skipAll("reconstruction-mode-characterization")
	}

	spec, hasSpec := core.ReadSpec(objID)
	if !hasSpec {
		return skipAll("no-spec")
	}
	if hasCharacterizationClause(spec.Contract) {
		return skipAll("reconstruction-mode-characterization")
	}
	if len(spec.Contract) == 0 {
		// Grandfather: pre-Step-2 bundles, OR Step-2 bundles where the
		// LLM forgot the ---CONTRACT--- section. Both are silent
		// passes today — the agent's next describe re-run will populate.
		// (Once migration is judged complete, flip this to Fail on
		// "contract-empty" code to force re-describe.)
		return skipAll("legacy-no-contract")
	}
	rb.Pass("contract-empty")

	tests, hasTests := core.ReadTests(objID)
	if !hasTests {
		rb.Skip("contract-clause-uncovered", "no-tests")
		rb.Skip("contract-ref-unknown", "no-tests")
		return rb.Build()
	}

	// Build clause index for backward check + cover-tracking for forward.
	//
	// Coverage sources (2026-05-25 testCode extension): unify the
	// schema-cases per-case ContractRefs with the testCode-mode
	// top-level tests.ContractRefs. Either source contributes
	// coverage; the gate doesn't care which mode the synth used.
	clauseByID := make(map[string]core.ContractClause, len(spec.Contract))
	covered := make(map[string][]string, len(spec.Contract))
	for _, c := range spec.Contract {
		clauseByID[c.ID] = c
	}
	for _, tc := range tests.Cases {
		for _, ref := range tc.ContractRefs {
			covered[ref] = append(covered[ref], tc.Name)
		}
	}
	// testCode-mode flat coverage. Use a synthetic "case name" so the
	// uncovered/unknown messages still point at something the agent
	// can reason about.
	for _, ref := range tests.ContractRefs {
		covered[ref] = append(covered[ref], "<testCode-body>")
	}

	// Forward: every non-optional clause cited by ≥1 case.
	var uncovered []string
	for _, c := range spec.Contract {
		if c.Optional {
			continue
		}
		if len(covered[c.ID]) == 0 {
			uncovered = append(uncovered, c.ID)
		}
	}
	if len(uncovered) > 0 {
		sort.Strings(uncovered)
		// Echo each uncovered clause's body so the agent doesn't have
		// to re-read the spec evidence to know what's missing.
		var bodies []string
		for _, id := range uncovered {
			bodies = append(bodies, fmt.Sprintf("  - %s (%s): %s", id, clauseByID[id].Kind, clip(clauseByID[id].Body, 120)))
		}
		rb.Fail("contract-clause-uncovered", mkIssue("contract-clause-uncovered",
			fmt.Sprintf("%d non-optional contract clause(s) have no test coverage — every TestCase.ContractRefs must collectively cover every clause:\n%s\n\nRegenerate tests via typecalc_synthesize_tests (it sees the contract and is instructed to cite every clause).",
				len(uncovered), strings.Join(bodies, "\n"))))
	} else {
		rb.Pass("contract-clause-uncovered")
	}

	// Backward: every contractRef in any source resolves to a known
	// clause. Both per-case refs (schema mode) and top-level refs
	// (testCode mode) are checked.
	type unknownRef struct {
		caseName string
		refs     []string
	}
	var unknowns []unknownRef
	for _, tc := range tests.Cases {
		var bad []string
		for _, ref := range tc.ContractRefs {
			if _, ok := clauseByID[ref]; !ok {
				bad = append(bad, ref)
			}
		}
		if len(bad) > 0 {
			unknowns = append(unknowns, unknownRef{caseName: tc.Name, refs: bad})
		}
	}
	var topBad []string
	for _, ref := range tests.ContractRefs {
		if _, ok := clauseByID[ref]; !ok {
			topBad = append(topBad, ref)
		}
	}
	if len(topBad) > 0 {
		unknowns = append(unknowns, unknownRef{caseName: "<testCode-body>", refs: topBad})
	}
	if len(unknowns) > 0 {
		var lines []string
		for _, u := range unknowns {
			lines = append(lines, fmt.Sprintf("  - case %q references unknown clause(s): %s", u.caseName, strings.Join(u.refs, ", ")))
		}
		// List the valid clause IDs so the agent can fix in place.
		var validIDs []string
		for id := range clauseByID {
			validIDs = append(validIDs, id)
		}
		sort.Strings(validIDs)
		rb.Fail("contract-ref-unknown", mkIssue("contract-ref-unknown",
			fmt.Sprintf("%d test case(s) cite unknown clause IDs (likely stale after spec re-describe):\n%s\n\nValid clause IDs in current spec: %s\n\nRegenerate tests via typecalc_synthesize_tests, or update the case refs to a valid clause.",
				len(unknowns), strings.Join(lines, "\n"), strings.Join(validIDs, ", "))))
	} else {
		rb.Pass("contract-ref-unknown")
	}

	return rb.Build()
}

// hasCharacterizationClause reports whether any clause in the list is
// Kind="characterization". Used by the reconstruction-mode branch to
// detect the Step 5 unified path.
func hasCharacterizationClause(clauses []core.ContractClause) bool {
	for _, c := range clauses {
		if c.Kind == "characterization" {
			return true
		}
	}
	return false
}

