// Package typecalctools hosts the typecalc_* agent tools — the
// agent-facing wrappers around typecalc compile / test / probe / feedback
// functionality. Imported as typecalctools to avoid collision with
// internal/typecalc.
package typecalctools

import (
	"github.com/creator915/Koncept_OS/internal/graph"
	"github.com/creator915/Koncept_OS/internal/llm"
	"github.com/creator915/Koncept_OS/internal/session"
)

// Tools returns the typecalc-area agent tools.
func Tools() map[string]llm.Tool {
	return map[string]llm.Tool{
		"typecalc_compile":        typecalcCompileTool(),
		"typecalc_test":           typecalcTestTool(),
		"typecalc_probe_plan":     typecalcProbePlanTool(),
		"typecalc_apply_feedback": typecalcApplyFeedbackTool(),
	}
}

// mutateGraph mirrors the helper in tools/graph/graph.go: load → mutate
// → save K/graph.json, capturing the diff to the focused session.
// Duplicated here (rather than imported) because typecalctools cannot
// import graphtools without creating a tangle — and the helper is small.
func mutateGraph(mutate func(*graph.Graph) error) error {
	g, err := graph.LoadOrInit(graph.DefaultPath)
	if err != nil {
		return err
	}
	before := g.Clone()
	if err := mutate(g); err != nil {
		return err
	}
	if err := graph.Save(graph.DefaultPath, g); err != nil {
		return err
	}
	_ = session.CaptureDiff(session.DefaultDir, before, g)
	return nil
}
