package router

// Phase 2.3 — the observed type-flow, frozen into match rules.
//
// P2.2 observed (tests/.batch-logs/p22-01.log, a real DeepSeek run) that
// kcpos's verification chain is NOT free composition: typed values move
// along a FIXED partial order. This file freezes that order as an
// explicit, testable rule table. Every edge here is grounded in a real
// `In:`/`Out:` registration in internal/router/chains/chain.go (verified
// by grep at authoring time) — NOT invented. String literals mirror the
// chains.Type* constants (router cannot import chains: chains imports
// router; the values are stable).
//
// The Router itself already enforces single-handler-per-In dispatch
// (router.go Register), so "no ambiguity" is structural; these rules let
// us assert match / no-match / determinism independently of a built
// chain, and a fidelity test cross-checks them against the real
// BuildChain so the freeze can never drift from reality.

// FlowRule is one source type and every type it may legally produce.
type FlowRule struct {
	From string
	To   []string
}

// ObservedTypeFlow is the frozen partial order (the canonical confirm
// chain spine + its error/obstacle sum-branches).
var ObservedTypeFlow = []FlowRule{
	{"StartConfirm", []string{"Compiled<Object>", "CompileError<Object>", "Obstacle<Object,Reason>"}},
	{"Request<Object,Compile>", []string{"Compiled<Object>", "CompileError<Object>", "Obstacle<Object,Reason>"}},
	{"Compiled<Object>", []string{"Described<Object>", "Obstacle<Object,Reason>"}},
	{"Described<Object>", []string{"Synthesized<Object>", "Tested<Object,Pass>", "TestError<Object>", "Obstacle<Object,Reason>"}},
	{"Synthesized<Object>", []string{"Tested<Object,Pass>", "TestError<Object>", "Obstacle<Object,Reason>"}},
	{"Request<Object,Test>", []string{"Compiled<Object>", "CompileError<Object>", "Tested<Object,Pass>", "TestError<Object>", "Obstacle<Object,Reason>"}},
	{"Tested<Object,Pass>", []string{"Reviewed<Object,Pass>", "ReviewFailed<Object>", "Obstacle<Object,Reason>"}},
	{"Request<Object,Review>", []string{"Compiled<Object>", "CompileError<Object>", "Reviewed<Object,Pass>", "ReviewFailed<Object>", "Obstacle<Object,Reason>"}},
	{"Reviewed<Object,Pass>", []string{"Confirmed<Object>", "Obstacle<Object,Reason>"}},
}

// flowTerminals are the only types with NO successor (the chain ends).
var flowTerminals = []string{"Confirmed<Object>", "Obstacle<Object,Reason>"}

// FlowSuccessors returns the legal next types for from (nil if from is
// terminal or unknown — both have no outgoing edge).
func FlowSuccessors(from string) []string {
	for _, r := range ObservedTypeFlow {
		if r.From == from {
			return r.To
		}
	}
	return nil
}

// FlowAllows reports whether from→to is a frozen, legal transition.
func FlowAllows(from, to string) bool {
	for _, s := range FlowSuccessors(from) {
		if s == to {
			return true
		}
	}
	return false
}

// FlowIsTerminal reports whether t is a chain end-state.
func FlowIsTerminal(t string) bool {
	for _, x := range flowTerminals {
		if x == t {
			return true
		}
	}
	return false
}

// FlowSourcesUnique is the real "no ambiguity" invariant: every source
// type appears in AT MOST ONE rule. That mirrors the Router's structural
// single-handler-per-In dispatch (router.go Register), so a typed value
// can never be ambiguously routed. (Multi-element To is NOT ambiguity —
// it is a sum-branch the single handler itself selects, e.g.
// Described→Synthesized | Tested<Pass> on the HTML fast-path.)
func FlowSourcesUnique() bool {
	seen := map[string]bool{}
	for _, r := range ObservedTypeFlow {
		if seen[r.From] {
			return false
		}
		seen[r.From] = true
	}
	return true
}

// CanonicalSpine is the declared happy path (no error/obstacle branch).
// Each consecutive pair MUST be a FlowAllows edge — asserted by the
// tests, so the spine can never drift from ObservedTypeFlow.
var CanonicalSpine = []string{
	"StartConfirm",
	"Compiled<Object>",
	"Described<Object>",
	"Synthesized<Object>",
	"Tested<Object,Pass>",
	"Reviewed<Object,Pass>",
	"Confirmed<Object>",
}
