package graphtools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/creator915/Koncept_OS/internal/graph"
	"github.com/creator915/Koncept_OS/internal/llm"
	"github.com/creator915/Koncept_OS/internal/session"
	"github.com/creator915/Koncept_OS/internal/typecalc"
	"github.com/creator915/Koncept_OS/internal/typecalc/lang"
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

func graphShowTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
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

func graphCreateAttributeTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
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

func graphCreateObjectTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
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

func graphLinkRefineTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
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

func graphLinkConsumeTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
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

func graphLinkProduceTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "graph_link_produce",
				Description: "Record that an object produces an attribute (writes it as fresh output, replacing any prior value). Use `graph_link_mutate` instead for in-place mutation (e.g. JS object property assignment).",
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

// graphLinkMutateTool wires the §5.3 `mutates` edge: an object reads AND
// writes the attribute in place, without creating a new value. preflight
// cycle detection ignores mutates edges so a function can both consume
// and mutate the same attribute without tripping a self-cycle.
//
// Use this instead of graph_link_produce when modeling JavaScript-style
// object mutation, in-place data structure updates, or any function whose
// "output" is "the same object, modified". Use graph_link_produce when
// the function returns a fresh value.
func graphLinkMutateTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "graph_link_mutate",
				Description: "Record that an object MUTATES an attribute in place (reads + writes the same value, no fresh output). Use this for JS-style mutation. Distinct from `produces` (fresh output) — `mutates` does NOT count as a producer in cycle detection, so mutual mutation of shared state does not create false cycles.",
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
				return g.LinkMutate(obj, attr)
			}); err != nil {
				return "", err
			}
			return fmt.Sprintf("%s mutates %s", obj, attr), nil
		},
	}
}

func graphUnlinkMutateTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "graph_unlink_mutate",
				Description: "Remove a mutates edge from object's mutates list. Idempotent.",
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
				return g.UnlinkMutate(obj, attr)
			}); err != nil {
				return "", err
			}
			return fmt.Sprintf("%s no longer mutates %s", obj, attr), nil
		},
	}
}

func graphUnlinkRefineTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
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

func graphUnlinkConsumeTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
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

func graphUnlinkProduceTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
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

func graphMergeAttributeTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "graph_merge_attribute",
				Description: "Apply a partial JSON patch to an existing attribute. Allowed fields: intent, status, statusSession, valueSpace, confirmedOps, laws. Structural fields (def, refines) are not patchable here — use unlink/relink instead. The patch must be a JSON object string; only present keys are updated.\n\nOptional `session_id` temporarily swaps the focused session for the duration of the merge so the diff is recorded against that session — saves the focus / merge / re-focus call cycle when ticking through child sessions in batch.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":         map[string]interface{}{"type": "string", "description": "Attribute id."},
						"patch":      map[string]interface{}{"type": "string", "description": "JSON object as string, e.g. '{\"status\":\"confirmed\",\"valueSpace\":{\"celsius\":\"number\"}}'."},
						"session_id": map[string]interface{}{"type": "string", "description": "Optional. Session id to record this diff against — temporarily focused for the merge, then previous focus restored."},
					},
					"required": []string{"id", "patch"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			id, _ := args["id"].(string)
			patchStr, _ := args["patch"].(string)
			sessionID, _ := args["session_id"].(string)
			if id == "" || patchStr == "" {
				return "", fmt.Errorf("id and patch required")
			}
			var patch map[string]any
			if err := json.Unmarshal([]byte(patchStr), &patch); err != nil {
				return "", fmt.Errorf("patch must be valid JSON object: %w", err)
			}
			restore, err := withTempFocus(sessionID)
			if err != nil {
				return "", err
			}
			defer restore()
			if err := mutateGraph(func(g *graph.Graph) error {
				return g.MergeAttribute(id, patch)
			}); err != nil {
				return "", err
			}
			return fmt.Sprintf("merged %d field(s) into attribute %s", len(patch), id), nil
		},
	}
}

func graphMergeObjectTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "graph_merge_object",
				Description: "Apply a partial JSON patch to an existing object. Allowed fields: intent, impl, status, statusSession, temporal, preconditions, postconditions. Structural fields (def, consumes, produces) are not patchable — use unlink/relink. Patch must be a JSON object string.\n\nOptional `session_id` temporarily swaps the focused session for the duration of the merge so the diff is recorded against that session — saves the focus / merge / re-focus cycle when ticking many child sessions through implementing→confirmed in a batch.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":         map[string]interface{}{"type": "string", "description": "Object id."},
						"patch":      map[string]interface{}{"type": "string", "description": "JSON object as string."},
						"session_id": map[string]interface{}{"type": "string", "description": "Optional. Session id to record this diff against — temporarily focused for the merge, then previous focus restored."},
					},
					"required": []string{"id", "patch"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			id, _ := args["id"].(string)
			patchStr, _ := args["patch"].(string)
			sessionID, _ := args["session_id"].(string)
			if id == "" || patchStr == "" {
				return "", fmt.Errorf("id and patch required")
			}
			var patch map[string]any
			if err := json.Unmarshal([]byte(patchStr), &patch); err != nil {
				return "", fmt.Errorf("patch must be valid JSON object: %w", err)
			}
			restore, err := withTempFocus(sessionID)
			if err != nil {
				return "", err
			}
			defer restore()
			if err := mutateGraph(func(g *graph.Graph) error {
				return g.MergeObject(id, patch)
			}); err != nil {
				return "", err
			}
			result := fmt.Sprintf("merged %d field(s) into object %s", len(patch), id)
			// Fix 3 (auto-typecalc on impl-set): when the patch sets `impl`,
			// the file path now formally identifies this object's
			// implementation. Run typecalc_compile against the file
			// content immediately and record evidence — closes the §5.5
			// timing gap where write_file ran BEFORE impl was set, so
			// auto-typecalc-after-write found no graph match. With this
			// hook, the agent gets the same compile evidence either way:
			// write-then-merge (auto on write) or merge-then-write
			// (verifies later) or merge-after-write (verifies now).
			if extra := autoCompileOnImplSet(ctx, id, patch); extra != "" {
				result += "\n\n" + extra
			}
			return result, nil
		},
	}
}

// autoCompileOnImplSet runs typecalc_compile against the file referenced
// by a freshly-set `impl` field on a graph object, and writes evidence
// for that object on success. Returns a string to append to the merge
// result describing what happened (empty = no auto-compile triggered).
//
// Trigger: patch contains "impl" key with a non-empty string value.
// We re-load the graph after merge to get the canonical impl path
// (the patch may use a relative path that the merger normalized).
func autoCompileOnImplSet(ctx context.Context, objectID string, patch map[string]any) string {
	implRaw, ok := patch["impl"]
	if !ok {
		return ""
	}
	implPath, ok := implRaw.(string)
	if !ok || implPath == "" {
		return ""
	}
	content, err := os.ReadFile(implPath)
	if err != nil {
		// File not on disk yet — likely the agent will write_file next
		// (which will trigger auto-typecalc-on-write since impl is now
		// set). Don't fail the merge; just note.
		return fmt.Sprintf("[auto-typecalc] impl=%q referenced but file not yet on disk; "+
			"compile will run automatically on the next write_file to that path", implPath)
	}
	ext := filepath.Ext(implPath)
	langTag := typecalc.LangFromExt(ext)
	if langTag == typecalc.LangNone {
		return fmt.Sprintf("[auto-typecalc] could not infer language from extension %q (impl=%q); "+
			"call typecalc_compile object_id=%q lang=<L> manually", ext, implPath, objectID)
	}
	tv := typecalc.New(typecalc.KindCode, string(content)).
		WithState(typecalc.StateUncompiled).
		WithLang(langTag)
	env := &typecalc.RuleEnv{WorkDir: "."}
	out, err := lang.CompileLanguageInvoker(ctx, env, tv)
	if err != nil {
		return fmt.Sprintf("[auto-typecalc] invoker error on %s: %v", implPath, err)
	}
	if out.State == typecalc.StateCompiled {
		effectiveLang := typecalc.DetectEffectiveLang(string(content), langTag)
		if recErr := typecalc.RecordEvidence(objectID, "compile", string(effectiveLang), true); recErr != nil {
			return fmt.Sprintf("[auto-typecalc] compile passed but evidence write failed: %v", recErr)
		}
		return fmt.Sprintf("[auto-typecalc] %s compile passed; evidence recorded for %s (lang=%s)",
			implPath, objectID, effectiveLang)
	}
	if out.Kind == typecalc.KindCompileError {
		ce, _ := typecalc.DecodeCompileError(out)
		return fmt.Sprintf(
			"[auto-typecalc] COMPILE FAILED on %s for object %s\n  errorCode: %s\n  errorLog:\n%s\n\n"+
				"No evidence was recorded. Fix the source file and re-merge (or simply re-write the file — "+
				"auto-typecalc-on-write will retry).",
			implPath, objectID, ce.ErrorCode, indent(ce.ErrorLog, "    "))
	}
	return ""
}

// indent prefixes every line of s with prefix. Local copy because the
// helper otherwise lives in tools/fs/write.go (also a subpackage); too
// trivial to extract into a shared util package.
func indent(s, prefix string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// withTempFocus saves current focus, sets focus to id, and returns a
// closure that restores the prior focus. If id is empty (caller opted
// out of temp-focus), the closure is a no-op and current focus stays as
// the agent left it.
//
// We use a closure rather than `defer session.SetFocus(prev)` directly
// so the merge tools can both share the pattern without each one
// repeating the save/restore boilerplate.
func withTempFocus(id string) (func(), error) {
	if id == "" {
		return func() {}, nil
	}
	prev, _ := session.GetFocus(session.DefaultDir)
	if err := session.SetFocus(session.DefaultDir, id); err != nil {
		return func() {}, fmt.Errorf("temp-focus %s: %w", id, err)
	}
	return func() {
		_ = session.SetFocus(session.DefaultDir, prev)
	}, nil
}

func graphValidateTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
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

func graphRenderTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
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

func graphPreflightTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
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

func graphAutowireTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
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
