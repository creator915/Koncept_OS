package graphtools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/domain/protocol"
	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
	"github.com/creator915/Koncept_OS/internal/app/workflow"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
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
	// Capture errors are intentionally swallowed: the graph is already saved
	// successfully and we don't want to confuse the model with secondary
	// failures. CaptureDiff itself no-ops if no session is focused.
	_ = workflow.CaptureDiff(persistence.SessionDefaultDir, before, g)
	return nil
}

func graphShowTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: true,
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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
			g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
			if err != nil {
				return "", err
			}
			return g.Show(id)
		},
	}
}

func graphCreateAttributeTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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

func graphCreateObjectTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "graph_create_object",
				Description: "Add a new object (function type / hyperedge) to K/graph.json. Errors if id already exists. Status starts as 'declared'.\n\nDefault `def` is `defs/<id>.ts` (TypeScript-first convention). For non-TS projects, pass `def` explicitly.\n\nv9.5: storyPoints (Fibonacci scale 1/2/3/5/8/13) and storyRationale (≥10 chars) are required at creation time — they anchor decomposition discipline BEFORE writing begins. storyPoints ≥ 8 blocks status=implementing until graph_split_object decomposes the object. Scale: 1=arithmetic, 2=single loop, 3=multi-branch, 5=multi-step workflow, 8=must-split, 13=unrepresentable.\n\nAfter creation: (a) create the def file with the function signature and (b) once implemented, call `graph_merge_object --patch '{\"impl\":\"<actual file>\",\"status\":\"confirmed\"}'` to mark the work done — otherwise the §root-deliver gate will block session finish.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":             map[string]interface{}{"type": "string", "description": "PascalCase identifier, e.g. 'NormalizeWeather'."},
						"intent":         map[string]interface{}{"type": "string", "description": "Design intent — what this object computes."},
						"def":            map[string]interface{}{"type": "string", "description": "Path to the signature file. Defaults to 'defs/<id>.ts' (TS-first spec). Override for non-TS projects."},
						"storyPoints":    map[string]interface{}{"type": "integer", "description": "Required (v9.5). Fibonacci scale: 1/2/3/5/8/13. Pick the smallest value the object plausibly fits — overestimates force unnecessary splits; underestimates hide growing complexity until the chain blows up at confirm."},
						"storyRationale": map[string]interface{}{"type": "string", "description": "Required (v9.5, ≥10 chars). One sentence justifying the storyPoints choice, e.g. 'single loop with one boundary check' for a 2-point object."},
					},
					"required": []string{"id", "intent", "storyPoints", "storyRationale"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			id, _ := args["id"].(string)
			intent, _ := args["intent"].(string)
			def, _ := args["def"].(string)
			storyRationale, _ := args["storyRationale"].(string)
			// JSON numbers arrive as float64 from encoding/json; tolerate
			// the LLM serializing storyPoints as either number or string.
			var storyPoints int
			spPresent := false
			switch v := args["storyPoints"].(type) {
			case float64:
				storyPoints = int(v)
				spPresent = true
			case int:
				storyPoints = v
				spPresent = true
			case string:
				// Some providers stringify integers in tool arguments.
				if v != "" {
					if _, err := fmt.Sscanf(v, "%d", &storyPoints); err == nil {
						spPresent = true
					}
				}
			}
			if id == "" {
				return "", fmt.Errorf("id required")
			}
			if intent == "" {
				return "", fmt.Errorf("intent required")
			}
			if !spPresent {
				return "", fmt.Errorf("storyPoints required (v9.5) — Fibonacci scale 1/2/3/5/8/13. Pick the smallest value the object plausibly fits")
			}
			if !protocol.ValidStoryPoints(storyPoints) {
				return "", fmt.Errorf("storyPoints must be one of 1, 2, 3, 5, 8, 13 (Fibonacci scale); got %d", storyPoints)
			}
			if len(storyRationale) < 10 {
				return "", fmt.Errorf("storyRationale required (v9.5, >=10 chars) — one sentence justifying the chosen storyPoints; got %d chars", len(storyRationale))
			}
			if def == "" {
				def = "defs/" + id + ".ts"
			}
			if err := mutateGraph(func(g *graph.Graph) error {
				if err := g.AddObject(id, graph.NewObject(def, intent)); err != nil {
					return err
				}
				// AddObject writes a baseline Object via NewObject — backfill
				// storyPoints/storyRationale post-add to avoid widening
				// NewObject's signature (8+ callers, mostly tests).
				obj := g.Objects[id]
				obj.StoryPoints = storyPoints
				obj.StoryRationale = storyRationale
				g.Objects[id] = obj
				return nil
			}); err != nil {
				return "", err
			}
			return fmt.Sprintf("created object %s (def=%s, status=declared, storyPoints=%d)", id, def, storyPoints), nil
		},
	}
}

func graphLinkRefineTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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

func graphLinkConsumeTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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

func graphLinkProduceTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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
func graphLinkMutateTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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

func graphUnlinkMutateTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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

func graphUnlinkRefineTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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

func graphUnlinkConsumeTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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

func graphUnlinkProduceTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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

func graphMergeAttributeTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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
			sessionID, _ := args["session_id"].(string)
			if id == "" {
				return "", fmt.Errorf("id required")
			}
			// Accept patch as string or object — see graph_merge_object
			// Run for the rationale (LLMs often send nested object form
			// despite the schema declaring string).
			patch, err := decodePatchArg(args["patch"])
			if err != nil {
				return "", err
			}
			restore, err := withTempFocus(sessionID)
			if err != nil {
				return "", err
			}
			defer restore()
			injectStatusSessionFromFocus(patch)
			if err := mutateGraph(func(g *graph.Graph) error {
				return g.MergeAttribute(id, patch)
			}); err != nil {
				return "", err
			}
			return fmt.Sprintf("merged %d field(s) into attribute %s", len(patch), id), nil
		},
	}
}

// injectStatusSessionFromFocus mutates patch in place: when the agent
// merges `status` but not `statusSession`, fill the latter from the
// currently-focused session id (set by the optional session_id arg via
// withTempFocus, else the persistent project focus). Without this,
// agents that drive status=confirmed via direct graph_merge_attribute /
// graph_merge_object — the common path; the confirm_object service's
// MarkConfirmed only covers chained confirmation — leave statusSession
// null, erasing the audit trail of "which session confirmed this entity".
//
// Honor explicit values in patch, including explicit null: if the agent
// passed statusSession, do not overwrite. If there is no current focus
// (atypical — usually means the project has no active session at all),
// leave statusSession unset to avoid lying about provenance.
func injectStatusSessionFromFocus(patch map[string]any) {
	if _, hasStatus := patch["status"]; !hasStatus {
		return
	}
	if _, hasSess := patch["statusSession"]; hasSess {
		return
	}
	focus, _ := persistence.GetFocus(persistence.SessionDefaultDir)
	if focus == "" {
		return
	}
	patch["statusSession"] = focus
}

// decodePatchArg accepts either a JSON-encoded string (schema-compliant)
// or a nested JSON object (what LLMs often send), returning the patch
// map. Centralized here so graph_merge_attribute and graph_merge_object
// stay in sync; see graph_merge_object Run for the full rationale.
func decodePatchArg(raw any) (map[string]any, error) {
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil, fmt.Errorf("patch required")
		}
		var patch map[string]any
		if err := json.Unmarshal([]byte(v), &patch); err != nil {
			return nil, fmt.Errorf("patch must be valid JSON object: %w", err)
		}
		return patch, nil
	case map[string]any:
		if len(v) == 0 {
			return nil, fmt.Errorf("patch required")
		}
		return v, nil
	default:
		return nil, fmt.Errorf("patch required (string or object)")
	}
}

func graphMergeObjectTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "graph_merge_object",
				Description: "Apply a partial JSON patch to an existing object. Allowed fields: intent, impl, implFragment, implSymbol, implContent, implLang, status, statusSession, temporal, preconditions, postconditions, portObservation, storyPoints, storyRationale. Structural fields (def, consumes, produces) are not patchable — use unlink/relink. Patch must be a JSON object string.\n\nportObservation values (MUST be exact — common mistake is using \"return value\"): \"global\" / \"return\" / \"return.<dotted.path>\" / \"args.<n>.<dotted.path>\" / \"side_effect\". Example: '{\"portObservation\":{\"flags_config\":\"return\",\"ball_x\":\"return.ball.x\"}}'.\n\nOptional `session_id` temporarily swaps the focused session for the duration of the merge so the diff is recorded against that session — saves the focus / merge / re-focus cycle when ticking many child sessions through implementing→confirmed in a batch.",
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
			sessionID, _ := args["session_id"].(string)
			if id == "" {
				return "", fmt.Errorf("id required")
			}
			// Accept patch as EITHER a JSON-encoded string (schema-compliant)
			// OR a nested JSON object. LLMs frequently send the latter
			// despite the schema declaring string; without dual-accept the
			// agent burns a turn AND the four hooks downstream of merge
			// (confirmed-impl, typecalc-use, object-gate, status-transition)
			// disengage that turn with "could not run" warnings, silencing
			// the very anti-theater enforcement we need active at confirm.
			patch, err := decodePatchArg(args["patch"])
			if err != nil {
				return "", err
			}
			// 2026-05-09 v8.5 — typecalc-use becomes BLOCKING for the
			// status=confirmed transition. Previously the typecalc-use
			// hook (agent/hooks.go) only emitted an after-the-fact
			// warning; agents would set status=confirmed, see the warn,
			// then backfill compile evidence. The 5-instance v8.4 batch
			// hit this 14× across all 5 instances ("先上车后买票").
			// Now: refuse the merge before the on-disk graph mutates
			// when patch.status=confirmed without the evidence file.
			// The agent must run typecalc_compile (or typecalc_test)
			// FIRST. The error message points at the exact remediation.
			if statusVal, hasStatus := patch["status"]; hasStatus {
				if s, _ := statusVal.(string); s == graph.StatusConfirmed {
					if !typecalcEvidenceFileExists(id) {
						return "", fmt.Errorf(
							"refusing to set status=confirmed for object %q: no typecalc evidence at .kcpos/typecalc-evidence/%s.json. "+
								"Run typecalc_compile (or typecalc_test) for this object FIRST, then re-issue the merge. "+
								"This is the v8.5 anti-'confirm-first-evidence-later' enforcement: agents were observed flipping confirm and backfilling compile, undermining the typecalc invariant.",
							id, id)
					}
				}
			}
			// 2026-05-09 v8.5 / 2026-05-11 v9.0.3 — dual-source prevention.
			// The v7→v8.4 fixes routed HTML through the JS harness so kcpos
			// can test the actual deliverable, eliminating the need for
			// shadow K/impl/*.js files. The v8.4 batch saw agents create
			// those shadow files out of habit, where inline functions in
			// index.html drift from the .js shadows being tested → kcpos
			// says "all green" while the user-opened deliverable is broken.
			// v8.5 closed that by refusing impl=*.js when index.html exists.
			//
			// v9.0.3 keeps the same guarantee (no .js shadow as the
			// declared `impl`) but reintroduces a per-object writing
			// surface via the SEPARATE `implFragment` field — see
			// `kcpos doc protocol` § "Single-file deliverable model".
			// implFragment is intentionally NOT subject to this hook;
			// it's a staging path, not the deliverable.
			if implVal, hasImpl := patch["impl"]; hasImpl {
				if implPath, _ := implVal.(string); implPath != "" {
					cwd, _ := os.Getwd()
					indexExists := false
					if _, err := os.Stat(filepath.Join(cwd, "index.html")); err == nil {
						indexExists = true
					}
					ext := strings.ToLower(filepath.Ext(implPath))
					if indexExists && ext != ".html" && ext != ".htm" {
						return "", fmt.Errorf(
							"refusing to set impl=%q for object %q: project root contains index.html — kcpos v8 routes HTML inline scripts directly through the JS harness, so K/impl/*.js shadow files are unnecessary AND dangerous (the inline script and the shadow can drift, leaving the deliverable broken while kcpos says 'all green'). "+
								"v9.0.3 model for HTML single-file projects: set impl=\"index.html\" (the deliverable) PLUS implFragment=\"K/frags/<ObjectId>.js\" (this object's per-object staging file). Child sessions write to implFragment; the R2 build step concatenates every fragment into index.html. Example: graph_merge_object id=%q patch='{\"impl\":\"index.html\",\"implFragment\":\"K/frags/%s.js\"}'.",
							implPath, id, id, id)
					}
					// v9.3: refuse fragment-as-impl. v92-04 had every
					// confirmed object set impl=K/frags/<id>.js, which is
					// the staging path, not the deliverable. The build
					// step never assembled anything, the gate never ran
					// runtime_smoke (impl wasn't HTML), and the run looked
					// green while shipping nothing.
					normalized := strings.ReplaceAll(implPath, "\\", "/")
					if strings.HasPrefix(normalized, "K/frags/") {
						return "", fmt.Errorf(
							"refusing to set impl=%q for object %q: K/frags/ is the per-object STAGING area for fragments, not a deliverable path. The deliverable is what runs when the user opens the project (e.g. index.html for HTML projects, dist/<bundle>.js for bundled, src/<file>.go for Go). For HTML single-file projects use impl=\"index.html\" + implFragment=%q. If this is a multi-file project, set impl to the actual file the user will execute — never K/frags/*",
							implPath, id, implPath)
					}
					// v9.3.1: HTML impl REQUIRES implFragment. v93-04 monolithic
					// retro showed agent picked impl=index.html and skipped
					// implFragment entirely → wrote the entire game inline →
					// review's "read fragment not deliverable" optimisation
					// (Phase 2.2) became inert → reviewer fed the full 1176-
					// line index.html to the LLM → token overflow loop. Force
					// the fragment to be set in the same patch (or be already
					// set on the object) so HTML projects can only exist in
					// the canonical fragment form.
					if ext == ".html" || ext == ".htm" {
						hasFragInPatch := false
						if v, ok := patch["implFragment"].(string); ok && v != "" {
							hasFragInPatch = true
						}
						hasFragOnObj := false
						if g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath); err == nil {
							if obj, exists := g.Objects[id]; exists && obj.ImplFragment != nil && *obj.ImplFragment != "" {
								hasFragOnObj = true
							}
						}
						if !hasFragInPatch && !hasFragOnObj {
							return "", fmt.Errorf(
								"refusing to set impl=%q for object %q without implFragment: HTML deliverables MUST be assembled from per-object fragments (kcpos v9.3.1 canonical single-file model). Same patch must also set implFragment=\"K/frags/%s.js\" (each child session writes its own fragment; session_build assembles them in reference mode by default). v93-04 retro: ignoring this requirement produces a monolithic 1000+-line index.html that breaks review's prompt budget and bypasses the AP11 fragment scan",
								implPath, id, id)
						}
					}
				}
			}
			// v9.0.3 — validate implFragment path is under K/frags/ when
			// set, to keep all per-object staging files in one location
			// and prevent agents from picking arbitrary paths that
			// collide with the multi-file project model.
			if fragVal, hasFrag := patch["implFragment"]; hasFrag {
				if fragPath, _ := fragVal.(string); fragPath != "" {
					normalized := strings.ReplaceAll(fragPath, "\\", "/")
					if !strings.HasPrefix(normalized, "K/frags/") {
						return "", fmt.Errorf(
							"refusing to set implFragment=%q for object %q: implFragment must live under K/frags/ (per-object staging area). Example: implFragment=\"K/frags/%s.js\"",
							fragPath, id, id)
					}
				}
			}
			restore, err := withTempFocus(sessionID)
			if err != nil {
				return "", err
			}
			defer restore()
			injectStatusSessionFromFocus(patch)
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

// typecalcEvidenceFileExists wraps core.LoadObjectState so the
// graph_merge_object confirm-needs-evidence rule shares logic with
// the gate and the agent hook. Pre-v9.0 each of those three sites
// re-implemented the file probe; consolidation lives in
// internal/typecalc/state.go.
func typecalcEvidenceFileExists(objectID string) bool {
	if objectID == "" {
		return false
	}
	s := core.LoadObjectState(objectID, "")
	return s.HasUsableEvidence()
}

// autoCompileOnImplSet runs typecalc_compile against the file referenced
// by a freshly-set `impl` (or `implFragment`) field on a graph object,
// and writes evidence for that object on success. Returns a string to
// append to the merge result describing what happened (empty = no
// auto-compile triggered).
//
// Trigger: patch contains "impl" OR "implFragment" key with a non-empty
// string value. v9.0.3: when `implFragment` is present (or already set
// on the graph object), the fragment path is the auto-compile target
// because that's the per-object file the agent will write. Using `impl`
// as the target for a shared-deliverable HTML project meant N objects
// would re-trigger the same hook against the same index.html, producing
// N "pending compile" messages and pressuring the agent to single-shot
// write the entire deliverable from the parent context (the v9.0.2
// Terraria batch death mode).
func autoCompileOnImplSet(ctx context.Context, objectID string, patch map[string]any) string {
	target := pickAutoCompileTarget(objectID, patch)
	if target == "" {
		return ""
	}
	content, err := os.ReadFile(target)
	if err != nil {
		// File not on disk yet — likely the agent will write_file next
		// (which will trigger auto-typecalc-on-write since the path is now
		// declared). Don't fail the merge; just note.
		return fmt.Sprintf("[auto-typecalc] %q referenced but file not yet on disk; "+
			"compile will run automatically on the next write_file to that path", target)
	}
	implPath := target
	ext := filepath.Ext(implPath)
	langTag := core.LangFromExt(ext)
	if langTag == core.LangNone {
		return fmt.Sprintf("[auto-typecalc] could not infer language from extension %q (impl=%q); "+
			"call typecalc_compile object_id=%q lang=<L> manually", ext, implPath, objectID)
	}
	tv := core.New(core.KindCode, string(content)).
		WithState(core.StateUncompiled).
		WithLang(langTag)
	env := &core.RuleEnv{WorkDir: "."}
	out, err := lang.CompileLanguageInvoker(ctx, env, tv)
	if err != nil {
		return fmt.Sprintf("[auto-typecalc] invoker error on %s: %v", implPath, err)
	}
	if out.State == core.StateCompiled {
		effectiveLang := core.DetectEffectiveLang(string(content), langTag)
		if recErr := core.RecordEvidence(objectID, "compile", string(effectiveLang), true); recErr != nil {
			return fmt.Sprintf("[auto-typecalc] compile passed but evidence write failed: %v", recErr)
		}
		return fmt.Sprintf("[auto-typecalc] %s compile passed; evidence recorded for %s (lang=%s)",
			implPath, objectID, effectiveLang)
	}
	if out.Kind == core.KindCompileError {
		ce, _ := core.DecodeCompileError(out)
		return fmt.Sprintf(
			"[auto-typecalc] COMPILE FAILED on %s for object %s\n  errorCode: %s\n  errorLog:\n%s\n\n"+
				"No evidence was recorded. Fix the source file and re-merge (or simply re-write the file — "+
				"auto-typecalc-on-write will retry).",
			implPath, objectID, ce.ErrorCode, indent(ce.ErrorLog, "    "))
	}
	return ""
}

// pickAutoCompileTarget chooses what file to auto-compile after a
// graph_merge_object patch. v9.0.3 prefers `implFragment` (per-object,
// no shared-deliverable contention) over `impl` (often shared across N
// objects when the deliverable is a single index.html). The selection
// considers both the patch and the post-merge graph state — if the
// patch only sets `impl` but the object already had `implFragment`
// from a prior merge, we still use the fragment as the target.
func pickAutoCompileTarget(objectID string, patch map[string]any) string {
	// 1. patch-supplied implFragment wins.
	if v, has := patch["implFragment"]; has {
		if s, _ := v.(string); s != "" {
			return s
		}
	}
	// 2. existing implFragment on the merged-into object wins next.
	if g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath); err == nil {
		if obj, ok := g.Objects[objectID]; ok && obj.ImplFragment != nil && *obj.ImplFragment != "" {
			return *obj.ImplFragment
		}
	}
	// 3. fall back to impl from the patch.
	if v, has := patch["impl"]; has {
		if s, _ := v.(string); s != "" {
			return s
		}
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
// We use a closure rather than `defer persistence.SetFocus(prev)` directly
// so the merge tools can both share the pattern without each one
// repeating the save/restore boilerplate.
func withTempFocus(id string) (func(), error) {
	if id == "" {
		return func() {}, nil
	}
	prev, _ := persistence.GetFocus(persistence.SessionDefaultDir)
	if err := persistence.SetFocus(persistence.SessionDefaultDir, id); err != nil {
		return func() {}, fmt.Errorf("temp-focus %s: %w", id, err)
	}
	return func() {
		_ = persistence.SetFocus(persistence.SessionDefaultDir, prev)
	}, nil
}

func graphValidateTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: true,
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "graph_validate",
				Description: "Run all KonceptOS structural checks against K/graph.json: produce/consume balance (with subtype substitution), refines DAG (no cycles), naming uniqueness, reference integrity, temporal causality, plus warnings for orphan attributes, missing impl files, and incomplete metadata. Returns a human-readable report ending in PASS or FAIL.",
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
			if err != nil {
				return "", err
			}
			cwd, _ := os.Getwd()
			report := g.Validate(cwd)
			return report.String(), nil
		},
	}
}

func graphRenderTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: true,
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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
			g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
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

func graphPreflightTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: true,
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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
			g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
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

func graphAutowireTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: true,
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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
			g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
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
