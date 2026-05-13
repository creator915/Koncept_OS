// Package graphtools hosts the graph_* agent tools — the canonical
// write entry into K/graph.json. Each mutating call records a graphDiff
// to the focused session for rollback. Imported as graphtools to avoid
// collision with internal/graph (which it wraps).
package graphtools

import "github.com/creator915/Koncept_OS/internal/llm/toolcall"

// Tools returns the graph-area agent tools.
func Tools() map[string]toolcall.Tool {
	return map[string]toolcall.Tool{
		"graph_show":             graphShowTool(),
		"graph_create_attribute": graphCreateAttributeTool(),
		"graph_create_object":    graphCreateObjectTool(),
		"graph_link_refine":      graphLinkRefineTool(),
		"graph_link_consume":     graphLinkConsumeTool(),
		"graph_link_produce":     graphLinkProduceTool(),
		"graph_link_mutate":      graphLinkMutateTool(),
		"graph_unlink_refine":    graphUnlinkRefineTool(),
		"graph_unlink_consume":   graphUnlinkConsumeTool(),
		"graph_unlink_produce":   graphUnlinkProduceTool(),
		"graph_unlink_mutate":    graphUnlinkMutateTool(),
		"graph_merge_attribute":  graphMergeAttributeTool(),
		"graph_merge_object":     graphMergeObjectTool(),
		"graph_autowire":         graphAutowireTool(),
		"graph_validate":         graphValidateTool(),
		"graph_preflight":        graphPreflightTool(),
		"graph_render":           graphRenderTool(),
	}
}
