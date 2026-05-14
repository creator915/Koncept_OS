// Package agentctx exposes the agent-loop context plumbing that tool
// Run functions need to read. Lives outside internal/app/agent so tool
// packages can import it without creating a cycle (agent depends on
// tools; tools cannot import agent).
//
// Currently it carries one piece of state — the agent's nesting depth —
// used by v9.6 dispatch-mode hardening: once the graph reaches a size
// threshold, the main conversation (depth 0) must delegate impl work
// to subagents (depth 1+) instead of running it inline. See
// internal/tools/fs/write.go and internal/app/services/typecalc_service.go
// for the call sites.
package agentctx

import (
	"context"
	"fmt"
)

// DispatchModeThreshold is the graph object count at which the main
// conversation (depth=0) enters "dispatch mode" — it must stop doing
// impl-side work directly and instead spawn subagents. Threshold is 5
// because the 2026-05-14 fx batch died exactly at this scale: the agent
// declared 8 objects, then tried to write 15 def files + link 20+ edges
// all in the main conversation, blowing past the LLM stream timeout
// during the link-edges turn. Five was chosen as the smallest count
// that's clearly "many enough to need orchestration" — single-function
// HumanEval tasks (1-2 objects) and small multi-function tasks (3-4)
// still get the convenience of inline main-conversation work.
const DispatchModeThreshold = 5

// CheckMainImplWork returns an error iff (depth=0 AND objectCount >=
// threshold). Callers pass the current graph object count — keeping
// this package free of persistence/graph imports so any tool can call
// it without dragging in a deep dependency graph. The action argument
// is woven into the error message so the agent knows which tool was
// blocked and why.
func CheckMainImplWork(ctx context.Context, objectCount int, action string) error {
	if Depth(ctx) > 0 {
		return nil // subagents may do impl-side work
	}
	if objectCount < DispatchModeThreshold {
		return nil // graph is still small, main conversation may work directly
	}
	return fmt.Errorf(
		"refusing %q from main conversation: graph has %d objects (>= dispatch-mode threshold %d). "+
			"With this many objects, the main conversation must orchestrate via spawn_subagent. "+
			"Each object's impl, def, and typecalc chain belong in a subagent session — keeping "+
			"main-conversation context bounded is what prevents the LLM stream from dying mid-turn "+
			"(fx batch 2026-05-14). Spawn a subagent and pass this action as its task.",
		action, objectCount, DispatchModeThreshold)
}

// depthKey is the unexported context key. Only this package can read or
// write it — callers go through WithDepth / Depth.
type depthKey struct{}

// WithDepth returns ctx annotated with the given agent depth. Called
// by the agent loop at the top of every RunTurnOpts call.
func WithDepth(ctx context.Context, d int) context.Context {
	return context.WithValue(ctx, depthKey{}, d)
}

// Depth returns the agent nesting depth stored on ctx, defaulting to 0
// (main conversation) when ctx has no annotation. 0 = main conversation;
// 1, 2, ... = subagents at increasing depth.
func Depth(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	if v, ok := ctx.Value(depthKey{}).(int); ok {
		return v
	}
	return 0
}
