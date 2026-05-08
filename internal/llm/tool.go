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
	// Concurrent declares whether this tool is safe to run concurrently
	// with other Concurrent=true tools in the same agent turn. The
	// dispatcher batches consecutive Concurrent calls into goroutines.
	//
	// Set to true ONLY when the tool either:
	//   - is read-only (no on-disk mutation), OR
	//   - writes only to per-id evidence files where each invocation
	//     uses a distinct path (typecalc_describe / synthesize / review).
	//
	// Tools that mutate K/graph.json, K/sessions/, K/checkpoint.json,
	// or arbitrary files (write_file / edit / bash / graph_*) MUST keep
	// Concurrent=false — race conditions on those shared resources are
	// silent and corrupt state.
	Concurrent bool
}
