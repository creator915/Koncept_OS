package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/creator915/Koncept_OS/internal/chat"
	"github.com/creator915/Koncept_OS/internal/graph"
	"github.com/creator915/Koncept_OS/internal/session"
)

// Graph tools share the load → mutate → save pattern. Each call reads
// K/graph.json fresh from disk, applies the change, writes back. No in-memory
// cache between calls — sequential agent turns see consistent on-disk state.

// mutateGraph runs mutate against a freshly-loaded graph, saves it, and (if
// a session is currently focused and active) appends the diff to that
// session's graphDiff. Tool wrappers use this so every mutating operation
// gets diff capture for free.
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
	// Capture errors are intentionally swallowed: the graph is already saved
	// successfully and we don't want to confuse the model with secondary
	// failures. CaptureDiff itself no-ops if no session is focused.
	_ = session.CaptureDiff(session.DefaultDir, before, g)
	return nil
}

func graphShowTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "graph_show",
				Description: "Show a node and its immediate neighbors in K/graph.json (the project hypergraph). Works for both attribute and object IDs.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id": map[string]interface{}{"type": "string", "description": "Attribute (snake_case) or object (PascalCase) id."},
					},
					"required": []string{"id"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			id, _ := args["id"].(string)
			if id == "" {
				return "", fmt.Errorf("id required")
			}
			g, err := graph.LoadOrInit(graph.DefaultPath)
			if err != nil {
				return "", err
			}
			return g.Show(id)
		},
	}
}

func graphCreateAttributeTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "graph_create_attribute",
				Description: "Add a new attribute (data type / hypergraph node) to K/graph.json. Errors if id already exists. Status starts as 'declared'.\n\nDefault `def` is `defs/<id>.ts` (TypeScript-first convention). For non-TS projects (Go / Python / single-file HTML), pass `def` explicitly to point at the actual file where this type's signature lives — otherwise the def-existence check will warn. After creation, you (or a child) are responsible for creating that file with the type definition.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":     map[string]interface{}{"type": "string", "description": "snake_case identifier, e.g. 'weather_data'."},
						"intent": map[string]interface{}{"type": "string", "description": "Design intent — what this attribute represents. Be descriptive; redundancy beats omission."},
						"def":    map[string]interface{}{"type": "string", "description": "Path to the type-definition file. Defaults to 'defs/<id>.ts' (TS-first spec). Override for non-TS projects."},
					},
					"required": []string{"id", "intent"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			id, _ := args["id"].(string)
			intent, _ := args["intent"].(string)
			def, _ := args["def"].(string)
			if id == "" {
				return "", fmt.Errorf("id required")
			}
			if intent == "" {
				return "", fmt.Errorf("intent required")
			}
			if def == "" {
				def = "defs/" + id + ".ts"
			}
			if err := mutateGraph(func(g *graph.Graph) error {
				return g.AddAttribute(id, graph.NewAttribute(def, intent))
			}); err != nil {
				return "", err
			}
			return fmt.Sprintf("created attribute %s (def=%s, status=declared)", id, def), nil
		},
	}
}

func graphCreateObjectTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "graph_create_object",
				Description: "Add a new object (function type / hyperedge) to K/graph.json. Errors if id already exists. Status starts as 'declared'.\n\nDefault `def` is `defs/<id>.ts` (TypeScript-first convention). For non-TS projects, pass `def` explicitly. After creation you must (a) create the def file with the function signature and (b) once implemented, call `graph_merge_object --patch '{\"impl\":\"<actual file>\",\"status\":\"confirmed\"}'` to mark the work done — otherwise the §root-deliver gate will block session finish.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":     map[string]interface{}{"type": "string", "description": "PascalCase identifier, e.g. 'NormalizeWeather'."},
						"intent": map[string]interface{}{"type": "string", "description": "Design intent — what this object computes."},
						"def":    map[string]interface{}{"type": "string", "description": "Path to the signature file. Defaults to 'defs/<id>.ts' (TS-first spec). Override for non-TS projects."},
					},
					"required": []string{"id", "intent"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			id, _ := args["id"].(string)
			intent, _ := args["intent"].(string)
			def, _ := args["def"].(string)
			if id == "" {
				return "", fmt.Errorf("id required")
			}
			if intent == "" {
				return "", fmt.Errorf("intent required")
			}
			if def == "" {
				def = "defs/" + id + ".ts"
			}
			if err := mutateGraph(func(g *graph.Graph) error {
				return g.AddObject(id, graph.NewObject(def, intent))
			}); err != nil {
				return "", err
			}
			return fmt.Sprintf("created object %s (def=%s, status=declared)", id, def), nil
		},
	}
}

func graphLinkRefineTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "graph_link_refine",
				Description: "Record that one attribute refines another in the partial order (child <: parent). Both attributes must already exist.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"child":  map[string]interface{}{"type": "string", "description": "The more specific attribute id."},
						"parent": map[string]interface{}{"type": "string", "description": "The more general attribute id."},
					},
					"required": []string{"child", "parent"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			child, _ := args["child"].(string)
			parent, _ := args["parent"].(string)
			if err := mutateGraph(func(g *graph.Graph) error {
				return g.LinkRefine(child, parent)
			}); err != nil {
				return "", err
			}
			return fmt.Sprintf("linked %s <: %s", child, parent), nil
		},
	}
}

func graphLinkConsumeTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "graph_link_consume",
				Description: "Record that an object consumes an attribute (reads it as input). Both must already exist.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object":    map[string]interface{}{"type": "string", "description": "Object id (PascalCase)."},
						"attribute": map[string]interface{}{"type": "string", "description": "Attribute id (snake_case)."},
					},
					"required": []string{"object", "attribute"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			obj, _ := args["object"].(string)
			attr, _ := args["attribute"].(string)
			if err := mutateGraph(func(g *graph.Graph) error {
				return g.LinkConsume(obj, attr)
			}); err != nil {
				return "", err
			}
			return fmt.Sprintf("%s consumes %s", obj, attr), nil
		},
	}
}

func graphLinkProduceTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "graph_link_produce",
				Description: "Record that an object produces an attribute (writes it as output). Both must already exist.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object":    map[string]interface{}{"type": "string", "description": "Object id (PascalCase)."},
						"attribute": map[string]interface{}{"type": "string", "description": "Attribute id (snake_case)."},
					},
					"required": []string{"object", "attribute"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			obj, _ := args["object"].(string)
			attr, _ := args["attribute"].(string)
			if err := mutateGraph(func(g *graph.Graph) error {
				return g.LinkProduce(obj, attr)
			}); err != nil {
				return "", err
			}
			return fmt.Sprintf("%s produces %s", obj, attr), nil
		},
	}
}

func graphUnlinkRefineTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "graph_unlink_refine",
				Description: "Remove a refines edge (child no longer refines parent). Idempotent.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"child":  map[string]interface{}{"type": "string"},
						"parent": map[string]interface{}{"type": "string"},
					},
					"required": []string{"child", "parent"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			child, _ := args["child"].(string)
			parent, _ := args["parent"].(string)
			if err := mutateGraph(func(g *graph.Graph) error {
				return g.UnlinkRefine(child, parent)
			}); err != nil {
				return "", err
			}
			return fmt.Sprintf("unlinked %s <: %s", child, parent), nil
		},
	}
}

func graphUnlinkConsumeTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "graph_unlink_consume",
				Description: "Remove a consume edge. Idempotent.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object":    map[string]interface{}{"type": "string"},
						"attribute": map[string]interface{}{"type": "string"},
					},
					"required": []string{"object", "attribute"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			obj, _ := args["object"].(string)
			attr, _ := args["attribute"].(string)
			if err := mutateGraph(func(g *graph.Graph) error {
				return g.UnlinkConsume(obj, attr)
			}); err != nil {
				return "", err
			}
			return fmt.Sprintf("%s no longer consumes %s", obj, attr), nil
		},
	}
}

func graphUnlinkProduceTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "graph_unlink_produce",
				Description: "Remove a produce edge. Idempotent.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object":    map[string]interface{}{"type": "string"},
						"attribute": map[string]interface{}{"type": "string"},
					},
					"required": []string{"object", "attribute"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			obj, _ := args["object"].(string)
			attr, _ := args["attribute"].(string)
			if err := mutateGraph(func(g *graph.Graph) error {
				return g.UnlinkProduce(obj, attr)
			}); err != nil {
				return "", err
			}
			return fmt.Sprintf("%s no longer produces %s", obj, attr), nil
		},
	}
}

func graphMergeAttributeTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "graph_merge_attribute",
				Description: "Apply a partial JSON patch to an existing attribute. Allowed fields: intent, status, statusSession, valueSpace, confirmedOps, laws. Structural fields (def, refines) are not patchable here — use unlink/relink instead. The patch must be a JSON object string; only present keys are updated.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":    map[string]interface{}{"type": "string", "description": "Attribute id."},
						"patch": map[string]interface{}{"type": "string", "description": "JSON object as string, e.g. '{\"status\":\"confirmed\",\"valueSpace\":{\"celsius\":\"number\"}}'."},
					},
					"required": []string{"id", "patch"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			id, _ := args["id"].(string)
			patchStr, _ := args["patch"].(string)
			if id == "" || patchStr == "" {
				return "", fmt.Errorf("id and patch required")
			}
			var patch map[string]any
			if err := json.Unmarshal([]byte(patchStr), &patch); err != nil {
				return "", fmt.Errorf("patch must be valid JSON object: %w", err)
			}
			if err := mutateGraph(func(g *graph.Graph) error {
				return g.MergeAttribute(id, patch)
			}); err != nil {
				return "", err
			}
			return fmt.Sprintf("merged %d field(s) into attribute %s", len(patch), id), nil
		},
	}
}

func graphMergeObjectTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "graph_merge_object",
				Description: "Apply a partial JSON patch to an existing object. Allowed fields: intent, impl, status, statusSession, temporal, preconditions, postconditions. Structural fields (def, consumes, produces) are not patchable — use unlink/relink. Patch must be a JSON object string.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":    map[string]interface{}{"type": "string", "description": "Object id."},
						"patch": map[string]interface{}{"type": "string", "description": "JSON object as string."},
					},
					"required": []string{"id", "patch"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			id, _ := args["id"].(string)
			patchStr, _ := args["patch"].(string)
			if id == "" || patchStr == "" {
				return "", fmt.Errorf("id and patch required")
			}
			var patch map[string]any
			if err := json.Unmarshal([]byte(patchStr), &patch); err != nil {
				return "", fmt.Errorf("patch must be valid JSON object: %w", err)
			}
			if err := mutateGraph(func(g *graph.Graph) error {
				return g.MergeObject(id, patch)
			}); err != nil {
				return "", err
			}
			return fmt.Sprintf("merged %d field(s) into object %s", len(patch), id), nil
		},
	}
}

func graphValidateTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "graph_validate",
				Description: "Run all KonceptOS structural checks against K/graph.json: produce/consume balance (with subtype substitution), refines DAG (no cycles), naming uniqueness, reference integrity, temporal causality, plus warnings for orphan attributes, missing impl files, and incomplete metadata. Returns a human-readable report ending in PASS or FAIL.",
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			g, err := graph.LoadOrInit(graph.DefaultPath)
			if err != nil {
				return "", err
			}
			cwd, _ := os.Getwd()
			report := g.Validate(cwd)
			return report.String(), nil
		},
	}
}

func graphRenderTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "graph_render",
				Description: "Export K/graph.json as a diagram. format='mermaid' (default, paste into a markdown file or GitHub) or format='dot' (pipe to graphviz: `dot -Tsvg`). Attributes are rectangles; objects are rounded; consume/produce edges are solid; refines edges are dashed. Status drives node color.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"format": map[string]interface{}{
							"type":        "string",
							"description": "mermaid (default) | dot",
						},
					},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			format, _ := args["format"].(string)
			if format == "" {
				format = "mermaid"
			}
			g, err := graph.LoadOrInit(graph.DefaultPath)
			if err != nil {
				return "", err
			}
			switch format {
			case "mermaid":
				return g.RenderMermaid(), nil
			case "dot":
				return g.RenderDot(), nil
			default:
				return "", fmt.Errorf("unknown format %q (mermaid|dot)", format)
			}
		},
	}
}

func graphPreflightTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "graph_preflight",
				Description: "Analyze a batch of object ids for parallel-execution safety. Builds a dependency graph from consumes/produces edges (with subtype substitution) and partitions into topologically sorted waves. Returns SAFE with the wave plan, or UNSAFE if a dependency cycle is detected (the cycle path is reported). Also flags potential value-dependencies — multiple objects in the same wave consuming the same attribute, which may need serial execution if their value-space assumptions diverge. Call this BEFORE dispatching parallel sub-sessions.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"objects": map[string]interface{}{
							"type":        "array",
							"items":       map[string]interface{}{"type": "string"},
							"description": "Object ids to analyze.",
						},
					},
					"required": []string{"objects"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			ids := stringSliceArg(args["objects"])
			if len(ids) == 0 {
				return "", fmt.Errorf("objects array required and must be non-empty")
			}
			g, err := graph.LoadOrInit(graph.DefaultPath)
			if err != nil {
				return "", err
			}
			r := g.Preflight(ids)
			return r.String(), nil
		},
	}
}

// stringSliceArg coerces a []any (from JSON-decoded args) to []string,
// silently dropping non-string elements.
func stringSliceArg(v any) []string {
	if v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func graphAutowireTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "graph_autowire",
				Description: "Query: given a producer object and a consumer object, list compatible data-flow pairs (producer's output X feeds consumer's input Y when X == Y or X refines Y in the partial order). Pure query — does not mutate the graph. Use this to discover whether two objects can already be connected via existing types before adding new attributes.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"producer": map[string]interface{}{"type": "string", "description": "Object id of the producer."},
						"consumer": map[string]interface{}{"type": "string", "description": "Object id of the consumer."},
					},
					"required": []string{"producer", "consumer"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			producer, _ := args["producer"].(string)
			consumer, _ := args["consumer"].(string)
			g, err := graph.LoadOrInit(graph.DefaultPath)
			if err != nil {
				return "", err
			}
			matches, err := g.Autowire(producer, consumer)
			if err != nil {
				return "", err
			}
			if len(matches) == 0 {
				return fmt.Sprintf("no compatible flow from %s to %s", producer, consumer), nil
			}
			var b strings.Builder
			fmt.Fprintf(&b, "%s → %s (%d match):\n", producer, consumer, len(matches))
			for _, m := range matches {
				if m.Kind == "direct" {
					fmt.Fprintf(&b, "  %s → %s (direct)\n", m.ProducerAttr, m.ConsumerAttr)
				} else {
					fmt.Fprintf(&b, "  %s <: %s (refines)\n", m.ProducerAttr, m.ConsumerAttr)
				}
			}
			return b.String(), nil
		},
	}
}
