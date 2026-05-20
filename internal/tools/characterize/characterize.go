// Package characterize wraps internal/legacy/characterize as an
// agent-callable tool. Per the original KonceptOS implementation
// plan (Phase 1.2): when typecalc_test returns TestError, the
// system MUST NOT loop the failure back to the LLM as "rewrite
// the impl"; it must enter brownfield characterization to determine
// whether the implementation is wrong or the test is wrong.
//
// The pre-2026-05-20 Phase-2.C outer Router omitted this branch and
// drove every TestError back through an LLM rewrite cycle, which
// (per KonceptOS_implementation_plan.md §1.2 L158/167/178) violates
// the documented design. This tool restores the missing entry point.
package characterize

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"github.com/creator915/Koncept_OS/internal/legacy/characterize"
	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// Tools returns the characterize tool set. Currently one tool.
func Tools() map[string]toolcall.Tool {
	return map[string]toolcall.Tool{
		"characterize": characterizeTool(),
	}
}

func characterizeTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name: "characterize",
				Description: "Brownfield-style behavioral characterization of an object's current implementation. " +
					"Reads the impl file declared on the graph object, runs LLM-synthesized probes against it, observes the actual outputs, " +
					"and records a 'characterization lock' — what the code actually DOES, independent of what its tests claim it should do. " +
					"\n\nPRIMARY USE per the original Handler 1.2 design: when typecalc_test returns TestError, do NOT immediately edit the impl and retry. " +
					"Call characterize FIRST. The result tells you whether (a) the impl honestly implements its intent and the tests were wrong " +
					"(LOCKED behavior matches description — regenerate tests via typecalc_synthesize_tests), or (b) the impl diverges from its intent " +
					"(UNLOCKED probes / behavior drift — then edit the impl).\n\n" +
					"Returns a JSON summary: { lockedCases: N, unlockedProbes: [...], assumptions: [...], oracle: {...}, persistedTo: '.kcpos/characterize/<id>.json' }",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object_id": map[string]interface{}{
							"type":        "string",
							"description": "Graph object id whose impl should be characterized. Must already have impl path set, an impl file on disk, and a synthesizable description (typecalc_describe should have run first).",
						},
						"stability": map[string]interface{}{
							"type":        "integer",
							"description": "Number of times to observe each probe (default 1). >1 enables the nondeterminism guard — probes whose output differs across observations are left UNLOCKED.",
						},
					},
					"required": []string{"object_id"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			id, _ := args["object_id"].(string)
			if id == "" {
				return "", fmt.Errorf("object_id required")
			}
			stability := 1
			switch v := args["stability"].(type) {
			case float64:
				stability = int(v)
			case int:
				stability = v
			}
			if stability < 1 {
				stability = 1
			}

			g, err := persistence.LoadGraph(persistence.GraphDefaultPath)
			if err != nil || g == nil {
				return "", fmt.Errorf("characterize: load graph: %v", err)
			}
			obj, ok := g.Objects[id]
			if !ok {
				return "", fmt.Errorf("characterize: object %q not found in graph", id)
			}
			if obj.Impl == nil || *obj.Impl == "" {
				return "", fmt.Errorf("characterize: object %q has no impl path — set impl via graph_merge_object before characterizing", id)
			}
			implPath := *obj.Impl
			absFile, err := filepath.Abs(implPath)
			if err != nil {
				return "", fmt.Errorf("characterize: resolve impl path: %v", err)
			}
			body, err := os.ReadFile(absFile)
			if err != nil {
				return "", fmt.Errorf("characterize: read impl file %q: %v — call session_build first if this is an HTML/implContent project", implPath, err)
			}

			lang := obj.ImplLang
			if lang == "" {
				lang = langFromExt(absFile)
			}
			if lang == "" {
				return "", fmt.Errorf("characterize: cannot infer lang for %q — set obj.implLang via graph_merge_object", implPath)
			}

			symbol := obj.ImplSymbol
			if symbol == "" {
				symbol = id
			}

			signature := readSignatureFromDef(obj)

			description := readDescriptionFromEvidence(id)
			if description == "" {
				intent := obj.Intent
				if intent == "" {
					intent = "(intent not set)"
				}
				description = "Object " + id + ": " + intent
			}

			produces := obj.Produces
			if len(produces) == 0 {
				produces = []string{"result"}
			}

			portObs := map[string]string{}
			for k, v := range obj.PortObservation {
				portObs[k] = v
			}
			if len(portObs) == 0 {
				if len(produces) == 1 {
					portObs[produces[0]] = "return"
				} else {
					for _, p := range produces {
						portObs[p] = "return." + p
					}
				}
			}

			env := map[string]string{
				"lang": lang,
				"os":   runtime.GOOS,
				"arch": runtime.GOARCH,
			}

			res, err := characterize.Characterize(ctx, characterize.CharRequest{
				ObjectID:        id,
				ImplSymbol:      symbol,
				Lang:            lang,
				ArtifactPath:    absFile,
				CodeHash:        core.HashSource(string(body)),
				Signature:       signature,
				Description:     description,
				Produces:        produces,
				PortObservation: portObs,
				Environment:     env,
				Stability:       stability,
				IntroducedBy:    "agent characterize tool (brownfield TestError diagnosis)",
			}, characterize.DefaultSynthesize(), characterize.RealHarness())
			if err != nil {
				return "", fmt.Errorf("characterize: %v", err)
			}

			if err := characterize.Persist(id, res); err != nil {
				return "", fmt.Errorf("characterize: persist lock: %v", err)
			}

			summary := buildSummary(id, res)
			b, _ := json.MarshalIndent(summary, "", "  ")
			return string(b), nil
		},
	}
}

func langFromExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".py":
		return "python"
	case ".js":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".html", ".htm":
		return "html"
	case ".go":
		return "go"
	}
	return ""
}

func readSignatureFromDef(obj *graph.Object) string {
	if obj.Def == "" {
		return ""
	}
	b, err := os.ReadFile(obj.Def)
	if err != nil {
		return ""
	}
	const max = 600
	s := string(b)
	if len(s) > max {
		s = s[:max] + "...(truncated)"
	}
	return s
}

// readDescriptionFromEvidence pulls the description previously written
// by typecalc_describe (if any) so characterize can compare LOCKED
// behavior against the agent's stated contract.
func readDescriptionFromEvidence(objectID string) string {
	path := filepath.Join(".kcpos", "typecalc", objectID+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var bundle struct {
		Spec struct {
			Description string `json:"description"`
		} `json:"spec"`
	}
	_ = json.Unmarshal(b, &bundle)
	return bundle.Spec.Description
}

type charSummary struct {
	ObjectID       string   `json:"objectId"`
	LockedCases    int      `json:"lockedCases"`
	UnlockedProbes []string `json:"unlockedProbes,omitempty"`
	Assumptions    []string `json:"assumptions,omitempty"`
	OracleVerdict  string   `json:"oracleVerdict"`
	PersistedTo    string   `json:"persistedTo"`
	Guidance       string   `json:"guidance"`
}

func buildSummary(objectID string, res *characterize.CharResult) charSummary {
	s := charSummary{
		ObjectID:    objectID,
		LockedCases: len(res.Lock.Cases),
		PersistedTo: filepath.Join(".kcpos", "characterize", objectID+".json"),
	}
	// Unlocked is []string per characterize/types.go — probe id +
	// reason joined by the engine.
	s.UnlockedProbes = append(s.UnlockedProbes, res.Lock.Unlocked...)
	for _, a := range res.Assumptions {
		s.Assumptions = append(s.Assumptions, a.ID+": "+a.Statement)
	}
	if res.Oracle.Property != "" {
		s.OracleVerdict = res.Oracle.Property
	}
	// Guidance string: drives the LLM's next decision per the original
	// design (KonceptOS_implementation_plan.md §1.2: "TestError 不打回
	// LLM — 进入 brownfield 流程（定位是代码错还是测试错）").
	switch {
	case s.LockedCases > 0 && len(s.UnlockedProbes) == 0:
		s.Guidance = "Implementation behavior is FULLY locked across probes — the impl honestly implements some consistent behavior. If typecalc_test failed against this impl, the tests are likely wrong (synthesized expectations don't match the impl's observed behavior). Regenerate tests via typecalc_synthesize_tests, then retry confirm_object."
	case s.LockedCases == 0:
		s.Guidance = "Implementation behavior could not be locked on ANY probe — the impl is either non-deterministic, broken, or fundamentally diverging from its declared intent. Edit the impl to match the intent + portObservation, then re-characterize."
	default:
		s.Guidance = fmt.Sprintf("Implementation has %d locked case(s) but %d UNLOCKED probe(s) — some behaviors are consistent, others diverge. Inspect the unlocked probes' reasons to decide whether the impl needs targeted fixes or the unlocked dimension is genuinely outside the contract.", s.LockedCases, len(s.UnlockedProbes))
	}
	return s
}
