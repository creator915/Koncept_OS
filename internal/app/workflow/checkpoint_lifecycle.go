package workflow

import (
	"github.com/creator915/Koncept_OS/internal/domain/checkpoint"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"fmt"
	"time"
)

// AddItem adds a new item. Refuses if the checkpoint is frozen, or if id
// already exists, or if id/severity are malformed. Persists on success.
func AddItem(path, id, description, category string, severity checkpoint.Severity, waiverReason string) error {
	c, err := persistence.LoadCheckpointOrInit(path)
	if err != nil {
		return err
	}
	if c.Frozen {
		return fmt.Errorf("checkpoint is frozen — cannot add items (only waive/fill allowed)")
	}
	if err := checkpoint.ValidateID(id); err != nil {
		return err
	}
	if !checkpoint.IsValidSeverity(string(severity)) {
		return fmt.Errorf("severity must be must|should|waiver, got %q", severity)
	}
	if description == "" {
		return fmt.Errorf("description required (must include 'how to observe this passes')")
	}
	if c.FindItem(id) >= 0 {
		return fmt.Errorf("item %s already exists", id)
	}
	if severity == checkpoint.SeverityWaiver && waiverReason == "" {
		return fmt.Errorf("waiver items require a non-empty waiverReason")
	}
	c.Items = append(c.Items, checkpoint.Item{
		ID:           id,
		Description:  description,
		Category:     category,
		Severity:     severity,
		WaiverReason: waiverReason,
	})
	c.UpdatedAt = time.Now().UTC()
	return persistence.SaveCheckpoint(path, c)
}

// Freeze locks the items list. After this, AddItem returns an error.
// Idempotent: freezing an already-frozen checkpoint is a no-op.
func Freeze(path string) error {
	c, err := persistence.LoadCheckpointOrInit(path)
	if err != nil {
		return err
	}
	if c.Frozen {
		return nil
	}
	if len(c.Items) == 0 {
		return fmt.Errorf("cannot freeze an empty checkpoint — add items first")
	}
	c.Frozen = true
	c.FrozenAt = time.Now().UTC()
	c.UpdatedAt = c.FrozenAt
	return persistence.SaveCheckpoint(path, c)
}

// Fill records codeProof for an item. Allowed at any time (pre or post
// freeze). codeProof should reference file:line + key export, e.g.
// "src/Op.impl.ts:42 NormalizeWeather".
func Fill(path, id, codeProof string) error {
	c, err := persistence.LoadCheckpointOrInit(path)
	if err != nil {
		return err
	}
	idx := c.FindItem(id)
	if idx < 0 {
		return fmt.Errorf("item %s not found", id)
	}
	if c.Items[idx].Severity == checkpoint.SeverityWaiver {
		return fmt.Errorf("cannot fill codeProof for a waiver item — use checkpoint_unwaive first or add a fresh item")
	}
	if codeProof == "" {
		return fmt.Errorf("codeProof required (e.g. 'src/foo.ts:42 ExportName')")
	}
	c.Items[idx].CodeProof = codeProof
	c.Items[idx].VerifiedAt = time.Now().UTC()
	c.UpdatedAt = time.Now().UTC()
	return persistence.SaveCheckpoint(path, c)
}

// Waive converts an existing must|should item to a waiver with a reason.
// This is the post-freeze escape hatch when a planned check turns out to
// be infeasible to verify mechanically.
func Waive(path, id, reason string) error {
	c, err := persistence.LoadCheckpointOrInit(path)
	if err != nil {
		return err
	}
	idx := c.FindItem(id)
	if idx < 0 {
		return fmt.Errorf("item %s not found", id)
	}
	if reason == "" {
		return fmt.Errorf("waiver reason required — explain why this check is being excused")
	}
	c.Items[idx].Severity = checkpoint.SeverityWaiver
	c.Items[idx].WaiverReason = reason
	c.Items[idx].CodeProof = "" // clear; waiver does not need code proof
	c.Items[idx].VerifiedAt = time.Now().UTC()
	c.UpdatedAt = time.Now().UTC()
	return persistence.SaveCheckpoint(path, c)
}
