package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/creator915/Koncept_OS/internal/router"
	"github.com/creator915/Koncept_OS/internal/snapshot"
)

// milestoneNameFor maps an Outer.* state tag to a short milestone
// ref-name suffix. Returns "" for states that aren't worth
// snapshotting as rollback targets (mid-loop SomeConfirmed, terminal
// Obstacle / Finished).
//
// Naming kept deliberately stable — Phase 7 rollback paths rely on
// "graph-declared-<session>" / "all-confirmed-<session>" /
// "checkpointed-<session>" being predictable refs.
func milestoneNameFor(outerType string) string {
	switch outerType {
	case router.OuterTypeArchitecture:
		return "architecture"
	case router.OuterTypeGraphDeclared:
		return "graph-declared"
	case router.OuterTypeAllConfirmed:
		return "all-confirmed"
	case router.OuterTypeAggregated:
		return "aggregated"
	case router.OuterTypeBuilt:
		return "built"
	case router.OuterTypeCheckpointed:
		return "checkpointed"
	case router.OuterTypeGatePassed:
		return "gate-passed"
	}
	return ""
}

// RoutedRunResult is the terminal outcome of an outer-Router run.
type RoutedRunResult struct {
	TerminalType    string          // router.OuterTypeFinished or router.OuterTypeObstacle
	TerminalPayload json.RawMessage // the Content of the terminal TypedValue
	Steps           int             // forward + feedback transitions taken
	Trace           []RoutedStep    // every transition the Router took, in order
}

// TracePersistor, when non-nil, is invoked after EVERY successful
// Router transition with a snapshot of RoutedRunResult so far. The
// closure is expected to atomically write the snapshot to disk so
// SIGKILL during a later Handler still leaves the most-recent
// transition trace on disk. The 2026-05-20 batch #3 lost all five
// trace.json files because trace was written only at end-of-run —
// every instance hit 1800s SIGKILL before reaching that line.
type TracePersistor func(snapshot RoutedRunResult)

// RoutedStep is one observed Router transition.
type RoutedStep struct {
	From    string          `json:"from"`
	To      string          `json:"to"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// BuildOuterRouter constructs the outer Router with every Handler
// from outer_handlers.go registered. The router's Connectivity check
// (called inline) verifies the graph is closed: every Handler's Out
// list is either a registered Handler's In or in OuterTerminals.
//
// Returns an error if the registration graph is inconsistent — that's
// always a programmer bug, caught at startup, not at runtime.
func BuildOuterRouter(deps *OuterDeps) (*router.Router, error) {
	if deps == nil {
		return nil, fmt.Errorf("BuildOuterRouter: deps is nil")
	}
	r := router.NewRouter()
	if deps.MaxRouterSteps > 0 {
		r.SetMaxSteps(deps.MaxRouterSteps)
	}

	// Forward handlers.
	r.Register(H_architect(deps))
	r.Register(H_graph_declare(deps))
	r.Register(H_confirm_one_from_declared(deps))
	r.Register(H_confirm_one_loop(deps))
	r.Register(H_aggregate(deps))
	r.Register(H_build(deps))
	r.Register(H_checkpoint(deps))
	r.Register(H_gate(deps))
	r.Register(H_finish(deps))

	// Repair handlers (feedback arcs).
	r.Register(H_repair_object(deps))
	r.Register(H_repair_checkpoint(deps))
	r.Register(H_repair_build(deps))
	r.Register(H_repair_attrs(deps))

	// Terminals.
	r.RegisterTerminal(router.OuterTypeFinished)
	r.RegisterTerminal(router.OuterTypeObstacle)

	if orphans := r.Connectivity(); len(orphans) > 0 {
		return nil, fmt.Errorf("BuildOuterRouter: orphan output types (no handler, not terminal): %v", orphans)
	}
	return r, nil
}

// RunRoutedTurn drives the outer Router until it reaches a terminal
// type or hits the step cap. This REPLACES RunTurnOpts as the
// top-level kcpos runtime when the routed entry is used; the legacy
// LLM tool-loop remains available for backwards compatibility.
//
// Starting state is inferred from disk via router.InferOuterStateDetail
// (so an interrupted run can resume mid-pipeline). The initial
// Outer.Task payload is constructed from deps.TaskDescription /
// SpecPath / RootSessionID.
//
// `persist`, when non-nil, is called after every successful Router
// transition with the in-progress RoutedRunResult so callers can
// atomically write trace.json to disk on each step — SIGKILL safe.
func RunRoutedTurn(ctx context.Context, deps *OuterDeps) (RoutedRunResult, error) {
	return RunRoutedTurnWithPersist(ctx, deps, nil)
}

// RunRoutedTurnWithPersist is the variant that takes an explicit
// TracePersistor. RunRoutedTurn is the no-persist wrapper.
func RunRoutedTurnWithPersist(ctx context.Context, deps *OuterDeps, persist TracePersistor) (RoutedRunResult, error) {
	if deps == nil {
		return RoutedRunResult{}, fmt.Errorf("RunRoutedTurn: deps is nil")
	}
	r, err := BuildOuterRouter(deps)
	if err != nil {
		return RoutedRunResult{}, err
	}

	// Infer where to resume. If nothing's on disk yet we start at
	// Outer.Task. Otherwise pick up where the artifacts say we are.
	det, _ := router.InferOuterStateDetail(deps.RootSessionID)
	startType := det.Type
	if router.IsOuterTerminal(startType) {
		// Already finished or already obstacled — return immediately.
		return RoutedRunResult{
			TerminalType: startType,
			Steps:        0,
		}, nil
	}

	initialPayload := map[string]string{
		"rootSessionID":   deps.RootSessionID,
		"specPath":        deps.SpecPath,
		"taskDescription": deps.TaskDescription,
	}
	current := router.MarshalPayload(startType, initialPayload)

	result := RoutedRunResult{}
	maxSteps := deps.MaxRouterSteps
	if maxSteps <= 0 {
		maxSteps = 200
	}

	for step := 0; step < maxSteps; step++ {
		if ctx.Err() != nil {
			result.TerminalType = router.OuterTypeObstacle
			result.TerminalPayload = mustMarshal(map[string]interface{}{
				"phase":   "router",
				"reasons": []string{"context cancelled: " + ctx.Err().Error()},
			})
			return result, nil
		}
		if router.IsOuterTerminal(current.Type) {
			result.TerminalType = current.Type
			result.TerminalPayload = current.Content
			return result, nil
		}
		next, herr := r.Step(ctx, current)
		if herr != nil {
			// Infrastructural error (orphan output, handler returned
			// non-declared type, etc). Bubble up as Obstacle.
			result.TerminalType = router.OuterTypeObstacle
			result.TerminalPayload = mustMarshal(map[string]interface{}{
				"phase":   "router-step",
				"reasons": []string{herr.Error()},
				"fromType": current.Type,
			})
			return result, nil
		}
		result.Trace = append(result.Trace, RoutedStep{
			From:    current.Type,
			To:      next.Type,
			Payload: next.Content,
		})
		if deps.OnTransition != nil {
			deps.OnTransition(current.Type, next.Type, next.Content)
		}
		// Phase 2d (originally hooked in router.Run, but
		// RunRoutedTurnWithPersist uses r.Step in this custom loop
		// so the router-package hook never fires here) — emit
		// outer.transition for the snapshot event log. Without this,
		// lesson synthesis (Phase 6) can't find the terminal
		// Obstacle reason and pickRollbackMilestone has no causal
		// trace to walk. The Phase 8 e2e test caught this gap.
		if s := snapshot.FromContext(ctx); s != nil {
			s.Capture("outer.transition", snapshot.EventTypeOuterTransition, snapshot.OuterTransitionEvent{
				From:    current.Type,
				To:      next.Type,
				Payload: next.Content,
			})
		}
		// Phase 7 — auto-milestone on key forward-progress Outer.*
		// transitions when snapshotting is active. These milestones
		// are the rollback targets if a later attempt obstacles —
		// we want to rewind to the LATEST point that represents
		// "real agent work invested": architecture, graph declared,
		// each object confirmed, fully aggregated, checkpointed.
		//
		// Naming convention: "<phase>-<rootSessionID>" so multi-
		// instance test runs don't collide milestone refs.
		//
		// Failure mode: SetMilestone writes one event + one ref. If
		// either fails (disk full, permission), we log to stderr
		// rather than silently swallow — the next retry would
		// otherwise miss this milestone as a rollback target with
		// no diagnostic for why.
		if s := snapshot.FromContext(ctx); s != nil {
			if mname := milestoneNameFor(next.Type); mname != "" {
				if _, err := s.SetMilestone(mname+"-"+deps.RootSessionID, "auto: reached "+next.Type); err != nil {
					fmt.Fprintf(os.Stderr, "[snapshot/milestone] set milestone %s for %s failed: %v\n", mname, deps.RootSessionID, err)
				}
			}
		}
		result.Steps++
		current = next
		// In-progress snapshot of TerminalType so a SIGKILL after this
		// transition still produces a meaningful trace.json (the
		// last-known TerminalType reflects where the Router was, even
		// though execution didn't reach the terminal cleanly).
		if router.IsOuterTerminal(current.Type) {
			result.TerminalType = current.Type
			result.TerminalPayload = current.Content
		}
		if persist != nil {
			persist(result)
		}
	}

	// Step cap exhausted.
	result.TerminalType = router.OuterTypeObstacle
	result.TerminalPayload = mustMarshal(map[string]interface{}{
		"phase":   "router",
		"reasons": []string{fmt.Sprintf("step cap reached (%d) without terminal", maxSteps)},
	})
	return result, nil
}

func mustMarshal(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
