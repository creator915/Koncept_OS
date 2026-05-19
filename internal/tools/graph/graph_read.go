package graphtools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
	"github.com/creator915/Koncept_OS/internal/llm/transport"
)

// graph-read family (KonceptOS_implementation_plan.md §1.1). All ops are
// READ-ONLY: they LoadGraphOrInit / LoadExpansionGraphOrInit and never
// Save. The plan lists six ops; `show` and `validate` (current-layer)
// are already provided by graph_show / graph_validate, so this file adds
// the four layered ones. Graph-walk helpers live in graph_read_walk.go.

func readSpec(name, desc, argName, argDesc string, required bool) transport.ToolSpec {
	props := map[string]interface{}{argName: map[string]interface{}{"type": "string", "description": argDesc}}
	spec := transport.ToolSpec{Type: "function", Function: transport.ToolFunction{
		Name: name, Description: desc,
		Parameters: map[string]interface{}{"type": "object", "properties": props},
	}}
	if required {
		spec.Function.Parameters["required"] = []string{argName}
	}
	return spec
}

func graphShowExpandedTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: true,
		Spec:       readSpec("graph_show_expanded", "Show a node AND recursively expand its sub-hypergraph (follows the expansion hyperlink into K/expansions/<sid>/graph.json). Read-only.", "id", "Object/attribute id to show expanded.", true),
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			id, _ := args["id"].(string)
			if id == "" {
				return "", fmt.Errorf("id required")
			}
			g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
			if err != nil {
				return "", err
			}
			return renderExpanded(g, id, map[string]bool{}, 0), nil
		},
	}
}

func graphValidateDeepTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: true,
		Spec:       readSpec("graph_validate_deep", "Validate the current hypergraph AND recursively validate every expanded sub-hypergraph (produce/consume balance, refinement DAG, etc.). Read-only.", "_", "unused", false),
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
			if err != nil {
				return "", err
			}
			cwd, _ := os.Getwd()
			return validateDeep(g, "top", map[string]bool{}, cwd), nil
		},
	}
}

func graphDepOrderTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: true,
		Spec:       readSpec("graph_dep_order", "Topological order (parallel-safe waves) of the CURRENT layer's objects via consume/produce dependencies. Optional comma-separated object_ids; defaults to all. Read-only.", "object_ids", "Optional comma-separated object ids; empty = all.", false),
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
			if err != nil {
				return "", err
			}
			var ids []string
			if raw, _ := args["object_ids"].(string); strings.TrimSpace(raw) != "" {
				for _, p := range strings.Split(raw, ",") {
					if p = strings.TrimSpace(p); p != "" {
						ids = append(ids, p)
					}
				}
			} else {
				ids = sortedObjectIDs(g)
			}
			return g.Preflight(ids).String(), nil
		},
	}
}

func graphQueryDownstreamTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: true,
		Spec:       readSpec("graph_query_downstream", "List objects transitively affected by an attribute (consume/mutate, then through produced attrs), descending into expanded sub-hypergraphs (cross-layer, best-effort by name). Read-only.", "attribute", "Attribute id whose downstream impact to compute.", true),
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			attr, _ := args["attribute"].(string)
			if attr == "" {
				return "", fmt.Errorf("attribute required")
			}
			g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
			if err != nil {
				return "", err
			}
			return queryDownstreamRec("top", g, attr, map[string]bool{}), nil
		},
	}
}
