package agent

import (
	"context"
	"fmt"

	"github.com/creator915/Koncept_OS/internal/llm/memory"
	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/router"
	"github.com/creator915/Koncept_OS/internal/tools"
)

// OuterDeps is the dependency surface every outer Handler reads. The
// outer Router builds one OuterDeps at startup and passes it down to
// every Handler closure. Handlers must NOT cache fields outside the
// per-call context — the deps are intentionally read-only.
type OuterDeps struct {
	// Client is the LLM transport used by creative Handlers
	// (H_architect, H_graph_declare, H_confirm_one's impl-write
	// step, H_checkpoint's codeProof-fill step). Deterministic
	// Handlers (H_aggregate, H_build, H_gate, H_finish) don't touch
	// it — they call tools.Execute directly.
	Client *transport.Client

	// ToolSet is the full builtin tool registry. Handlers narrow
	// this to a focused subset before invoking scoped sub-LLM loops.
	// Mutating ToolSet is a programmer error; copy the map first.
	ToolSet map[string]tools.Tool

	// RootSessionID identifies the kcpos session this outer run
	// drives. Threaded through every TypedValue payload so
	// post-condition checks resolve the right session.
	RootSessionID string

	// SpecPath is the path to the user's SPEC.md (or equivalent)
	// for the project being driven. Passed to H_architect /
	// H_graph_declare via the Task payload.
	SpecPath string

	// TaskDescription is the user's natural-language ask. Carried
	// in Task payload; Handlers may show it to the LLM as context.
	TaskDescription string

	// PerHandlerMaxIter caps the LLM sub-loop inside a single
	// creative Handler invocation. Defaults to 30; bumped via
	// RunRoutedOptions for hard projects.
	PerHandlerMaxIter int

	// MaxRouterSteps caps the total Router transitions across the
	// run (forward + feedback arcs). Defaults to 200.
	MaxRouterSteps int

	// OnTransition, if set, is fired AFTER every successful
	// Handler return — receives (fromType, toType, detail-payload).
	// Used by tests to record the observed sequence; production
	// can leave it nil.
	OnTransition func(from, to string, payload []byte)

	// Transcript, if set, persists every sub-LLM turn taken inside
	// any Handler. Each runScopedLLM call APPENDS its sub-loop
	// messages onto this single Transcript and the OnProgress hook
	// fires after every turn — so SIGKILL / 1800s timeout still
	// leaves a JSON record of everything the agent did up to the
	// last completed turn. Without this the routed runtime has no
	// observable transcript on disk (run.log stderr is human-only,
	// truncates large tool args, and can't be resumed).
	Transcript *memory.Transcript

	// confirmAttempts counts how many H_confirm_one_* invocations
	// have targeted each object. Used to (a) rotate target selection
	// to the least-attempted object, preventing one stuck object
	// (e.g. CANNOT_SYNTHESIZE orchestrator) from monopolising the
	// loop, and (b) inject an explicit split-or-skip guidance line
	// once the same object has been re-entered 3+ times without
	// reaching confirmed.
	confirmAttempts map[string]int
}

// recordConfirmAttempt bumps the per-object attempt counter and
// returns the new count. Caller is expected to use the count to
// switch the system prompt to a stronger "split or skip" guidance
// after 3+ attempts.
func (d *OuterDeps) recordConfirmAttempt(objectID string) int {
	if d.confirmAttempts == nil {
		d.confirmAttempts = map[string]int{}
	}
	d.confirmAttempts[objectID]++
	return d.confirmAttempts[objectID]
}

// pickConfirmTarget chooses the next un-confirmed object to drive
// through H_confirm_one. The picker minimises attempts — given two
// un-confirmed objects, the one with FEWER prior attempts wins.
// Ties break by ID (deterministic for tests).
//
// Without rotation, batch #3 saw pong-01 burn 30 turns × 30min on
// GameLoop while 5 other objects sat untouched at the start of the
// queue — every H_confirm_one_loop re-entry picked RemainingObjectIDs[0]
// = GameLoop and got stuck on the same CANNOT_SYNTHESIZE. Rotation
// makes the queue advance even when one object resists.
func (d *OuterDeps) pickConfirmTarget(remaining []string) string {
	if len(remaining) == 0 {
		return ""
	}
	best := remaining[0]
	bestCount := d.confirmAttempts[best]
	for _, id := range remaining[1:] {
		c := d.confirmAttempts[id]
		if c < bestCount || (c == bestCount && id < best) {
			best = id
			bestCount = c
		}
	}
	return best
}

// scopedToolSet returns a fresh map containing only the named tools
// from d.ToolSet. Used to constrain creative sub-LLM loops to the
// exact tools a Handler authorises.
func (d *OuterDeps) scopedToolSet(names ...string) map[string]tools.Tool {
	out := map[string]tools.Tool{}
	for _, n := range names {
		if t, ok := d.ToolSet[n]; ok {
			out[n] = t
		}
	}
	return out
}

// runScopedLLM invokes the legacy LLM tool-loop with a focused system
// message + constrained tool set + small iteration cap. Used by
// creative Handlers; deterministic Handlers do NOT call this.
//
// Each Handler is responsible for checking the on-disk post-condition
// AFTER runScopedLLM returns — the sub-loop may exit by hitting the
// iter cap with the goal unmet, by the LLM giving up, or by clean
// success. Only disk state is ground truth.
//
// **Depth = 1** is the canonical depth for a Handler-internal sub-LLM:
// it identifies the loop as a subagent (not the main conversation), so
// the v9.6 dispatch-mode write_file guard does NOT trigger and the
// LLM can write impl/def files directly. Without this the 2026-05-20
// pong batch saw every Handler 1.2 sub-loop blocked on a "use
// spawn_subagent" recommendation that the scoped tool set couldn't
// satisfy — burned 13 minutes per instance with zero confirms.
func (d *OuterDeps) runScopedLLM(ctx context.Context, systemMsg, userTask string, allowedTools []string) error {
	if d.Client == nil {
		return fmt.Errorf("outer deps missing LLM client")
	}
	max := d.PerHandlerMaxIter
	if max <= 0 {
		max = 60
	}

	// Per-Handler scoped system message; prepended each call so the
	// transcript clearly shows "Handler 1.2: confirm SetupCanvas"
	// boundaries when rendered.
	sysMsg := transport.Message{Role: "system", Content: systemMsg}

	// Append onto the shared Transcript (if provided). The whole
	// run's LLM activity ends up in one JSON, with each Handler's
	// sub-loop visible as a system-message → user-task → tool turns
	// segment.
	if d.Transcript != nil {
		d.Transcript.Messages = append(d.Transcript.Messages, sysMsg)
		runOpts := RunOptions{
			Tools:         d.scopedToolSet(allowedTools...),
			SkipSystem:    true,
			MaxIterations: max,
			Depth:         1,
			OnProgress:    func() { _ = d.Transcript.Save() },
		}
		err := RunTurnOpts(ctx, d.Client, &d.Transcript.Messages, userTask, runOpts)
		// Final save AFTER the sub-loop returns — captures the last
		// completed turn even if OnProgress was racing.
		_ = d.Transcript.Save()
		return err
	}

	// Fallback when no Transcript is wired (tests, dry-runs).
	msgs := []transport.Message{sysMsg}
	return RunTurnOpts(ctx, d.Client, &msgs, userTask, RunOptions{
		Tools:         d.scopedToolSet(allowedTools...),
		SkipSystem:    true,
		MaxIterations: max,
		Depth:         1,
	})
}

// emitObstacle is the canonical way for a Handler to bail out. It
// returns a TypedValue with type Outer.Obstacle and the listed reasons.
func emitObstacle(phase string, reasons ...string) router.TypedValue {
	type obstaclePayload struct {
		Phase   string   `json:"phase"`
		Reasons []string `json:"reasons"`
	}
	return router.MarshalPayload(router.OuterTypeObstacle, obstaclePayload{
		Phase:   phase,
		Reasons: reasons,
	})
}
