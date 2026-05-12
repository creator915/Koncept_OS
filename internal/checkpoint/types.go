// Package checkpoint implements the project verification checklist:
// each requirement is added as an item, the list is frozen, then each
// must-item is filled with a codeProof before the project is considered
// done. Mechanical verification only — no gameplayProof / UI / runtime
// simulation. v8.7 attempted to introduce gameplayProof as an optional
// proof slot, v8.8 removed it: in a CLI agent without reliable headless
// browser tooling, gameplayProof devolved into either fabricated
// descriptive text or systematic skip — neither has verification value.
// The codeProof + typecalc evidence chain is the source of truth for
// "this code does what intent says", and `kcpos snap` (v8.9+, planned)
// will provide artifact-based runtime evidence as a separate system if
// needed — not as a checkpoint field.
//
// Storage: K/checkpoint.json — one per project.
//
// Workflow:
//   1. agent reads requirements doc, calls checkpoint_add_item per item
//      while planning (status: not frozen)
//   2. checkpoint_freeze locks the item list — no more adds/removes
//   3. agent does work; calls checkpoint_fill for each item with codeProof
//      pointing at the implementation
//   4. checkpoint_show summarizes; finalVerdict is PASS once every "must"
//      item has a codeProof and no "must" is missing.
package checkpoint

import (
	"fmt"
	"regexp"
	"time"
)

// Severity classifies how blocking a checkpoint item is.
//
// must:    blocks finalVerdict=PASS if codeProof missing.
// should:  reported but doesn't block PASS.
// waiver:  explicitly excused with WaiverReason (must include "why").
type Severity string

const (
	SeverityMust   Severity = "must"
	SeverityShould Severity = "should"
	SeverityWaiver Severity = "waiver"
)

func IsValidSeverity(s string) bool {
	return s == string(SeverityMust) || s == string(SeverityShould) || s == string(SeverityWaiver)
}

// Item is a single verification requirement.
type Item struct {
	ID           string    `json:"id"`
	Description  string    `json:"description"`
	Category     string    `json:"category,omitempty"`
	Severity     Severity  `json:"severity"`
	CodeProof    string    `json:"codeProof,omitempty"`
	WaiverReason string    `json:"waiverReason,omitempty"`
	VerifiedAt   time.Time `json:"verifiedAt,omitempty"`
}

// Filled reports whether this item has a codeProof (or is a waiver, which
// counts as resolved).
func (i *Item) Filled() bool {
	if i.Severity == SeverityWaiver {
		return i.WaiverReason != ""
	}
	return i.CodeProof != ""
}

// Summary is computed each Save; not the source of truth.
type Summary struct {
	TotalItems   int    `json:"totalItems"`
	Passed       int    `json:"passed"` // must|should items with codeProof
	Waived       int    `json:"waived"`
	Failed       int    `json:"failed"` // must items missing codeProof
	FinalVerdict string `json:"finalVerdict"`
}

// Verdict literals.
const (
	VerdictPass    = "PASS"
	VerdictFail    = "FAIL"
	VerdictPending = "PENDING" // not frozen yet, no point computing
)

// Checkpoint is the entire ledger.
type Checkpoint struct {
	Frozen    bool      `json:"frozen"`
	FrozenAt  time.Time `json:"frozenAt,omitempty"`
	Items     []Item    `json:"items"`
	Summary   Summary   `json:"summary"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// New returns an empty unfrozen checkpoint with current timestamps.
func New() *Checkpoint {
	now := time.Now().UTC()
	return &Checkpoint{
		Frozen:    false,
		Items:     []Item{},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// idPattern: CHK- followed by digits or alphanumerics. Permissive (the spec
// uses "CHK-001"-style; we accept "CHK-XXX" or any non-empty token after
// the prefix). Mostly to keep ids regular and easy to grep.
var idPattern = regexp.MustCompile(`^CHK-[A-Za-z0-9_]+$`)

// ValidateID enforces the CHK-<token> convention.
func ValidateID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("invalid checkpoint id %q (must match CHK-<alphanumeric>, e.g. CHK-001)", id)
	}
	return nil
}

// FindItem returns the index of the item with id, or -1.
func (c *Checkpoint) FindItem(id string) int {
	for i := range c.Items {
		if c.Items[i].ID == id {
			return i
		}
	}
	return -1
}

// RecomputeSummary updates c.Summary based on the items' current state.
// Verdict logic:
//   - If !Frozen: PENDING
//   - If any "must" item is missing codeProof: FAIL
//   - Otherwise: PASS
func (c *Checkpoint) RecomputeSummary() {
	s := Summary{TotalItems: len(c.Items)}
	if !c.Frozen {
		s.FinalVerdict = VerdictPending
		c.Summary = s
		return
	}
	failedMusts := 0
	for _, it := range c.Items {
		switch it.Severity {
		case SeverityWaiver:
			s.Waived++
		case SeverityMust, SeverityShould:
			if it.Filled() {
				s.Passed++
			} else if it.Severity == SeverityMust {
				failedMusts++
			}
		}
	}
	s.Failed = failedMusts
	if failedMusts == 0 {
		s.FinalVerdict = VerdictPass
	} else {
		s.FinalVerdict = VerdictFail
	}
	c.Summary = s
}
