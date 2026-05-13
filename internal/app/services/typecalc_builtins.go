// Package typecalctools hosts the typecalc_* agent tools — the
// agent-facing wrappers around typecalc compile / test / probe / feedback
// functionality. Imported as typecalctools to avoid collision with
// internal/typecalc.
package services

import (
	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
	"github.com/creator915/Koncept_OS/internal/app/workflow"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
)

// Tools returns the typecalc-area agent tools.
func TypecalcTools() map[string]toolcall.Tool {
	return map[string]toolcall.Tool{
		// v9.0 high-level entry point — replaces the 6-step manual
		// typecalc_compile / describe / synthesize / test / review /
		// graph_merge_object sequence with a single state-machine
		// invocation that handles enrich-retry between failures.
		"confirm_object": confirmObjectTool(),

		// Low-level tools — kept available for advanced workflows
		// (debugging, single-step re-runs) but the typical agent path
		// should be confirm_object.
		"typecalc_compile":          typecalcCompileTool(),
		"typecalc_test":             typecalcTestTool(),
		"typecalc_probe_plan":       typecalcProbePlanTool(),
		"typecalc_apply_feedback":   typecalcApplyFeedbackTool(),
		"typecalc_describe":         typecalcDescribeTool(),
		"typecalc_synthesize_tests": typecalcSynthesizeTestsTool(),
		"typecalc_review":           typecalcReviewTool(),
		// typecalc_waive + typecalc_obstacle removed in v9.2: the
		// obstacle/waiver pair was the universal escape hatch out of
		// the verification chain. The 2026-05-12 Terraria batch retro
		// proved this: 5/5 instances rode structural waivers into
		// "confirmed", 4/5 deliverables shipped broken. The gate is
		// now binary "pass / fail" with no transfer space — fix the
		// code, add a runner, or refactor into a verifiable form.
	}
}

// mutateGraph mirrors the helper in tools/graph/graph.go: load → mutate
// → save K/graph.json, capturing the diff to the focused session.
// Duplicated here (rather than imported) because typecalctools cannot
// import graphtools without creating a tangle — and the helper is small.
func mutateGraph(mutate func(*graph.Graph) error) error {
	g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if err != nil {
		return err
	}
	before := g.Clone()
	if err := mutate(g); err != nil {
		return err
	}
	if err := persistence.SaveGraph(persistence.GraphDefaultPath, g); err != nil {
		return err
	}
	_ = workflow.CaptureDiff(persistence.SessionDefaultDir, before, g)
	return nil
}
