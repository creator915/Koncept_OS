package typecalc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CycleEvidencePath is the per-object review-cycle counter file.
// Each typecalc_review invocation increments .Count; a successful
// review (ok=true) resets it. Once .Count exceeds CycleCap the
// agent is forced to either change approach or emit an obstacle —
// no more silent grinding.
func CycleEvidencePath(objectID string) string {
	return filepath.Join(EvidenceDir, objectID+".cycles.json")
}

// CycleCap is the maximum number of review attempts on the same
// object before the agent must escalate to an obstacle. 3 was the
// empirical baseline; 5 gives breathing room for the multi-axis
// failure pattern observed in v6 (each review can return static +
// runtime + reasonableness issues independently — agent sometimes
// needs distinct turns to address each axis). Combined with the
// progress-detection logic in IncrementCycleWithIssues and the
// graph-diff reset in MaybeResetCycleOnImplChange, the effective
// budget is "5 stalls" not "5 attempts" — genuine progress no
// longer burns counter.
const CycleCap = 5

// CycleEvidence is the on-disk counter. Tracks the issue rules from
// the previous failed review so IncrementCycleWithIssues can detect
// progress (issue set strictly shrunk). Also tracks the impl-hash of
// the source the previous review judged, so a graph_merge_object
// changing impl/portObservation can trigger a reset on the next call.
type CycleEvidence struct {
	ObjectID    string    `json:"objectId"`
	Count       int       `json:"count"`
	PrevIssues  []string  `json:"prevIssues,omitempty"`  // rule names from the last failed review
	PrevImplKey string    `json:"prevImplKey,omitempty"` // impl-hash + portObservation hash from last review
	UpdatedAt   time.Time `json:"updatedAt"`
}

// IncrementCycle bumps the per-object review-cycle counter and
// returns the new count. Use IncrementCycleWithIssues when issue
// rule names are available — that variant detects progress and
// avoids over-counting when the agent is genuinely fixing things.
func IncrementCycle(objectID string) (int, error) {
	return IncrementCycleWithIssues(objectID, nil, "")
}

// IncrementCycleWithIssues bumps the counter unless the new
// issues are a strict subset of the previous run's issues — in
// which case the agent has demonstrably converged some failures
// and the count is held steady. Always updates PrevIssues to the
// new list. Pass implKey to remember the source state so a later
// MaybeResetCycleOnImplChange can detect structural changes.
func IncrementCycleWithIssues(objectID string, currentIssues []string, implKey string) (int, error) {
	if objectID == "" {
		return 0, nil
	}
	if err := os.MkdirAll(EvidenceDir, 0o755); err != nil {
		return 0, fmt.Errorf("mkdir evidence dir: %w", err)
	}
	rec := &CycleEvidence{ObjectID: objectID}
	if existing, ok := ReadCycle(objectID); ok {
		rec.Count = existing.Count
		// Progress detection: if the set of failing rule names is a
		// strict subset of the previous run, the agent fixed at least
		// one issue and didn't introduce new ones — that's progress,
		// not stalling. Don't burn cycle budget.
		if isStrictSubset(currentIssues, existing.PrevIssues) {
			// hold count
		} else {
			rec.Count++
		}
	} else {
		rec.Count = 1
	}
	rec.PrevIssues = dedupSorted(currentIssues)
	rec.PrevImplKey = implKey
	rec.UpdatedAt = time.Now().UTC()
	raw, _ := json.MarshalIndent(rec, "", "  ")
	if err := os.WriteFile(CycleEvidencePath(objectID), raw, 0o644); err != nil {
		return 0, err
	}
	return rec.Count, nil
}

// MaybeResetCycleOnImplChange clears the counter when the source
// state has changed since the last review (different impl-hash or
// portObservation). The intuition: if the artifact under judgment
// has structurally changed, the previous failure record is
// historical — the agent deserves a fresh budget on the new
// artifact, not a head start of "you already burned 3 cycles on
// the OLD code."
//
// Returns true if the counter was reset.
func MaybeResetCycleOnImplChange(objectID, currentImplKey string) bool {
	if objectID == "" || currentImplKey == "" {
		return false
	}
	existing, ok := ReadCycle(objectID)
	if !ok {
		return false
	}
	if existing.PrevImplKey == "" || existing.PrevImplKey == currentImplKey {
		return false
	}
	_ = ResetCycle(objectID)
	return true
}

// isStrictSubset returns true when every element of `a` is also in
// `b`, AND `a` is shorter than `b` (so at least one element was
// removed). Empty `a` with non-empty `b` qualifies (full
// resolution counts as progress; the caller is expected to mark
// success via ResetCycle in that case rather than using this
// function, but the semantics still hold).
func isStrictSubset(a, b []string) bool {
	if len(a) >= len(b) {
		return false
	}
	if len(b) == 0 {
		return false
	}
	bs := map[string]bool{}
	for _, x := range b {
		bs[x] = true
	}
	for _, x := range a {
		if !bs[x] {
			return false
		}
	}
	return true
}

// dedupSorted normalizes a slice for stable comparison: dedup,
// sort lexicographically. Empty in → empty out.
func dedupSorted(xs []string) []string {
	if len(xs) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sortStrings(out)
	return out
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}

// ResetCycle zeroes the counter (called on a successful review).
func ResetCycle(objectID string) error {
	if objectID == "" {
		return nil
	}
	rec := &CycleEvidence{ObjectID: objectID, Count: 0, UpdatedAt: time.Now().UTC()}
	raw, _ := json.MarshalIndent(rec, "", "  ")
	if err := os.MkdirAll(EvidenceDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(CycleEvidencePath(objectID), raw, 0o644)
}

// ReadCycle loads the counter. Returns (zero, false) on missing.
func ReadCycle(objectID string) (*CycleEvidence, bool) {
	if objectID == "" {
		return nil, false
	}
	raw, err := os.ReadFile(CycleEvidencePath(objectID))
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	var rec CycleEvidence
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, false
	}
	return &rec, true
}

// ObstacleEvidencePath is where an explicit "I cannot continue"
// signal lands. Once written, the gate refuses to confirm the object
// until a human inspects and either resolves the obstacle (deletes
// the file) or upgrades to a waiver.
func ObstacleEvidencePath(objectID string) string {
	return filepath.Join(EvidenceDir, objectID+".obstacle.json")
}

// ObstacleEvidence captures why the agent gave up on this object.
type ObstacleEvidence struct {
	ObjectID    string    `json:"objectId"`
	Kind        string    `json:"kind"` // always "obstacle"
	Reason      string    `json:"reason"`
	Cycles      int       `json:"cyclesWhenObstacled"`
	Timestamp   time.Time `json:"timestamp"`
}

// WriteObstacle persists an obstacle record. When file exists, the
// gate fails the object — only a human (or explicit waiver) can
// proceed.
func WriteObstacle(rec *ObstacleEvidence) error {
	if rec == nil || rec.ObjectID == "" {
		return nil
	}
	if err := os.MkdirAll(EvidenceDir, 0o755); err != nil {
		return err
	}
	rec.Kind = "obstacle"
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}
	raw, _ := json.MarshalIndent(rec, "", "  ")
	return os.WriteFile(ObstacleEvidencePath(rec.ObjectID), raw, 0o644)
}

// ReadObstacle returns the obstacle record if present.
func ReadObstacle(objectID string) (*ObstacleEvidence, bool) {
	if objectID == "" {
		return nil, false
	}
	raw, err := os.ReadFile(ObstacleEvidencePath(objectID))
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	var rec ObstacleEvidence
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, false
	}
	return &rec, true
}
