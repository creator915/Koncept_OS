package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/creator915/Koncept_OS/internal/app/workflow"
	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"github.com/creator915/Koncept_OS/internal/llm/transport"
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
	// Resolved by the agent package via core.PresetByName. Mutually
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
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "spawn_subagent",
				Description: "Spawn a child agent to handle a focused, self-contained sub-task. The child runs in its own conversation context — it does NOT see your messages — and returns a single summary string when done. The child shares K/* state with you (graph, sessions, checkpoint) but its tool-call detail and reasoning stay out of your context.\n\n**This is the canonical way to do path B** (one sub-agent per testable object). When the work involves ≥3 independent objects, prefer spawning one child per object over working through them sequentially in your own context.\n\nUse this when:\n- a sub-task is well-scoped (one object's implementation; one type analysis; one file's refactor) — i.e. a child can succeed with the task description alone\n- your context is getting large and you want sub-task detail out\n- you explicitly want isolation: a child's failure won't contaminate your reasoning\n\n**session_id auto-creation:** if you pass session_id and that session does not yet exist, it will be auto-created with **parent=the root session of your current focus chain** (walked up from the focused session, not focus itself — this fixes the v9.0.6/v9.2 chain-spawn bug where siblings ended up as descendants). Pass `parent` explicitly to override (use for intentional nesting like wave-of-waves). If you have no focus AND no parent, the new session is itself a new root.\n\nThe child has the same tool set as you, including (recursively, up to a depth cap) spawn_subagent.\n\nOptional capability scoping: pass `role` to use a preset (implementer / tester / integrator / root) or `caps` for an explicit token list. If you pass either, every tool call in the child is gated against that set; tool calls outside the set return PermissionDenied. Child caps must be a subset of yours.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"task": map[string]interface{}{
							"type":        "string",
							"description": "Self-contained instruction for the child agent.",
						},
						"session_id": map[string]interface{}{
							"type":        "string",
							"description": "Optional. KonceptOS session id to auto-focus during the child's run. If the session does not exist, it is auto-created with parent=root-of-focus-chain (v9.3 — see Description for chain-spawn fix details).",
						},
						"parent": map[string]interface{}{
							"type":        "string",
							"description": "Optional. Explicit parent session id for the auto-created session. Use to override the default (root-of-focus-chain) when you genuinely want nesting (e.g. wave-2 children of a coordinator subagent). Empty string = use auto-default.",
						},
						"expands_object": map[string]interface{}{
							"type":        "string",
							"description": "Optional but RECOMMENDED for canonical path B: the graph object id this child decomposes/implements. When set, the auto-created child session is an EXPANSION session of that object — its graph-writes land in K/expansions/<session>/graph.json, and session_finish runs the expansion gate (sub-graph all-confirmed + produce/consume correspondence) then propagates expansion+confirmed to the parent object. Must already exist as an object in K/graph.json or the spawn is refused. This is what actually engages the layered-hypergraph (Handler 3) lifecycle for one-object-per-child path B.",
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
			eoArg, _ := args["expands_object"].(string)
			expandsObject := strings.TrimSpace(eoArg)
			req := SubAgentRequest{Task: task}
			if sid, ok := args["session_id"].(string); ok && sid != "" {
				// v9.3 fix for chain-spawn bug (v9.0.6 terraria-05, v92
				// 03/04): pre-v9.3 used GetFocus() to pick the parent,
				// which silently linked siblings into a chain when the
				// caller didn't reset focus between spawns. Now we walk
				// up the focus chain to find THE ROOT and set parent
				// to that — meaning fan-out is the default, and the
				// agent has to opt into chaining by passing `parent`
				// explicitly.
				normalized, nerr := session.NormalizeID(sid)
				if nerr == nil {
					if !persistence.ExistsSession(persistence.SessionDefaultDir, normalized) {
						// Determine parent:
						//   1. explicit `parent` arg wins (allows manual chain)
						//   2. else walk focus chain up to root
						//   3. else empty (this spawn creates a new root)
						parent := ""
						if pArg, ok := args["parent"].(string); ok {
							parent = strings.TrimSpace(pArg)
						}
						if parent == "" {
							focused, _ := persistence.GetFocus(persistence.SessionDefaultDir)
							if focused != "" {
								if root, ferr := persistence.FindRoot(persistence.SessionDefaultDir, focused); ferr == nil && root != "" {
									parent = root
								}
							}
						}
						if expandsObject != "" {
							tg, gerr := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
							if gerr != nil {
								return "", fmt.Errorf("spawn_subagent: load top graph: %w", gerr)
							}
							if _, ok := tg.Objects[expandsObject]; !ok {
								return "", fmt.Errorf("spawn_subagent: expands_object %q is not an object in the top hypergraph (K/graph.json) â create it via graph_create_object first", expandsObject)
							}
						}
						if _, sterr := workflow.Start(persistence.SessionDefaultDir, normalized, parent, task, session.Input{}); sterr != nil {
							return "", fmt.Errorf("spawn_subagent: auto-create session %s failed: %w", normalized, sterr)
						}
						if expandsObject != "" {
							es, lerr := persistence.LoadSession(persistence.SessionDefaultDir, normalized)
							if lerr != nil {
								return "", fmt.Errorf("spawn_subagent: reload session for expansion: %w", lerr)
							}
							es.ExpandsObject = expandsObject
							if serr := persistence.SaveSession(persistence.SessionDefaultDir, es); serr != nil {
								return "", fmt.Errorf("spawn_subagent: record expands_object: %w", serr)
							}
							if serr := persistence.SaveExpansionGraph(normalized, graph.NewGraph()); serr != nil {
								return "", fmt.Errorf("spawn_subagent: create sub-hypergraph: %w", serr)
							}
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
