package services

import (
	"github.com/creator915/Koncept_OS/internal/app/workflow"
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/creator915/Koncept_OS/internal/domain/checkpoint"
	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// Checkpoint tools maintain K/checkpoint.json — the project verification
// ledger. Mechanical verification only: codeProof (file:line + symbol);
// no gameplayProof / UI / runtime-simulation requirements, because those
// can't be reliably checked by an agent.

func checkpointAddItemTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "checkpoint_add_item",
				Description: "Add a verification item to K/checkpoint.json. Allowed only before freeze. Each item describes one mechanically-verifiable requirement: must (blocks PASS), should (warning only), or waiver (explicitly excused with reason). Description should include 'how to observe this passes' — i.e. what code/file to point at.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":           map[string]interface{}{"type": "string", "description": "Pattern: CHK-<alphanumeric>, e.g. CHK-001, CHK-auth_basic."},
						"description":  map[string]interface{}{"type": "string", "description": "What this requirement is + how to verify."},
						"category":     map[string]interface{}{"type": "string", "description": "Optional grouping label."},
						"severity":     map[string]interface{}{"type": "string", "description": "must | should | waiver"},
						"waiverReason": map[string]interface{}{"type": "string", "description": "Required when severity=waiver. Why this is being excused."},
					},
					"required": []string{"id", "description", "severity"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			id, _ := args["id"].(string)
			desc, _ := args["description"].(string)
			category, _ := args["category"].(string)
			sevStr, _ := args["severity"].(string)
			waiverReason, _ := args["waiverReason"].(string)
			if err := workflow.AddItem(persistence.CheckpointDefaultPath, id, desc, category, checkpoint.Severity(sevStr), waiverReason); err != nil {
				return "", err
			}
			return fmt.Sprintf("added %s [%s]: %s", id, sevStr, truncateText(desc, 60)), nil
		},
	}
}

func checkpointFreezeTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "checkpoint_freeze",
				Description: "Freeze the checkpoint item list — after this, no items can be added or removed (only filled with codeProof or waived). Idempotent: re-freezing is a no-op.",
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			if err := workflow.Freeze(persistence.CheckpointDefaultPath); err != nil {
				return "", err
			}
			c, _ := persistence.LoadCheckpoint(persistence.CheckpointDefaultPath)
			return fmt.Sprintf("frozen at %s · %d items", c.FrozenAt.Format("2006-01-02 15:04:05"), len(c.Items)), nil
		},
	}
}

func checkpointFillTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "checkpoint_fill",
				Description: "Record codeProof for an item (the file:line + key export name that satisfies it, e.g. 'src/Auth.impl.ts:42 LoginHandler'). Use this once the implementation is in place to demonstrate the requirement is met.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":         map[string]interface{}{"type": "string"},
						"code_proof": map[string]interface{}{"type": "string", "description": "file:line + symbol, e.g. 'src/foo.ts:42 ExportName'."},
					},
					"required": []string{"id", "code_proof"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			id, _ := args["id"].(string)
			proof, _ := args["code_proof"].(string)
			// 2026-05-09 v8.5 — checkpoint_fill must not fabricate
			// codeProof. The 5-instance v8.4 batch caught pong-03
			// filling 8 items 365 lines BEFORE its first Tested<Pass>
			// (i.e. claiming "code does X" when no test had verified
			// any X yet). The fix: require at least one graph object
			// to be in status=confirmed with passing typecalc evidence
			// before any fill is allowed. Once verified work exists,
			// subsequent fills are unrestricted (item-by-item linkage
			// is too fine-grained to enforce mechanically; the
			// safeguard is "no fills until SOMETHING was actually
			// verified to work").
			if !anyConfirmedWithPassingEvidence() {
				return "", fmt.Errorf(
					"refusing checkpoint_fill for %q: no confirmed object on K/graph.json yet has passing typecalc evidence (kind=test ok=true OR kind=insufficient+waiver). "+
						"Filling codeProof before any code has been verified amounts to fabricating evidence. Run typecalc_compile + typecalc_test on at least one object, get it to confirmed with passing evidence, THEN return to fill checkpoint items.",
					id)
			}
			if err := workflow.Fill(persistence.CheckpointDefaultPath, id, proof); err != nil {
				return "", err
			}
			return fmt.Sprintf("%s · codeProof = %s", id, proof), nil
		},
	}
}

// anyConfirmedWithPassingEvidence is the precondition for
// checkpoint_fill: at least one graph object must be confirmed AND
// have a typecalc evidence file recording ok=true (or kind=insufficient
// paired with a waiver). Returns false on a fresh project, on a
// project with all-declared objects, or when every confirmed object
// has only ok=false evidence.
func anyConfirmedWithPassingEvidence() bool {
	g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if err != nil {
		return false
	}
	for objID, obj := range g.Objects {
		if obj.Status != graph.StatusConfirmed {
			continue
		}
		// v9.0: route through ObjectState so the same "is this object
		// verified?" semantics power gate, hooks, and this precondition
		// check.
		st := core.LoadObjectState(objID, "")
		if st.HasUsableEvidence() {
			return true
		}
	}
	return false
}

func checkpointWaiveTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "checkpoint_waive",
				Description: "Convert an existing item to a waiver with a reason. Use this post-freeze when a planned check turns out to be infeasible to verify mechanically. Waivers count as resolved — they do not block PASS, but the reason becomes part of the audit trail.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":     map[string]interface{}{"type": "string"},
						"reason": map[string]interface{}{"type": "string", "description": "Why this check is being waived."},
					},
					"required": []string{"id", "reason"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			id, _ := args["id"].(string)
			reason, _ := args["reason"].(string)
			if err := workflow.Waive(persistence.CheckpointDefaultPath, id, reason); err != nil {
				return "", err
			}
			return fmt.Sprintf("%s waived · reason: %s", id, reason), nil
		},
	}
}

func checkpointShowTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "checkpoint_show",
				Description: "Show the checkpoint summary and items. Pass id=<CHK-xxx> to focus on a single item, or omit to see the full ledger with verdict. Read-only.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id": map[string]interface{}{"type": "string", "description": "Optional. Specific item id."},
					},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			c, err := persistence.LoadCheckpointOrInit(persistence.CheckpointDefaultPath)
			if err != nil {
				return "", err
			}
			c.RecomputeSummary()
			id, _ := args["id"].(string)
			if id != "" {
				idx := c.FindItem(id)
				if idx < 0 {
					return "", fmt.Errorf("item %s not found", id)
				}
				return formatItem(&c.Items[idx]), nil
			}
			return formatCheckpoint(c), nil
		},
	}
}

func formatCheckpoint(c *checkpoint.Checkpoint) string {
	var b strings.Builder
	frozen := "no (mutable)"
	if c.Frozen {
		frozen = "yes · " + c.FrozenAt.Format("2006-01-02 15:04:05")
	}
	fmt.Fprintf(&b, "checkpoint: %s\n", c.Summary.FinalVerdict)
	fmt.Fprintf(&b, "  frozen: %s\n", frozen)
	fmt.Fprintf(&b, "  totalItems=%d · passed=%d · waived=%d · failed=%d\n",
		c.Summary.TotalItems, c.Summary.Passed, c.Summary.Waived, c.Summary.Failed)
	if len(c.Items) == 0 {
		fmt.Fprintln(&b, "  (no items)")
		return b.String()
	}
	// Sort items by id for stable output.
	idx := make([]int, len(c.Items))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return c.Items[idx[a]].ID < c.Items[idx[b]].ID })
	fmt.Fprintln(&b, "  items:")
	for _, i := range idx {
		fmt.Fprintf(&b, "    %s\n", oneLine(&c.Items[i]))
	}
	return b.String()
}

func formatItem(it *checkpoint.Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s · [%s]\n", it.ID, it.Severity)
	fmt.Fprintf(&b, "  description: %s\n", it.Description)
	if it.Category != "" {
		fmt.Fprintf(&b, "  category: %s\n", it.Category)
	}
	if it.CodeProof != "" {
		fmt.Fprintf(&b, "  codeProof: %s\n", it.CodeProof)
	}
	if it.WaiverReason != "" {
		fmt.Fprintf(&b, "  waiverReason: %s\n", it.WaiverReason)
	}
	if !it.VerifiedAt.IsZero() {
		fmt.Fprintf(&b, "  verifiedAt: %s\n", it.VerifiedAt.Format("2006-01-02 15:04:05"))
	}
	return b.String()
}

func oneLine(it *checkpoint.Item) string {
	state := "·"
	switch {
	case it.Severity == checkpoint.SeverityWaiver:
		state = "WAIVED"
	case it.CodeProof != "":
		state = "FILLED"
	case it.Severity == checkpoint.SeverityMust:
		state = "MISSING"
	default:
		state = "(unfilled)"
	}
	return fmt.Sprintf("%s · [%s] · %s · %s", it.ID, it.Severity, state, truncateText(it.Description, 50))
}
