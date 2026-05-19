package agent

// loop_confirmgate.go — correction ③ (process-justice / 流程正义).
//
// A harnessed run (Caps != nil ⇒ a scored/task run, not casual
// interactive chat — the contract-aware scoping agreed in 岔路3) may
// NOT terminate "successfully" unless a deliverable graph object has
// reached status=confirmed. Given corrections ①②, confirmed can ONLY
// have been conferred by the confirm_object chain AFTER the mechanical
// behavioral-equivalence oracle passed (hand-set is hard-refused,
// MarkConfirmed is gated). So "a confirmed object exists" is, by
// construction, evidence-derived — not "the model said it's done".
//
// Bounded: the loop nudges the model up to maxConfirmGateNudges times;
// if it still finishes with no confirmed deliverable the run ends in an
// EXPLICIT error (GateFailed) — never silent success, never infinite.

import (
	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
)

// maxConfirmGateNudges bounds the refuse-and-reprompt loop so a model
// that will not run the verification chain ends in explicit failure
// rather than spinning forever (infinite = the fragile hang pattern we
// reject). 6 is generous: a compliant model confirms within 1–2 turns
// once nudged.
const maxConfirmGateNudges = 6

// hasConfirmedDeliverable reports whether the on-disk graph has at
// least one object at status=confirmed. Best-effort: a missing/empty
// graph ⇒ false (correctly blocks finish — nothing was verified).
func hasConfirmedDeliverable() bool {
	g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if err != nil || g == nil {
		return false
	}
	for _, obj := range g.Objects {
		if obj.Status == graph.StatusConfirmed {
			return true
		}
	}
	return false
}
