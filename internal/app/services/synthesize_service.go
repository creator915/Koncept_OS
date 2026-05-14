package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
	"github.com/creator915/Koncept_OS/internal/typecalc/synthesize"
)

// typecalcSynthesizeTestsTool produces a test suite derived from the
// spec — intent + auto-generated description + signature + port
// declarations — WITHOUT seeing the impl source. Tests must verify the
// contract, not the implementation. The synthesized suite is also
// instructed to append per-call inputs/outputs to the runtime trace
// file the runtime-check rule reads.
//
// Canonical flow (post-2026-05-08):
//
//	write_file impl
//	  → typecalc_describe       (kind=spec)
//	  → typecalc_synthesize_tests (kind=tests)
//	  → typecalc_test           (using the synthesized tests, kind=test
//	                            evidence + .kcpos/typecalc-runtime trace)
//	  → typecalc_review         (static + runtime + reasonableness)
//	  → graph_merge_object status=confirmed
func typecalcSynthesizeTestsTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: true, // LLM call + per-id <id>.tests.json write; safe to parallelize
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name: "typecalc_synthesize_tests",
				Description: "Generate a test suite for an object from its spec (intent + description + signature + port declarations) WITHOUT inspecting the impl source. Tests verify the contract, not the implementation. Persists kind=tests evidence at .kcpos/typecalc-evidence/<id>.tests.json.\n\nPrerequisite: typecalc_describe must have run for this object_id (kind=spec evidence on disk) — synthesis without a description has nothing to drive it.\n\nThe generated tests are also instructed to log every function call's inputs and outputs to .kcpos/typecalc-runtime/<id>.json. The runtime-check rule (in typecalc_review) reads that trace to verify port presence, value ranges, and temporal causality against the graph's declarations — this is the user-design 'standard 1' (mechanical 'must-be-wrong' filter).\n\nThe tool returns the generated test code so you can immediately pass it to typecalc_test. If the LLM cannot synthesise (signature too underspecified), the response begins with the literal token CANNOT_SYNTHESIZE and the agent should refine the description first.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object_id": map[string]interface{}{
							"type":        "string",
							"description": "Graph object id whose tests should be synthesized.",
						},
						"lang": map[string]interface{}{
							"type":        "string",
							"description": "Optional. Override the language. Inferred from the impl path's extension when omitted.",
						},
					},
					"required": []string{"object_id"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			objectID, _ := args["object_id"].(string)
			if objectID == "" {
				return "", fmt.Errorf("object_id required")
			}
			langArg, _ := args["lang"].(string)

			g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
			if err != nil {
				return "", err
			}
			obj, ok := g.Objects[objectID]
			if !ok {
				return "", fmt.Errorf("object %q not found in K/graph.json", objectID)
			}
			// v9.3: HTML deliverables are verified by runtime_smoke, not
			// the synth/test chain. The 2026-05-12 v92 batch had multiple
			// instances burn 30+ minutes synthesizing tests for HTML/canvas
			// objects whose impl was index.html — the vm.Script harness
			// couldn't model the browser, every test fabricated, and the
			// agent looped on irrelevant Insufficient verdicts. Hard-error
			// here so the agent stops before the loop starts.
			if obj.Impl != nil && strings.HasSuffix(strings.ToLower(*obj.Impl), ".html") {
				return "", fmt.Errorf("typecalc_synthesize_tests: object %q has impl=%s (HTML deliverable). HTML objects are verified by runtime_smoke, not the synth/test chain — call `runtime_smoke object_id=%s` instead. If you want unit-level evidence on an extracted JS fragment, set obj.implFragment and run confirm_object (the chain will route correctly)", objectID, *obj.Impl, objectID)
			}

			// Spec evidence is the load-bearing input — synthesis without a
			// description has nothing to constrain it.
			spec, ok := core.ReadSpec(objectID)
			if !ok {
				return "", fmt.Errorf("no spec evidence for %s — call typecalc_describe object_id=%q first", objectID, objectID)
			}

			// Hash cache: if synthesized tests already exist for the
			// CURRENT spec hash, the suite is still valid — skip the LLM
			// call. Spec changes invalidate this naturally because the
			// re-described spec has a new SourceHash.
			if existing, ok := core.ReadTests(objectID); ok && existing.SpecHash == spec.SourceHash && len(existing.TestCode) > 0 {
				return fmt.Sprintf(
					"synthesized tests for %s [cache hit, no LLM call] — kept %s\n\n--- test code (cached) ---\n%s",
					objectID,
					core.TestsEvidencePath(objectID),
					existing.TestCode,
				), nil
			}

			// Read the def file (signature only — no impl) to give the LLM
			// the canonical interface.
			defBody := []byte{}
			if obj.Def != "" {
				defBody, _ = os.ReadFile(obj.Def)
			}
			// v9.0.5.4: extract any `@example` JSDoc blocks from the def
			// body and surface them prominently to the LLM. Concrete I/O
			// pairs are a far stronger grounding signal than @param /
			// @returns alone — the synthesized cases will inherit the
			// shape of the examples instead of inventing it from scratch.
			examples := extractJSDocExamples(string(defBody))

			// Language: argument > impl extension > "unknown".
			lang := langArg
			if lang == "" && obj.Impl != nil && *obj.Impl != "" {
				ext := extOf(*obj.Impl)
				lang = string(core.LangFromExt(ext))
			}

			// Build a JSON snippet of the produced/mutated attribute
			// valueSpaces for the LLM to honour.
			vs := buildValueSpaceSnippet(g, obj.Produces, obj.Mutates, obj.Consumes)

			poJSON := ""
			if len(obj.PortObservation) > 0 {
				if raw, err := json.MarshalIndent(obj.PortObservation, "", "  "); err == nil {
					poJSON = string(raw)
				}
			}
			out, err := synthesize.SynthesizeTests(ctx, synthesize.SynthesizeInputs{
				ObjectID:        objectID,
				Lang:            lang,
				Intent:          obj.Intent,
				Description:     spec.Description,
				Signature:       string(defBody),
				ImplSymbol:      obj.ImplSymbol,
				Consumes:        obj.Consumes,
				Produces:        obj.Produces,
				Mutates:         obj.Mutates,
				ValueSpace:      vs,
				PortObservation: poJSON,
				Examples:        examples,
			})
			if err != nil {
				return "", err
			}
			// CANNOT_SYNTHESIZE: surface verbatim, no evidence written.
			if len(out.Cases) == 0 && strings.HasPrefix(strings.TrimSpace(out.TestCode), "CANNOT_SYNTHESIZE") {
				return out.TestCode, nil
			}
			rec := &core.TestsEvidence{
				ObjectID: objectID,
				Lang:     lang,
				SpecHash: spec.SourceHash,
				Cases:    out.Cases,
				TestCode: out.TestCode,
			}
			if err := core.WriteTests(rec); err != nil {
				return "", fmt.Errorf("persist tests evidence: %w", err)
			}
			mode := "schema-driven (harness will render)"
			n := len(out.Cases)
			if n == 0 {
				mode = "legacy raw test code"
				n = strings.Count(out.TestCode, "\n")
			}
			return fmt.Sprintf(
				"synthesized tests for %s — wrote %s (%d %s)\n\n--- mode: %s ---",
				objectID,
				core.TestsEvidencePath(objectID),
				n,
				ifThenElse(len(out.Cases) > 0, "cases", "lines"),
				mode,
			), nil
		},
	}
}

func ifThenElse(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// extractJSDocExamples parses JSDoc-style `@example` blocks from a
// .js def file. Each block starts with `* @example` and runs until
// the next `@tag`, the JSDoc-closing `*/`, or another `@example`.
// Returns the bodies (lines after `@example` up to the next marker)
// with leading `*` decoration and indentation stripped, so each
// element is ready to drop verbatim into the synthesizer prompt.
//
// Empty bodies are dropped (`@example` with no following content
// would just be noise for the LLM).
func extractJSDocExamples(src string) []string {
	if src == "" {
		return nil
	}
	var out []string
	lines := strings.Split(src, "\n")
	in := false
	cur := []string{}
	flush := func() {
		if len(cur) == 0 {
			return
		}
		body := strings.TrimRight(strings.Join(cur, "\n"), " \t\n")
		if body != "" {
			out = append(out, body)
		}
		cur = cur[:0]
	}
	stripStar := func(ln string) string {
		t := strings.TrimSpace(ln)
		t = strings.TrimPrefix(t, "*")
		t = strings.TrimLeft(t, " \t")
		return t
	}
	for _, ln := range lines {
		trim := strings.TrimSpace(ln)
		if strings.HasPrefix(trim, "*/") {
			flush()
			in = false
			continue
		}
		if strings.Contains(trim, "@example") {
			flush()
			in = true
			// optional trailing label after @example: "@example boundary" — keep it as the first line context.
			afterTag := trim
			if i := strings.Index(afterTag, "@example"); i >= 0 {
				afterTag = strings.TrimSpace(afterTag[i+len("@example"):])
			}
			afterTag = strings.TrimLeft(afterTag, "* \t")
			if afterTag != "" {
				cur = append(cur, afterTag)
			}
			continue
		}
		if !in {
			continue
		}
		// Stop collecting when we hit ANOTHER @tag.
		if strings.HasPrefix(strings.TrimLeft(trim, "* \t"), "@") {
			flush()
			in = false
			continue
		}
		cur = append(cur, stripStar(ln))
	}
	flush()
	return out
}

func extOf(path string) string {
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[i:]
	}
	return ""
}

// buildValueSpaceSnippet emits a compact JSON map { attr → valueSpace }
// covering every port the object touches, so the LLM can write tests
// that respect the declared types/ranges.
func buildValueSpaceSnippet(g *graph.Graph, ports ...[]string) string {
	out := map[string]map[string]interface{}{}
	for _, group := range ports {
		for _, p := range group {
			a, ok := g.Attributes[p]
			if !ok || len(a.ValueSpace) == 0 {
				continue
			}
			out[p] = a.ValueSpace
		}
	}
	if len(out) == 0 {
		return ""
	}
	raw, _ := json.MarshalIndent(out, "", "  ")
	return string(raw)
}
