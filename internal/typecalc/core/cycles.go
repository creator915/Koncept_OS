package core

import (
	"fmt"
	"time"
)

// CycleEvidencePath returns the bundle path; v9.0 stores the cycle
// counter as the bundle's Cycles section, not a separate file.
func CycleEvidencePath(objectID string) string { return BundlePath(objectID) }

// CycleCap is the maximum number of review attempts on the same object
// before the agent must escalate to an obstacle. See bundle.go's
// CyclesSection for the on-disk schema.
const CycleCap = 5

// CycleEvidence is the v8.x flat shape. Kept for callers; v9.0 reads
// and writes via the bundle's Cycles section.
type CycleEvidence struct {
	ObjectID    string    `json:"objectId"`
	Count       int       `json:"count"`
	PrevIssues  []string  `json:"prevIssues,omitempty"`
	PrevImplKey string    `json:"prevImplKey,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// IncrementCycle bumps the per-object review-cycle counter.
func IncrementCycle(objectID string) (int, error) {
	return IncrementCycleWithIssues(objectID, nil, "")
}

// IncrementCycleWithIssues bumps the counter unless the new issues are
// a strict subset of the previous run's — in which case the agent has
// converged some failures and the count holds steady.
func IncrementCycleWithIssues(objectID string, currentIssues []string, implKey string) (int, error) {
	if objectID == "" {
		return 0, nil
	}
	b := LoadOrInitBundle(objectID)
	current := &CyclesSection{UpdatedAt: time.Now().UTC()}
	if b.Cycles != nil {
		current.Count = b.Cycles.Count
		if isStrictSubset(currentIssues, b.Cycles.PrevIssues) {
			// hold count — progress detected
		} else {
			current.Count++
		}
	} else {
		current.Count = 1
	}
	current.PrevIssues = dedupSorted(currentIssues)
	current.PrevImplKey = implKey
	b.Cycles = current
	if err := SaveBundle(b); err != nil {
		return 0, fmt.Errorf("save bundle cycles: %w", err)
	}
	return current.Count, nil
}

// MaybeResetCycleOnImplChange clears the counter when the source state
// has changed since the last review.
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
	b := LoadOrInitBundle(objectID)
	b.Cycles = &CyclesSection{Count: 0, UpdatedAt: time.Now().UTC()}
	return SaveBundle(b)
}

// ReadCycle loads the counter from the bundle's Cycles section,
// reshaped as the v8.x flat CycleEvidence.
func ReadCycle(objectID string) (*CycleEvidence, bool) {
	b, ok := ReadBundle(objectID)
	if !ok || b.Cycles == nil {
		return nil, false
	}
	return &CycleEvidence{
		ObjectID:    objectID,
		Count:       b.Cycles.Count,
		PrevIssues:  b.Cycles.PrevIssues,
		PrevImplKey: b.Cycles.PrevImplKey,
		UpdatedAt:   b.Cycles.UpdatedAt,
	}, true
}

// ObstacleEvidence + WriteObstacle / ReadObstacle / DeleteObstacle /
// ObstacleEvidencePath — all removed in v9.2 along with the obstacle/
// waiver escape mechanism. Chain-terminal failures still emit the
// TypeObstacle router signal (see router/typecalcchain/chain.go
// makeObstacle) for the agent to read, but nothing is persisted to the
// bundle — there's no escape pathway to consume it.
