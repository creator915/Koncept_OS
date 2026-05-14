package agent

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"github.com/creator915/Koncept_OS/internal/tools"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// MaxSubAgentDepth caps recursive sub-agent spawning. depth 0 = top-level
// user-facing agent; depth 1 = first child; etc. Past this depth the spawn
// tool is omitted from the child's tool set so it cannot recurse further.
//
// Depth 3 is enough for KonceptOS work-session decomposition (root → wave →
// individual object) while keeping the safety net.
const MaxSubAgentDepth = 3

// subAgentSystemPrompt is the system prompt given to every spawned child.
// Intentionally short and focused — the parent has already filtered the
// task, so the child shouldn't second-guess the scope. The child still has
// the same tool descriptions auto-attached via the tools spec.
const subAgentSystemPrompt = `You are a kcpos sub-agent: a focused worker spawned by a parent agent to handle one well-scoped task. You see only the task you were given — not the parent's conversation. Do the work using your tools, then return a short final message (a few lines max) summarizing what you did and any return values the parent needs. Stay on task; do not expand scope.

You share K/* on-disk state (graph.json, sessions/, checkpoint.json) with the parent. If a session is focused when you start, your graph mutations record to that session's graphDiff — that's intended.`

// SubAgentRunner is the agent-package implementation of
// tools.SubAgentRunner. It re-enters RunTurnOpts with a fresh message
// history, a depth-aware tool set, and indented status output so nested
// runs stay visually distinguishable.
type SubAgentRunner struct {
	client *transport.Client
	depth  int // 0 = top-level; this runner produces children at depth+1

	// parentCaps is the capability set of the runner's owning agent loop.
	// When the parent spawns a child with role/caps, the child's effective
	// CapSet is checked against parentCaps via SubsetCheck (§6.1) — a
	// child cannot widen its own privileges. Empty means the parent has
	// no scoping enabled, which short-circuits the subset check (see
	// resolveChildCaps below).
	parentCaps core.CapSet
}

// NewSubAgentRunner builds a runner for use at the given depth.
// Top-level callers (main.go) pass depth=0; the produced runner spawns
// children at depth=1, which themselves will spawn at depth=2, etc.
// At MaxSubAgentDepth, children won't be given a spawn_subagent tool.
func NewSubAgentRunner(client *transport.Client, depth int) *SubAgentRunner {
	return &SubAgentRunner{client: client, depth: depth}
}

// NewSubAgentRunnerWithCaps is the cap-aware constructor used when the
// owning agent loop is itself running with a CapSet — the runner remembers
// the parent set so it can enforce child ⊆ parent on every spawn.
func NewSubAgentRunnerWithCaps(client *transport.Client, depth int, parentCaps core.CapSet) *SubAgentRunner {
	return &SubAgentRunner{client: client, depth: depth, parentCaps: parentCaps}
}

// resolveChildCaps maps a SubAgentRequest's Role/Caps fields to a final
// CapSet for the child, performing the §6.1 subset check.
//
// Cases:
//   - both Role and Caps empty: child inherits the parent's CapSet
//     (which may itself be empty == unscoped).
//   - Role non-empty: resolve via core.PresetByName.
//   - Caps non-empty: convert tokens to a CapSet directly.
//
// In all cases except "no scoping at parent", the resolved child set must
// be ⊆ parentCaps; otherwise the spawn is refused with a structured error
// the model can react to.
func (r *SubAgentRunner) resolveChildCaps(req tools.SubAgentRequest) (core.CapSet, error) {
	var child core.CapSet
	switch {
	case req.Role != "":
		preset, ok := core.PresetByName(req.Role)
		if !ok {
			return nil, fmt.Errorf("unknown role %q (allowed: implementer / tester / integrator / root)", req.Role)
		}
		child = preset
	case len(req.Caps) > 0:
		child = make(core.CapSet, len(req.Caps))
		for i, t := range req.Caps {
			child[i] = core.Capability(t)
		}
	default:
		// Inherit parent's scoping (which may be empty).
		return r.parentCaps, nil
	}
	if len(r.parentCaps) > 0 {
		if err := child.SubsetCheck(r.parentCaps); err != nil {
			return nil, fmt.Errorf("child caps not ⊆ parent: %w", err)
		}
	}
	return child, nil
}

// Run implements tools.SubAgentRunner.
func (r *SubAgentRunner) Run(ctx context.Context, req tools.SubAgentRequest) (string, error) {
	childDepth := r.depth + 1
	if childDepth > MaxSubAgentDepth {
		return "", fmt.Errorf("subagent depth cap %d exceeded — refuse to recurse further", MaxSubAgentDepth)
	}

	// Save and restore focus around the child's run so we don't leak focus
	// state if the parent had something focused. Even if SessionID is empty,
	// the child may set focus internally; restoring on exit is defensive.
	prevFocus, _ := persistence.GetFocus(persistence.SessionDefaultDir)
	defer func() {
		_ = persistence.SetFocus(persistence.SessionDefaultDir, prevFocus)
	}()
	if req.SessionID != "" {
		if err := persistence.SetFocus(persistence.SessionDefaultDir, req.SessionID); err != nil {
			return "", fmt.Errorf("focus session %s: %w", req.SessionID, err)
		}
	}

	// Resolve the child's capability set per §6.1 (child ⊆ parent).
	childCaps, err := r.resolveChildCaps(req)
	if err != nil {
		return "", fmt.Errorf("spawn_subagent: %w", err)
	}

	// Build the child's tool set. If the child is at the depth cap, omit
	// spawn_subagent so it cannot recurse further. Otherwise inject a runner
	// configured for depth+1 that knows about childCaps so its own spawns
	// can enforce subset against them.
	var childTools map[string]tools.Tool
	if childDepth >= MaxSubAgentDepth {
		childTools = tools.Builtins()
	} else {
		childRunner := NewSubAgentRunnerWithCaps(r.client, childDepth, childCaps)
		childTools = tools.BuiltinsWithSubAgent(childRunner)
	}

	// Fresh message history. Start with the sub-agent's focused system
	// prompt — the parent's system prompt would only confuse it.
	messages := []transport.Message{
		{Role: "system", Content: subAgentSystemPrompt},
	}

	indent := strings.Repeat("  ", childDepth)
	focusStr := ""
	if req.SessionID != "" {
		focusStr = " · focus=" + req.SessionID
	}
	fmt.Fprintf(os.Stderr, "%s%s\x1b[2m┌─ subagent depth=%d%s ─\x1b[0m\n", Stamp(), indent, childDepth, focusStr)

	maxIters := req.MaxIterations
	if maxIters <= 0 {
		maxIters = defaultMaxIterations
	}

	err = RunTurnOpts(ctx, r.client, &messages, req.Task, RunOptions{
		Tools:         childTools,
		Indent:        indent,
		MaxIterations: maxIters,
		SkipSystem:    true, // we already added subAgentSystemPrompt above
		Caps:          childCaps,
		Depth:         childDepth,
	})

	fmt.Fprintf(os.Stderr, "%s%s\x1b[2m└─ subagent done ─\x1b[0m\n", Stamp(), indent)

	if err != nil {
		return "", fmt.Errorf("subagent failed: %w", err)
	}

	// The child's final response is the last assistant message with a
	// non-empty content (a turn that produced no tool_calls and a real
	// answer string).
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" && messages[i].Content != "" {
			return messages[i].Content, nil
		}
	}
	return "(subagent finished but produced no final message)", nil
}
