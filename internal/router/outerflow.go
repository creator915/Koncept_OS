package router

// Phase 2.C — outer (session-level) type flow.
//
// Pre-2026-05-20 kcpos had a split runtime: the OUTER agent loop was a
// classic LLM tool-loop (LLM picks any tool, runs it, repeats), while
// only the INNER confirm-chain (compile→describe→synthesize→test→review)
// was type-driven via Router (see typeflow.go for that frozen flow).
//
// 2026-05-20 outer typeflow lifts the same discipline up: at every
// step of a kcpos run, the current session state has a NOMINAL TYPE
// TAG, and the Router dispatches by tag. LLMs are still called inside
// individual Handlers for creative sub-tasks (writing impl, declaring
// graph objects, filling checkpoint codeProofs) but never at the
// orchestration level — the LLM does NOT decide "what runs next."
//
// Naming aligns with project_handler_x3_canonical (2026-05-19):
// Handler 1.1 / 1.2 / 1.3 are the canonical Handler family. The
// outer-typeflow handlers are thin orchestration wrappers around the
// existing Handler 1.1/1.2/1.3 services — they do NOT reimplement the
// underlying verification logic.
//
// Every Out type listed here MUST have a registered handler in
// app/agent/outer_handlers.go (or be in OuterTerminals). The
// fidelity test in app/agent/outer_loop_test.go verifies that.

// Outer type tags. Tags are unqualified strings (no nested generics)
// to keep error messages and gate diagnostics readable. Object/reason
// detail rides in the TypedValue Content payload.
const (
	// Initial entry. Content: { taskDescription, specPath, rootSessionID }.
	OuterTypeTask = "Outer.Task"

	// session_set_architecture done. Content: { rootSessionID }.
	OuterTypeArchitecture = "Outer.Architecture"

	// Every must-have graph object declared (status=declared).
	// Content: { rootSessionID, declaredObjectIDs }.
	OuterTypeGraphDeclared = "Outer.GraphDeclared"

	// At least one but not all objects reach status=confirmed. Loop
	// state — H_confirm_one re-enters on this until all confirmed.
	// Content: { rootSessionID, remainingObjectIDs }.
	OuterTypeSomeConfirmed = "Outer.SomeConfirmed"

	// Every object in graph at status=confirmed.
	// Content: { rootSessionID }.
	OuterTypeAllConfirmed = "Outer.AllConfirmed"

	// session_aggregate root done.
	OuterTypeAggregated = "Outer.Aggregated"

	// session_build done (HTML single-file project). Multi-file
	// projects skip H_build via direct Aggregated → Checkpointed
	// branch in H_aggregate's output.
	OuterTypeBuilt = "Outer.Built"

	// checkpoint_freeze + checkpoint_fill all must-items done.
	OuterTypeCheckpointed = "Outer.Checkpointed"

	// session_gate_check root returned PASS.
	OuterTypeGatePassed = "Outer.GatePassed"

	// FEEDBACK arms: gate FAILed, classified by reason category.
	// H_gate emits one of these instead of plain Obstacle when the
	// failure is recoverable; a repair handler picks the value up
	// and routes back to an earlier state, producing a closed
	// feedback loop.
	//
	// Content: { rootSessionID, reasons[], objectID? }.
	OuterTypeGateFailObject     = "Outer.GateFailObject"     // an object's verification chain is incomplete
	OuterTypeGateFailCheckpoint = "Outer.GateFailCheckpoint" // codeProof / finalVerdict missing
	OuterTypeGateFailBuild      = "Outer.GateFailBuild"      // deliverable file missing / empty
	OuterTypeGateFailAttrs      = "Outer.GateFailAttrs"      // attribute backfill: produced/mutated attrs still status=declared

	// TERMINALS

	// session_status root finished. Run is complete; deliverable
	// asserted on disk and gate PASS.
	OuterTypeFinished = "Outer.Finished"

	// Unrecoverable failure — Handler bubbled up after exhausting
	// internal retries, or the failure class isn't a feedback-eligible
	// state. Content: { phase, reasons[] }.
	OuterTypeObstacle = "Outer.Obstacle"
)

// OuterFlowRules is the canonical outer-loop type flow. Every edge
// is grounded in a real Handler registered in
// app/agent/outer_handlers.go. The outer_loop_test.go fidelity test
// verifies that this table matches the runtime Router exactly.
//
// Feedback arcs ("GateFailObject → SomeConfirmed" etc.) are first-class
// edges — the Router treats them like any other transition.
var OuterFlowRules = []FlowRule{
	{OuterTypeTask, []string{OuterTypeArchitecture, OuterTypeObstacle}},
	{OuterTypeArchitecture, []string{OuterTypeGraphDeclared, OuterTypeObstacle}},
	{OuterTypeGraphDeclared, []string{OuterTypeSomeConfirmed, OuterTypeAllConfirmed, OuterTypeObstacle}},
	{OuterTypeSomeConfirmed, []string{OuterTypeSomeConfirmed, OuterTypeAllConfirmed, OuterTypeObstacle}},
	{OuterTypeAllConfirmed, []string{OuterTypeAggregated, OuterTypeObstacle}},
	{OuterTypeAggregated, []string{OuterTypeBuilt, OuterTypeCheckpointed, OuterTypeObstacle}},
	{OuterTypeBuilt, []string{OuterTypeCheckpointed, OuterTypeGateFailBuild, OuterTypeObstacle}},
	{OuterTypeCheckpointed, []string{OuterTypeGatePassed, OuterTypeGateFailObject, OuterTypeGateFailCheckpoint, OuterTypeGateFailBuild, OuterTypeGateFailAttrs, OuterTypeObstacle}},
	{OuterTypeGatePassed, []string{OuterTypeFinished, OuterTypeObstacle}},

	// Feedback arms — each repair Handler routes back to an earlier
	// state, NOT to the next forward state. Producing the same state
	// the failure was diagnosed against is intentional: the loop
	// re-runs that state's normal handler.
	{OuterTypeGateFailObject, []string{OuterTypeSomeConfirmed, OuterTypeObstacle}},
	{OuterTypeGateFailCheckpoint, []string{OuterTypeCheckpointed, OuterTypeObstacle}},
	{OuterTypeGateFailBuild, []string{OuterTypeBuilt, OuterTypeObstacle}},
	// GateFailAttrs → Checkpointed: after the LLM backfills the
	// produced/mutated attributes via graph_merge_attribute, we
	// re-enter the Checkpointed state which re-runs the gate.
	{OuterTypeGateFailAttrs, []string{OuterTypeCheckpointed, OuterTypeObstacle}},
}

// OuterTerminals are the only outer-type tags with no successor — the
// Router halts when the current value matches.
var OuterTerminals = []string{
	OuterTypeFinished,
	OuterTypeObstacle,
}

// IsOuterTerminal reports whether tag is one of OuterTerminals.
func IsOuterTerminal(tag string) bool {
	for _, t := range OuterTerminals {
		if t == tag {
			return true
		}
	}
	return false
}

// OuterFlowSuccessors returns the legal next types for from (nil if
// from is terminal or unknown — both have no outgoing edge).
func OuterFlowSuccessors(from string) []string {
	for _, r := range OuterFlowRules {
		if r.From == from {
			return r.To
		}
	}
	return nil
}

// OuterFlowAllows reports whether from→to is a declared legal
// transition. Mirrors FlowAllows for the inner chain.
func OuterFlowAllows(from, to string) bool {
	if IsOuterTerminal(from) {
		return false
	}
	for _, s := range OuterFlowSuccessors(from) {
		if s == to {
			return true
		}
	}
	return false
}
