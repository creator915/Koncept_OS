package characterize

import "fmt"

// next.go implements compute_next_action at the MVP fidelity the design
// document EXPLICITLY specifies (设计文档 Part 7.4). This is NOT one of
// the [NOT YET DESIGNED] points — only the *full* internal algorithm
// (11.D: weighted-sum / decision-tree / RL) is deferred; Part 7.4 hands
// down a concrete 8-rung heuristic ladder as the designated MVP, and
// the document's whole thesis (原则 A / Part 7.1) is that the workflow
// is COMPUTED from state, not table-driven. Before this, the front
// stage was a manual one-shot command; this makes "which object needs
// characterizing" a computed decision.
//
// Scope discipline (设计文档 修订原则 + 重要提醒 #2): rungs the MVP
// slice cannot HONESTLY evaluate (3–6: evidence-kind diversity, EPA/
// SBFL suspiciousness, assumption-validation tracking, Reproducible
// activation budget) are NOT faked — they are reported as deferred via
// UnevaluatedRungs so the gap is auditable, exactly the way the product
// surfaces 未覆盖范围 to clients (Part 10.2). The autonomous loop that
// would persist AgentState across steps is intentionally NOT built: it
// depends on the Storage system, which is genuinely [NOT YET DESIGNED]
// (Part 8.3 / 11) and not reached by this slice — building it now would
// be 强行预设.

// ActionKind enumerates the Action variants from 设计文档 Part 7.2 that
// this slice can produce. (RunVerifier/InvokeTool/UpdateLedger/
// ForkBranch exist in the doc's enum but are not produced by the MVP
// ladder — they belong to rungs/stages this slice doesn't reach.)
type ActionKind string

const (
	// ActionEscalateToCustomer — rung 1: a blocking escalation is
	// pending; do NOT advance until the customer responds (Part 3.3).
	ActionEscalateToCustomer ActionKind = "EscalateToCustomer"
	// ActionCharacterize — rung 2: a high-stake property has NO Oracle;
	// generate one, preferring a char test (cheapest oracle). This is
	// the front stage, now computed rather than hand-invoked.
	ActionCharacterize ActionKind = "Characterize"
	// ActionContinueGoal — rung 7: the explicit task goal still has
	// unfinished steps.
	ActionContinueGoal ActionKind = "ContinueGoal"
	// ActionTerminate — rung 8: nothing outstanding; close the task.
	ActionTerminate ActionKind = "Terminate"
)

// Action is one decision. Reason + Rung make the decision auditable
// (设计文档 Part 10.4: the client can audit the agent's thinking path).
type Action struct {
	Kind     ActionKind
	ObjectID string // set for ActionCharacterize
	Rung     int    // which Part 7.4 rung fired
	Reason   string
}

// ObjectStatus is the minimal per-object state the MVP ladder reads.
// (设计文档 Part 2.8 AgentState is far larger; we model only what rungs
// 1/2/7/8 actually consult — honest about the subset.)
type ObjectStatus struct {
	ID            string
	HighStake     bool // Part 7.4 rung 2 targets HIGH-stake properties
	HasCharOracle bool // a Characterization Oracle already backs it
}

// AgentState is the MVP-evaluable slice of 设计文档 Part 2.8.
type AgentState struct {
	// BlockingEscalation: non-empty ⇒ a layer-escalation / oracle-
	// conflict awaiting the customer (rung 1). E.g. set from a
	// characterization that produced an "escalation-candidate"
	// nondeterminism assumption the customer must adjudicate.
	BlockingEscalation string
	Objects            []ObjectStatus
	GoalIncomplete     bool // rung 7
}

// ComputeNextAction is the Part 7.4 MVP heuristic ladder. Strictly
// ordered: an earlier rung that fires preempts every later one (the
// doc's "处理阻塞，不前进" discipline). Rungs 3–6 are deliberately
// skipped here and surfaced via UnevaluatedRungs instead of faked.
func ComputeNextAction(s AgentState) Action {
	// Rung 1 — blocking escalation: handle the blocker, do not advance.
	if s.BlockingEscalation != "" {
		return Action{
			Kind: ActionEscalateToCustomer, Rung: 1,
			Reason: "blocking escalation pending: " + s.BlockingEscalation,
		}
	}

	// Rung 2 — a high-stake property with NO Oracle: generate one,
	// preferring a char test (fastest). This is the computed entry into
	// the characterization front stage.
	for _, o := range s.Objects {
		if o.HighStake && !o.HasCharOracle {
			return Action{
				Kind: ActionCharacterize, ObjectID: o.ID, Rung: 2,
				Reason: fmt.Sprintf("high-stake object %q has no Characterization Oracle — generate one via char test", o.ID),
			}
		}
	}

	// Rungs 3–6 — evidence-diversity / Reproducible-activation /
	// assumption-validation / SBFL-EPA suspiciousness. NOT evaluable in
	// the MVP slice; see UnevaluatedRungs. We do not fabricate a
	// decision from dimensions we did not measure.

	// Rung 7 — explicit goal still has unfinished steps.
	if s.GoalIncomplete {
		return Action{Kind: ActionContinueGoal, Rung: 7, Reason: "explicit task goal has unfinished steps"}
	}

	// Rung 8 — nothing outstanding.
	return Action{Kind: ActionTerminate, Rung: 8, Reason: "no outstanding oracle gap / escalation / goal step"}
}

// UnevaluatedRungs reports the Part 7.4 rungs the MVP slice did NOT
// consult, with why. Surfacing this (rather than silently skipping) is
// the workflow-level analogue of the product's honest-confidence
// contract (设计文档 Part 10.2): the decision is honest about which of
// its own dimensions it could not assess.
func UnevaluatedRungs() map[int]string {
	return map[int]string{
		3: "Oracle evidence diversity (needs evidence-kind tracking) — [NOT YET DESIGNED] in this slice",
		4: "Reproducible-evidence activation budget — not built (Part 11.N)",
		5: "Active-assumption validation backlog — not tracked in MVP",
		6: "SBFL/EPA high-suspiciousness nodes — Part 4 engine not built (regression-triage stage, not front stage)",
	}
}
