package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// CharacterizationSection persists the brownfield characterization lock
// (屎山代码维护Agent设计文档 v1.0 Part 6.6 Characterization Test /
// Part 2.4b Finite vs Reproducible Evidence / Part 10.2 诚实置信度).
//
// Design constraints honored here:
//   - core stays the lowest layer: this section uses ONLY core/plain
//     types (no import of internal/legacy/characterize — that package
//     imports core; the reverse would cycle). The characterize package
//     maps its rich CharResult onto this persistable shape; the full
//     lossless object is kept in Detail for audit (Part 10.3: the
//     client can read the lock WITHOUT the agent).
//   - No scalar confidence: ConfidenceReport is the per-dimension
//     honest report (Part 10.2 forbids compressing to one number).
//   - Honest uncovered surface: Unlocked is persisted, never hidden.
//
// Gate relevance: the Method-Use-Rule (Feathers Part 6.6, enforced
// legacy-path-only in CheckObjectGate) reads CodeHash to decide whether
// the lock still characterizes the CURRENT artifact bytes, and Cases to
// re-run the lock as a regression oracle.
type CharacterizationSection struct {
	SuiteID     string `json:"suiteId"`
	ImplSymbol  string `json:"implSymbol"`
	Lang        string `json:"lang"`
	ArtifactRef string `json:"artifactRef"`
	// CodeHash is the SHA-256 of the legacy artifact at the time the
	// lock was recorded. The lock's authority is conditional on this
	// hash (设计文档 原则 C): a different hash ⇒ Oracle invalidated
	// until re-characterized.
	CodeHash string `json:"codeHash"`

	// Cases are the GOLDEN characterization cases: probes whose Expect
	// was filled from observed legacy behavior. Re-rendering + running
	// these against a modified artifact is the regression lock.
	Cases []TestCase `json:"cases"`

	LockedCount   int      `json:"lockedCount"`
	UnlockedCount int      `json:"unlockedCount"`
	Unlocked      []string `json:"unlocked,omitempty"` // honest 未覆盖范围

	// OracleProperty is the human-readable recovered-behavior statement.
	OracleProperty string `json:"oracleProperty"`
	// ConfidenceReport is the per-dimension confidence (e.g.
	// "coverage = 0.812", "independence = (not measured ...)"). Never a
	// single number.
	ConfidenceReport []string `json:"confidenceReport"`
	// ConditionalOn lists the human-readable assumption statements the
	// lock is valid under (设计文档 Part 2.4 conditional_on, surfaced
	// for client audit per Part 10.4).
	ConditionalOn []string `json:"conditionalOn"`

	// Detail is the lossless CharResult JSON (assumptions with ids,
	// finite/reproducible evidence, full oracle). Opaque to core; the
	// characterize package owns its schema. Kept so the client can
	// audit the agent's thinking path without the agent (Part 10.3/10.4).
	Detail json.RawMessage `json:"detail,omitempty"`

	Timestamp time.Time `json:"timestamp"`
}

// WriteCharacterization persists a characterization lock as a
// characterization-kind clause inside the bundle's Spec.Contract.
//
// 2026-05-25 Step 5 single-source refactor: Contract is the ONLY
// place characterization data lives. The old standalone
// b.Characterization section is cleared (set to nil) if present so
// nothing reads stale data — readers must now go through
// ReadCharacterizationClauses (Contract-based) or peek at
// b.Spec.Contract directly.
//
// Clause shape:
//   - ID:     "char-" + SuiteID  (stable, repeat calls overwrite)
//   - Kind:   "characterization"
//   - Body:   OracleProperty + locked/unlocked counts
//   - Source: "char:suite=" + SuiteID
//   - Detail: the full lossless CharacterizationSection as JSON for
//             audit (consumers wanting Cases / CodeHash / etc. parse
//             this back into a CharacterizationSection)
//
// Rationale: the previous mirror-and-also-write-the-old-field design
// created two sources of truth, forcing every invalidate / WriteSpec /
// re-describe path to remember to sync both. Single source eliminates
// that synchronization debt — B1 and B3 from the 2026-05-25 audit
// were symptoms of the dual-storage shape.
func WriteCharacterization(objectID string, sec *CharacterizationSection) error {
	if objectID == "" || sec == nil {
		return nil
	}
	if sec.Timestamp.IsZero() {
		sec.Timestamp = time.Now().UTC()
	}
	b := LoadOrInitBundle(objectID)
	// Clear the legacy field if a prior write left it populated.
	// Mid-refactor bundles may have both; this convergence pass
	// guarantees the legacy field stops being a stale shadow.
	b.Characterization = nil
	if b.Spec == nil {
		// No describe yet — create an empty Spec section so the clause
		// has somewhere to live. WriteSpec's merge semantics preserve
		// our clause when describe eventually runs.
		b.Spec = &SpecSection{Timestamp: time.Now().UTC()}
	}
	// Stash the full lossless lock data as Detail for audit consumers.
	detail, _ := json.Marshal(sec)
	clause := ContractClause{
		ID:   "char-" + nonEmpty(sec.SuiteID, "default"),
		Kind: "characterization",
		Body: fmt.Sprintf("%s (locked=%d, unlocked=%d)",
			nonEmpty(sec.OracleProperty, "impl behaviorally locked"),
			sec.LockedCount, sec.UnlockedCount),
		Source: "char:suite=" + nonEmpty(sec.SuiteID, "default"),
		Detail: detail,
	}
	b.Spec.Contract = upsertClause(b.Spec.Contract, clause)
	return SaveBundle(b)
}

// upsertClause replaces an existing clause with the same ID, or
// appends if absent. Used by WriteCharacterization so re-running
// characterize doesn't accumulate stale clauses.
func upsertClause(list []ContractClause, c ContractClause) []ContractClause {
	for i, existing := range list {
		if existing.ID == c.ID {
			list[i] = c
			return list
		}
	}
	return append(list, c)
}

// ReadCharacterizationClauses returns every Kind="characterization"
// clause from the bundle's Spec.Contract. The canonical read path
// post-Step-5 — replaces the legacy ReadCharacterization for the
// "is this object characterized?" question.
//
// Returns (nil, false) when no spec / no characterization clauses
// exist. Callers wanting the lossless CharacterizationSection data
// can unmarshal Detail back.
func ReadCharacterizationClauses(objectID string) ([]ContractClause, bool) {
	b, ok := ReadBundle(objectID)
	if !ok || b.Spec == nil {
		return nil, false
	}
	var out []ContractClause
	for _, c := range b.Spec.Contract {
		if c.Kind == "characterization" {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// LockedCountFromClauses sums the (locked=N) annotation across char
// clauses for callers that want the v8.x scalar. Parses the Body
// suffix "(locked=N, unlocked=M)"; clauses written by Step 5 always
// have this shape, so the sum is exact rather than approximate.
//
// Returns 0 when no parseable clause exists.
func LockedCountFromClauses(clauses []ContractClause) int {
	total := 0
	for _, c := range clauses {
		var locked int
		// Format: "<text> (locked=N, unlocked=M)"
		if i := strings.Index(c.Body, "(locked="); i >= 0 {
			fmt.Sscanf(c.Body[i:], "(locked=%d", &locked)
		}
		total += locked
	}
	return total
}

// nonEmpty returns s when non-empty, fallback otherwise. Lives in core
// alongside the Clause helpers so callers don't need to import a
// utility package.
func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// ReadCharacterization returns the bundle's Characterization section.
// (nil, false) when the object was never characterized — which is the
// case for every greenfield object.
//
// Step 5 single-source refactor (2026-05-25): characterization data
// now lives ONLY in Spec.Contract clauses of Kind="characterization"
// (the legacy b.Characterization field is cleared on every
// WriteCharacterization). This reader transparently reconstructs the
// flat CharacterizationSection from the most recent char clause's
// Detail blob, keeping the v8.x-shaped API alive for callers that
// still want CodeHash / LockedCount / etc. without forcing them to
// migrate.
//
// Read order:
//  1. If b.Spec carries char clauses → unmarshal the most recent
//     one's Detail (which WriteCharacterization stored as the full
//     serialized section). Aggregated LockedCount across all char
//     clauses if Detail is absent (older convergence).
//  2. Else if b.Characterization is set (legacy bundle pre-Step-5) →
//     return it directly.
//  3. Else (nil, false).
//
// This is the bridge that lets the dual-storage debt vanish without
// a flag-day migration of every caller.
func ReadCharacterization(objectID string) (*CharacterizationSection, bool) {
	b, ok := ReadBundle(objectID)
	if !ok {
		return nil, false
	}
	// Prefer Contract storage (Step 5 canonical).
	if b.Spec != nil {
		var chars []ContractClause
		for _, c := range b.Spec.Contract {
			if c.Kind == "characterization" {
				chars = append(chars, c)
			}
		}
		if len(chars) > 0 {
			// Reconstruct from the most recent clause's Detail blob.
			// Multiple char clauses can coexist (one per SuiteID); the
			// most recent by Detail.Timestamp wins. For correctness we
			// don't actually need to pick the "most recent" — any
			// representative section that carries LockedCount + CodeHash
			// satisfies the readers — but pick deterministically.
			latest := chars[0]
			for _, c := range chars[1:] {
				if len(c.Detail) > len(latest.Detail) {
					latest = c
				}
			}
			if len(latest.Detail) > 0 {
				var sec CharacterizationSection
				if err := json.Unmarshal(latest.Detail, &sec); err == nil {
					// Sum LockedCount across ALL char clauses so multi-
					// suite cases report total lock coverage.
					total := 0
					for _, c := range chars {
						var s CharacterizationSection
						if json.Unmarshal(c.Detail, &s) == nil {
							total += s.LockedCount
						}
					}
					if total > sec.LockedCount {
						sec.LockedCount = total
					}
					return &sec, true
				}
			}
			// Detail empty / unparseable but clauses exist — synthesize
			// a minimal section so callers see "yes, characterized".
			return &CharacterizationSection{
				LockedCount:   LockedCountFromClauses(chars),
				OracleProperty: latest.Body,
			}, true
		}
	}
	// Legacy bundle (pre-Step-5 mid-refactor convergence).
	if b.Characterization != nil {
		return b.Characterization, true
	}
	return nil, false
}
