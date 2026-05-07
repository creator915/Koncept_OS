package llm

import "context"

// Tool pairs a ToolSpec (the schema the LLM provider sees) with a Run
// closure (the in-process implementation). Lives in package llm rather
// than internal/tools because subpackages of tools/ (fs, graph, session,
// checkpoint, typecalc, git) all need to define and return Tools, and
// having Tool in tools/ would force them to depend on their parent —
// which then depends back on each subpackage to register them. Putting
// Tool one layer down (alongside ToolSpec, where it conceptually
// belongs) breaks that cycle.
type Tool struct {
	Spec ToolSpec
	Run  func(ctx context.Context, args map[string]interface{}) (string, error)
}
