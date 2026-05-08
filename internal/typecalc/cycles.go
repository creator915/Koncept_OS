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
// object before the agent must escalate to an obstacle. 3 is chosen
// empirically: previous runs showed agents commonly need 1-2 cycles
// to converge on real fixes; cycle 3+ is usually a sign of a
// structural problem the agent can't fix by trying harder.
const CycleCap = 3

// CycleEvidence is the on-disk counter.
type CycleEvidence struct {
	ObjectID  string    `json:"objectId"`
	Count     int       `json:"count"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// IncrementCycle bumps the per-object review-cycle counter and
// returns the new count.
func IncrementCycle(objectID string) (int, error) {
	if objectID == "" {
		return 0, nil
	}
	if err := os.MkdirAll(EvidenceDir, 0o755); err != nil {
		return 0, fmt.Errorf("mkdir evidence dir: %w", err)
	}
	rec := &CycleEvidence{ObjectID: objectID}
	if existing, ok := ReadCycle(objectID); ok {
		rec.Count = existing.Count
	}
	rec.Count++
	rec.UpdatedAt = time.Now().UTC()
	raw, _ := json.MarshalIndent(rec, "", "  ")
	if err := os.WriteFile(CycleEvidencePath(objectID), raw, 0o644); err != nil {
		return 0, err
	}
	return rec.Count, nil
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
