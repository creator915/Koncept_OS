package typecalctools

import (
	"context"
	"fmt"
	"strings"

	"github.com/creator915/Koncept_OS/internal/llm"
	"github.com/creator915/Koncept_OS/internal/typecalc"
)

// typecalcObstacleTool records an explicit "I cannot make this object
// pass review through more iterations" signal. Once written, the gate
// blocks the object until either:
//   - the obstacle file is removed (agent or human deletes it), OR
//   - a waiver record is paired with it
//
// Use this when typecalc_review has hit the cycle cap (3+ failed
// rounds on the same object) AND further automated iteration is
// unlikely to help. The reason becomes part of the project's
// obstacles surface for human review.
//
// Obstacles are NOT failures — they are STRUCTURED escalations.
// They tell kcpos "I'm stopping the autopilot for this object".
func typecalcObstacleTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name: "typecalc_obstacle",
				Description: "Record a structured obstacle: this object cannot make further automated progress on the describe/synthesize/test/review chain. Use after the iteration cap (3 failed reviews on the same object) or when the agent realises a problem is genuinely structural and more retries won't help.\n\nRequires a specific reason explaining WHAT the structural problem is. Examples: 'spec ambiguous about return shape — needs human clarification', 'graph declares ports a function does not produce — port_observation cannot bridge', 'test runner cannot drive this language without external instrumentation'.\n\nThe gate refuses to confirm an object with an obstacle file unless either: (a) the file is removed, or (b) a waiver is also present.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object_id": map[string]interface{}{
							"type":        "string",
							"description": "Graph object id that's stuck.",
						},
						"reason": map[string]interface{}{
							"type":        "string",
							"description": "Specific structural problem (≥40 chars). What's wrong, and why retries won't fix it.",
						},
					},
					"required": []string{"object_id", "reason"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			objectID, _ := args["object_id"].(string)
			reason, _ := args["reason"].(string)
			if objectID == "" {
				return "", fmt.Errorf("object_id required")
			}
			reason = strings.TrimSpace(reason)
			if len(reason) < 40 {
				return "", fmt.Errorf("reason too short (%d chars) — describe the structural problem in enough detail for a human reviewer to act", len(reason))
			}
			cycles := 0
			if c, ok := typecalc.ReadCycle(objectID); ok {
				cycles = c.Count
			}
			rec := &typecalc.ObstacleEvidence{
				ObjectID: objectID,
				Reason:   reason,
				Cycles:   cycles,
			}
			if err := typecalc.WriteObstacle(rec); err != nil {
				return "", err
			}
			return fmt.Sprintf(
				"obstacle recorded for %s — wrote %s\n  cycles before obstacle: %d\n  reason: %s\n\n"+
					"Gate will refuse to confirm %s until this obstacle is resolved (delete the file once fixed, or pair with typecalc_waive).",
				objectID,
				typecalc.ObstacleEvidencePath(objectID),
				cycles,
				reason,
				objectID,
			), nil
		},
	}
}
