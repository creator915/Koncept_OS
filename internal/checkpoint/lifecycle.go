package checkpoint

import (
	"fmt"
	"time"
)

// AddItem adds a new item. Refuses if the checkpoint is frozen, or if id
// already exists, or if id/severity are malformed. Persists on success.
func AddItem(path, id, description, category string, severity Severity, waiverReason string) error {
	c, err := LoadOrInit(path)
	if err != nil {
		return err
	}
	if c.Frozen {
		return fmt.Errorf("checkpoint is frozen — cannot add items (only waive/fill allowed)")
	}
	if err := ValidateID(id); err != nil {
		return err
	}
	if !IsValidSeverity(string(severity)) {
		return fmt.Errorf("severity must be must|should|waiver, got %q", severity)
	}
	if description == "" {
		return fmt.Errorf("description required (must include 'how to observe this passes')")
	}
	if c.FindItem(id) >= 0 {
		return fmt.Errorf("item %s already exists", id)
	}
	if severity == SeverityWaiver && waiverReason == "" {
		return fmt.Errorf("waiver items require a non-empty waiverReason")
	}
	c.Items = append(c.Items, Item{
		ID:           id,
		Description:  description,
		Category:     category,
		Severity:     severity,
		WaiverReason: waiverReason,
	})
	c.UpdatedAt = time.Now().UTC()
	return Save(path, c)
}

// Freeze locks the items list. After this, AddItem returns an error.
// Idempotent: freezing an already-frozen checkpoint is a no-op.
func Freeze(path string) error {
	c, err := LoadOrInit(path)
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
	return Save(path, c)
}

// Fill records codeProof (and optionally gameplayProof) for an item.
// Allowed at any time (pre or post freeze). codeProof should reference
// file:line + key export, e.g. "src/Op.impl.ts:42 NormalizeWeather".
// gameplayProof (v8.7) should reference reproduction steps + paths under
// K/proofs/<id>/ — empty string is allowed and will trigger the
// [gameplay-proof-required] gate rule only if the project has an
// executable deliverable.
//
// Either proof can be filled independently; subsequent calls overwrite
// only the slots that are non-empty, so the agent can fill code first
// and gameplay later (or vice versa) without losing prior progress.
func Fill(path, id, codeProof, gameplayProof string) error {
	c, err := LoadOrInit(path)
	if err != nil {
		return err
	}
	idx := c.FindItem(id)
	if idx < 0 {
		return fmt.Errorf("item %s not found", id)
	}
	if c.Items[idx].Severity == SeverityWaiver {
		return fmt.Errorf("cannot fill codeProof for a waiver item — use checkpoint_unwaive first or add a fresh item")
	}
	if codeProof == "" && gameplayProof == "" {
		return fmt.Errorf("at least one of codeProof or gameplayProof required (codeProof e.g. 'src/foo.ts:42 ExportName'; gameplayProof e.g. 'spawn / move left 30 / attack 5 — see K/proofs/CHK-001/final.png')")
	}
	if codeProof != "" {
		c.Items[idx].CodeProof = codeProof
	}
	if gameplayProof != "" {
		c.Items[idx].GameplayProof = gameplayProof
	}
	c.Items[idx].VerifiedAt = time.Now().UTC()
	c.UpdatedAt = time.Now().UTC()
	return Save(path, c)
}

// Waive converts an existing must|should item to a waiver with a reason.
// This is the post-freeze escape hatch when a planned check turns out to
// be infeasible to verify mechanically.
func Waive(path, id, reason string) error {
	c, err := LoadOrInit(path)
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
	c.Items[idx].Severity = SeverityWaiver
	c.Items[idx].WaiverReason = reason
	c.Items[idx].CodeProof = "" // clear; waiver does not need code proof
	c.Items[idx].VerifiedAt = time.Now().UTC()
	c.UpdatedAt = time.Now().UTC()
	return Save(path, c)
}
