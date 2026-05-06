package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/creator915/Koncept_OS/internal/chat"
	"github.com/creator915/Koncept_OS/internal/checkpoint"
)

// Checkpoint tools maintain K/checkpoint.json — the project verification
// ledger per CLAUDE.md §0 + §5.5 (convergent variant: codeProof only,
// gameplayProof omitted because mechanical agent verification cannot
// realistically simulate user interaction).

func checkpointAddItemTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
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
			if err := checkpoint.AddItem(checkpoint.DefaultPath, id, desc, category, checkpoint.Severity(sevStr), waiverReason); err != nil {
				return "", err
			}
			return fmt.Sprintf("added %s [%s]: %s", id, sevStr, truncateText(desc, 60)), nil
		},
	}
}

func checkpointFreezeTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "checkpoint_freeze",
				Description: "Freeze the checkpoint item list — after this, no items can be added or removed (only filled with codeProof or waived). Idempotent: re-freezing is a no-op.",
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			if err := checkpoint.Freeze(checkpoint.DefaultPath); err != nil {
				return "", err
			}
			c, _ := checkpoint.Load(checkpoint.DefaultPath)
			return fmt.Sprintf("frozen at %s · %d items", c.FrozenAt.Format("2006-01-02 15:04:05"), len(c.Items)), nil
		},
	}
}

func checkpointFillTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
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
			if err := checkpoint.Fill(checkpoint.DefaultPath, id, proof); err != nil {
				return "", err
			}
			return fmt.Sprintf("%s · codeProof = %s", id, proof), nil
		},
	}
}

func checkpointWaiveTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
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
			if err := checkpoint.Waive(checkpoint.DefaultPath, id, reason); err != nil {
				return "", err
			}
			return fmt.Sprintf("%s waived · reason: %s", id, reason), nil
		},
	}
}

func checkpointShowTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
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
			c, err := checkpoint.LoadOrInit(checkpoint.DefaultPath)
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
