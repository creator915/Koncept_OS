package core

import (
	"encoding/json"
	"fmt"
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

// WriteCharacterization stores the characterization lock as the
// bundle's Characterization section, leaving every other section
// untouched (load → set one section → save, the established
// non-disturbing pattern used by WriteSpec/WriteTests/etc.).
//
// 2026-05-22 (Step 5 of contract landing): ALSO mirrors the lock into
// the Spec section's Contract field as a Kind="characterization"
// clause. This is the single-source-of-truth migration path —
// downstream readers (gate, synth) progressively shift from
// CharacterizationSection to Contract clauses; once every reader has
// migrated, the standalone section can be retired. The mirror is
// idempotent (dedup by clause ID).
func WriteCharacterization(objectID string, sec *CharacterizationSection) error {
	if objectID == "" || sec == nil {
		return nil
	}
	if sec.Timestamp.IsZero() {
		sec.Timestamp = time.Now().UTC()
	}
	b := LoadOrInitBundle(objectID)
	b.Characterization = sec
	// Mirror as a Contract characterization clause. Use a stable ID
	// derived from SuiteID so repeated WriteCharacterization calls (e.g.
	// after re-running equiv_oracle) overwrite the same clause rather
	// than accumulating duplicates. Source carries SuiteID for audit.
	if b.Spec == nil {
		// No describe yet — create an empty Spec section so the clause
		// has somewhere to live. Description stays empty; the gate's
		// stale-spec rule continues to fire if needed, and the next
		// describe will populate Description without dropping our
		// mirrored clause (WriteSpec preserves Contract).
		b.Spec = &SpecSection{Timestamp: time.Now().UTC()}
	}
	mirror := ContractClause{
		ID:   "char-" + nonEmpty(sec.SuiteID, "default"),
		Kind: "characterization",
		Body: fmt.Sprintf("%s (locked=%d, unlocked=%d)",
			nonEmpty(sec.OracleProperty, "impl behaviorally locked"),
			sec.LockedCount, sec.UnlockedCount),
		Source: "char:suite=" + nonEmpty(sec.SuiteID, "default"),
	}
	b.Spec.Contract = upsertClause(b.Spec.Contract, mirror)
	return SaveBundle(b)
}

// upsertClause replaces an existing clause with the same ID, or
// appends if absent. Used by WriteCharacterization's mirror so
// re-running characterize doesn't accumulate stale clauses.
func upsertClause(list []ContractClause, c ContractClause) []ContractClause {
	for i, existing := range list {
		if existing.ID == c.ID {
			list[i] = c
			return list
		}
	}
	return append(list, c)
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
func ReadCharacterization(objectID string) (*CharacterizationSection, bool) {
	b, ok := ReadBundle(objectID)
	if !ok || b.Characterization == nil {
		return nil, false
	}
	return b.Characterization, true
}
