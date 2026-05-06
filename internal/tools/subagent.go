package tools

import (
	"context"
	"fmt"

	"github.com/creator915/Koncept_OS/internal/chat"
)

// SubAgentRunner is the contract a top-level agent injects so the
// `spawn_subagent` tool can fork a fresh agent loop without creating an
// import cycle (tools cannot import agent). The agent package implements
// this interface; main.go wires up an instance and passes it to
// BuiltinsWithSubAgent.
type SubAgentRunner interface {
	Run(ctx context.Context, req SubAgentRequest) (string, error)
}

// SubAgentRequest is the payload a parent agent passes to spawn one child.
type SubAgentRequest struct {
	// Task is the self-contained instruction for the child. The child does
	// NOT see the parent's conversation; the task must include all needed
	// context.
	Task string

	// SessionID, when set, focuses the child on that KonceptOS session for
	// the duration of its run. Graph mutations the child makes are recorded
	// to the session's graphDiff. Focus is restored to whatever the parent
	// had on completion.
	SessionID string

	// MaxIterations caps the child's internal LLM round-trips. 0 = use the
	// agent package's default.
	MaxIterations int
}

func subAgentTool(runner SubAgentRunner) Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name: "spawn_subagent",
				Description: "Spawn a child agent to handle a focused, self-contained sub-task. The child runs in its own conversation context — it does NOT see your messages — and returns a single summary string when done. The child shares K/* state with you (graph, sessions, checkpoint) but its tool-call detail and reasoning stay out of your context.\n\nUse this when:\n- a sub-task is well-scoped (one object's implementation; one type analysis; one file's refactor) — i.e. a child can succeed with the task description alone\n- your context is getting large and you want sub-task detail out\n- you explicitly want isolation: a child's failure won't contaminate your reasoning\n\nIf session_id is provided, the child auto-focuses on that KonceptOS session — its graph mutations record to that session's graphDiff. Focus is restored to your previous state on return.\n\nThe child has the same tool set as you, including (recursively, up to a depth cap) spawn_subagent.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"task": map[string]interface{}{
							"type":        "string",
							"description": "Self-contained instruction for the child agent.",
						},
						"session_id": map[string]interface{}{
							"type":        "string",
							"description": "Optional. KonceptOS session id to auto-focus during the child's run.",
						},
						"max_iterations": map[string]interface{}{
							"type":        "integer",
							"description": "Optional. Cap on the child's LLM iterations (default 25).",
						},
					},
					"required": []string{"task"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			if runner == nil {
				return "", fmt.Errorf("spawn_subagent: no runner registered (this should not happen — check main.go wiring)")
			}
			task, _ := args["task"].(string)
			if task == "" {
				return "", fmt.Errorf("task required")
			}
			req := SubAgentRequest{Task: task}
			if sid, ok := args["session_id"].(string); ok {
				req.SessionID = sid
			}
			if iters, ok := args["max_iterations"].(float64); ok {
				req.MaxIterations = int(iters)
			}
			return runner.Run(ctx, req)
		},
	}
}

// BuiltinsWithSubAgent returns the standard tool set augmented with
// spawn_subagent. Pass nil to omit the spawn tool (used for the deepest
// allowed depth — see internal/agent for the cap).
func BuiltinsWithSubAgent(runner SubAgentRunner) map[string]Tool {
	out := Builtins()
	if runner != nil {
		out["spawn_subagent"] = subAgentTool(runner)
	}
	return out
}
