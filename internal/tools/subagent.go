package tools

import (
	"context"
	"fmt"

	"github.com/creator915/Koncept_OS/internal/llm"
	"github.com/creator915/Koncept_OS/internal/session"
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

	// Role names a CapSet preset (implementer / tester / integrator / root).
	// Resolved by the agent package via typecalc.PresetByName. Mutually
	// exclusive with Caps; if both are set Role wins.
	Role string

	// Caps is the explicit capability list, in canonical token form
	// (e.g. "read_file:K/defs/*", "run_tool:graph_validate"). Used when
	// the parent wants finer control than a preset gives. The agent
	// package enforces child ⊆ parent (§6.1) before spawning.
	Caps []string
}

func subAgentTool(runner SubAgentRunner) Tool {
	return Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name: "spawn_subagent",
				Description: "Spawn a child agent to handle a focused, self-contained sub-task. The child runs in its own conversation context — it does NOT see your messages — and returns a single summary string when done. The child shares K/* state with you (graph, sessions, checkpoint) but its tool-call detail and reasoning stay out of your context.\n\n**This is the canonical way to do CLAUDE.md §5.4 path B** (one sub-agent per testable object). When the work involves ≥3 independent objects, prefer spawning one child per object over working through them sequentially in your own context.\n\nUse this when:\n- a sub-task is well-scoped (one object's implementation; one type analysis; one file's refactor) — i.e. a child can succeed with the task description alone\n- your context is getting large and you want sub-task detail out\n- you explicitly want isolation: a child's failure won't contaminate your reasoning\n\n**session_id auto-creation (v8.8):** if you pass session_id and that session does not yet exist, it will be auto-created (parent = your currently-focused session, task = the spawn task), activated, and focused before the child runs. This collapses the canonical path B sequence — session_create → session_status active → session_focus → spawn_subagent — into one tool call. If the session already exists, the child just focuses on it (same as pre-v8.8). Focus is restored to your previous state on return.\n\nThe child has the same tool set as you, including (recursively, up to a depth cap) spawn_subagent.\n\nOptional capability scoping (§6 of docs/TypeCalculator.md): pass `role` to use a preset (implementer / tester / integrator / root) or `caps` for an explicit token list. If you pass either, every tool call in the child is gated against that set; tool calls outside the set return PermissionDenied. Child caps must be a subset of yours.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"task": map[string]interface{}{
							"type":        "string",
							"description": "Self-contained instruction for the child agent.",
						},
						"session_id": map[string]interface{}{
							"type":        "string",
							"description": "Optional. KonceptOS session id to auto-focus during the child's run. If the session does not exist, it is auto-created (parent=your-current-focus, task=task) before the child runs.",
						},
						"max_iterations": map[string]interface{}{
							"type":        "integer",
							"description": "Optional. Cap on the child's LLM iterations (default 25).",
						},
						"role": map[string]interface{}{
							"type":        "string",
							"description": "Optional. Capability preset: implementer | tester | integrator | root. Mutually exclusive with caps.",
						},
						"caps": map[string]interface{}{
							"type":        "array",
							"items":       map[string]interface{}{"type": "string"},
							"description": "Optional. Explicit capability tokens, e.g. ['read_file:K/defs/*','run_tool:graph_validate']. Must be a subset of your caps.",
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
			if sid, ok := args["session_id"].(string); ok && sid != "" {
				// v8.8: if the session doesn't exist, create it on the
				// caller's behalf. Inherits parent from the parent's
				// current focus, with task from this spawn — that
				// represents the canonical "delegate this slice of work"
				// flow. If session.Start errors because the id is
				// already taken, that's fine and we proceed (the child
				// will simply focus on the existing session); other
				// errors propagate.
				normalized, nerr := session.NormalizeID(sid)
				if nerr == nil {
					if !session.Exists(session.DefaultDir, normalized) {
						parent, _ := session.GetFocus(session.DefaultDir)
						if _, sterr := session.Start(session.DefaultDir, normalized, parent, task, session.Input{}); sterr != nil {
							// Not fatal — the runner's own focus
							// handling will retry; surface the error
							// in the returned summary so the agent
							// can see what happened.
							return "", fmt.Errorf("spawn_subagent: auto-create session %s failed: %w", normalized, sterr)
						}
					}
					req.SessionID = normalized
				} else {
					req.SessionID = sid
				}
			}
			if iters, ok := args["max_iterations"].(float64); ok {
				req.MaxIterations = int(iters)
			}
			if role, ok := args["role"].(string); ok {
				req.Role = role
			}
			if rawCaps, ok := args["caps"].([]interface{}); ok {
				for _, c := range rawCaps {
					if s, ok := c.(string); ok {
						req.Caps = append(req.Caps, s)
					}
				}
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
