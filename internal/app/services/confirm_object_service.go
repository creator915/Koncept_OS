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
	"github.com/creator915/Koncept_OS/internal/llm/provider"
	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
	"github.com/creator915/Koncept_OS/internal/router"
	"github.com/creator915/Koncept_OS/internal/router/chains"
	runtimetools "github.com/creator915/Koncept_OS/internal/tools/runtime"
	sessiontools "github.com/creator915/Koncept_OS/internal/tools/session"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// confirmObjectTool exposes the v9.0 state-machine chain as a single
// agent tool. The agent calls `confirm_object object_id=Foo` and the
// router drives compile → describe → synthesize → test → review →
// graph.merge(status=confirmed) automatically, including error
// enrichment + LLM-driven retry between any of those steps.
//
// Pre-v9.0 the agent had to call 6 separate tools in a specific order;
// each step was a place where the agent could pick the wrong tool,
// skip a step, or hit an enforcement hook. v9.0 collapses that into
// one structured call. The agent only chooses tools at the
// per-object boundary ("which object to confirm next") and at the
// session/checkpoint level — the typecalc chain is fully owned by the
// state machine.
//
// Failure modes from the chain's perspective:
//   - Confirmed<Object> → success, status flipped to confirmed
//   - Obstacle<Object,Reason> → hard failure (v9.2). The agent MUST fix
//     the impl, refactor the object, or extend kcpos's runner support.
//     The obstacle signal is informational only — no waiver path exists
//     to bless past it.
func confirmObjectTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name: "confirm_object",
				Description: "Drive ONE graph object through the full verification chain (compile → describe → synthesize_tests → test → review → mark confirmed) and stop when it reaches Confirmed or Obstacle. Replaces the v8.x sequence of 6 manual typecalc_* + graph_merge_object calls. Failures route through enrich-feedback + LLM-driven retry automatically; if the retry budget is exhausted the chain emits Obstacle. v9.2: Obstacle is a HARD failure — fix the impl, refactor, or extend kcpos's runner support. There is no waiver escape. Prerequisites: graph object exists with impl path set and (if needed) portObservation declared.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object_id":   map[string]interface{}{"type": "string", "description": "Graph object id to confirm."},
						"max_retries": map[string]interface{}{"type": "integer", "description": "Optional cap on per-stage enrich-retry cycles. 0 = use chain default (5)."},
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
			maxRetries := 0
			if v, ok := args["max_retries"].(float64); ok {
				maxRetries = int(v)
			}

			deps, err := buildProductionDeps(ctx)
			if err != nil {
				return "", err
			}
			r, err := chains.BuildChain(deps)
			if err != nil {
				return "", fmt.Errorf("build chain: %w", err)
			}
			initial, _ := router.NewTypedValue(chains.TypeStartConfirm, chains.StartConfirmPayload{
				ObjectID:   objectID,
				MaxRetries: maxRetries,
			})
			out, err := r.Run(ctx, initial)
			if err != nil {
				return "", fmt.Errorf("chain run: %w", err)
			}
			return formatChainResult(out)
		},
	}
}

// buildProductionDeps wires the chain's invoker interface to the
// concrete tools + on-disk bundle. Each invoker calls the same
// implementation the standalone agent tools use, then reads the
// bundle for structured results.
//
// Layering: tools/typecalc → router/typecalcchain (depends on this
// package's tools), but typecalcchain doesn't import tools/typecalc
// (it accepts the Deps interface). The wiring lives here so the cycle
// is broken.
func buildProductionDeps(ctx context.Context) (chains.Deps, error) {
	// Each invoker delegates to the existing tool's Run handler so we
	// preserve every side effect (evidence written, hash recorded,
	// stale-detection, etc.). Then we re-read the bundle to extract
	// the structured fields the chain needs.
	compileTool := typecalcCompileTool()
	describeTool := typecalcDescribeTool()
	synthTool := typecalcSynthesizeTestsTool()
	testTool := typecalcTestTool()
	reviewTool := typecalcReviewTool()
	// v9.3 HTML branch: confirm_object's chain calls runtime_smoke
	// in place of synthesize+test when the impl is HTML. The tool is
	// owned by tools/runtime; we look it up by name so the runtime
	// package owns its own registration.
	smokeTool, ok := runtimetools.Tools()["runtime_smoke"]
	if !ok {
		return chains.Deps{}, fmt.Errorf("buildProductionDeps: runtime_smoke not registered in runtimetools.Tools()")
	}
	// v9.3.1: session_build is invoked by the chain before each smoke
	// to keep the deliverable in sync with the fragment set. Reference
	// mode default makes this cheap.
	buildTool, ok := sessiontools.Tools()["session_build"]
	if !ok {
		return chains.Deps{}, fmt.Errorf("buildProductionDeps: session_build not registered in sessiontools.Tools()")
	}

	return typecalchainProductionDeps{
		compile:  compileTool,
		describe: describeTool,
		synth:    synthTool,
		test:     testTool,
		review:   reviewTool,
		smoke:    smokeTool,
		build:    buildTool,
	}.toDeps(), nil
}

// typecalchainProductionDeps holds the resolved tools so each invoker
// can call them without re-allocating. The toDeps() method builds the
// chain-shaped Deps struct.
type typecalchainProductionDeps struct {
	compile, describe, synth, test, review, smoke, build toolcall.Tool
}

func (p typecalchainProductionDeps) toDeps() chains.Deps {
	return chains.Deps{
		Compile: func(ctx context.Context, id string) (lang, kind string, ok bool, errorCode, errorLog string, err error) {
			_, runErr := p.compile.Run(ctx, map[string]interface{}{"object_id": id})
			if runErr != nil {
				// Compile-tool returns non-nil err on infrastructural
				// problems (missing impl, unparseable graph). Bubble up.
				return "", "", false, "", "", runErr
			}
			// Compile invocation completed; bundle's Compile section is
			// authoritative for what happened.
			b, hasB := core.ReadBundle(id)
			if !hasB || b.Compile == nil {
				return "", "", false, "", "compile invocation produced no bundle Compile section", nil
			}
			return b.Compile.Lang, b.Compile.Kind, b.Compile.OK, "", b.Compile.Log, nil
		},

		Describe: func(ctx context.Context, id string) (string, error) {
			_, runErr := p.describe.Run(ctx, map[string]interface{}{"object_id": id})
			if runErr != nil {
				return "", runErr
			}
			spec, ok := core.ReadSpec(id)
			if !ok {
				return "", fmt.Errorf("describe produced no spec section")
			}
			return spec.Description, nil
		},

		Synthesize: func(ctx context.Context, id string) (int, error) {
			out, runErr := p.synth.Run(ctx, map[string]interface{}{"object_id": id})
			if runErr != nil {
				return 0, runErr
			}
			// The synthesizer occasionally emits CANNOT_SYNTHESIZE as
			// the raw response (no bundle update). Detect and signal
			// caseCount=0, which the chain treats as Obstacle.
			if strings.Contains(out, "CANNOT_SYNTHESIZE") {
				return 0, nil
			}
			tests, ok := core.ReadTests(id)
			if !ok {
				return 0, nil
			}
			return len(tests.Cases), nil
		},

		Test: func(ctx context.Context, id string) (kind string, ok bool, failingCase, expected, actual, runnerLog string, err error) {
			_, runErr := p.test.Run(ctx, map[string]interface{}{"object_id": id})
			if runErr != nil {
				return "", false, "", "", "", "", runErr
			}
			b, hasB := core.ReadBundle(id)
			if !hasB || b.Test == nil {
				return "", false, "", "", "", "test invocation produced no Test section", nil
			}
			// Pull the failing-case excerpt from the runtime trace's
			// log tail (best effort — exact structure varies by lang).
			logTail := tailLines(b.Test.Log, 30)
			return b.Test.Kind, b.Test.OK, "", "", "", logTail, nil
		},

		// v9.3 HTML branch: Smoke runs runtime_smoke. Verdict comes from
		// the bundle's RuntimeSmoke section — loadFired AND no pageErrors
		// is the gate's pass criterion. Canvas pixel state is advisory
		// only (see Phase 3.2); not every HTML deliverable has a canvas.
		Smoke: func(ctx context.Context, id string) (ok bool, summary string, err error) {
			out, runErr := p.smoke.Run(ctx, map[string]interface{}{"object_id": id})
			if runErr != nil {
				return false, "", runErr
			}
			b, hasB := core.ReadBundle(id)
			if !hasB || b.RuntimeSmoke == nil {
				return false, "runtime_smoke produced no RuntimeSmoke section", nil
			}
			summary = tailLines(out, 30)
			if !b.RuntimeSmoke.OK {
				var parts []string
				if !b.RuntimeSmoke.LoadFired {
					parts = append(parts, "load event did not fire within timeout")
				}
				for _, e := range b.RuntimeSmoke.PageErrors {
					parts = append(parts, "pageError: "+e.Message)
				}
				for _, e := range b.RuntimeSmoke.ConsoleErrors {
					parts = append(parts, "consoleError: "+e.Message)
				}
				if len(parts) > 0 {
					summary = strings.Join(parts, "\n") + "\n\n--- raw smoke output (tail) ---\n" + summary
				}
			}
			return b.RuntimeSmoke.OK, summary, nil
		},

		// v9.3.1: pre-smoke assembly. Called by the chain's HTML branch
		// before runtime_smoke so the deliverable always reflects the
		// current fragment set. Defaults to reference mode (cheap).
		Build: func(ctx context.Context) (string, error) {
			return p.build.Run(ctx, map[string]interface{}{})
		},

		IsHTMLImpl: func(ctx context.Context, id string) (bool, error) {
			g, gerr := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
			if gerr != nil {
				return false, gerr
			}
			obj, ok := g.Objects[id]
			if !ok {
				return false, fmt.Errorf("object %q not in graph", id)
			}
			if obj.Impl == nil || *obj.Impl == "" {
				return false, nil
			}
			lower := strings.ToLower(*obj.Impl)
			return strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".htm"), nil
		},

		Review: func(ctx context.Context, id string) (ok bool, staticIssues, runtimeIssues, reviewerReasons []string, confidence float64, err error) {
			_, runErr := p.review.Run(ctx, map[string]interface{}{"object_id": id})
			if runErr != nil {
				return false, nil, nil, nil, 0, runErr
			}
			b, hasB := core.ReadBundle(id)
			if !hasB || b.Accepted == nil {
				return false, nil, nil, nil, 0, fmt.Errorf("review invocation produced no Accepted section")
			}
			for _, iss := range b.Accepted.StaticIssues {
				staticIssues = append(staticIssues, fmt.Sprintf("[%s] %s — %s", iss.Code, iss.Where, iss.Message))
			}
			for _, iss := range b.Accepted.RuntimeIssues {
				runtimeIssues = append(runtimeIssues, fmt.Sprintf("[%s] %s — %s", iss.Code, iss.Where, iss.Message))
			}
			return b.Accepted.OK, staticIssues, runtimeIssues, b.Accepted.Reasonableness.Reasons, b.Accepted.Reasonableness.Confidence, nil
		},

		FixImpl: func(ctx context.Context, id, prompt string) (branch string, obstacleReason string, err error) {
			// LLM-driven retry handler. The router has formatted the
			// Request<...> envelope into `prompt`; we ship it to the
			// LLM with a focused system message, parse the response
			// for either (a) an edited impl source the LLM committed
			// via the tools it has access to in the inner call, or
			// (b) a structured "obstacle" reply.
			//
			// For v9.0 first cut: the inner call uses the standard
			// LLM client with no tools. The LLM's job is to ANALYSE
			// the failure and EITHER:
			//   - reply with TYPE: Retry plus a brief reasoning note
			//     (caller is expected to have edited the impl via
			//     other tools BEFORE re-invoking confirm_object), OR
			//   - reply with TYPE: Obstacle<Object,Reason> with a
			//     structural explanation.
			//
			// This means the v9.0 enrich-retry loop ALWAYS terminates
			// at an Obstacle if the LLM can't fix the impl in a single
			// non-tool reply. That's deliberate: agent-tool-using fixes
			// (write_file edits) happen OUTSIDE the chain by the
			// outer agent loop. The chain handles the "I diagnosed the
			// issue and here's what should change" signaling.
			//
			// Future enhancement: give the inner LLM call write_file/
			// edit tools so it can make the edit itself. Phase 7
			// keeps the boundary simpler.
			return fixImplViaLLM(ctx, id, prompt)
		},

		MarkConfirmed: func(ctx context.Context, id string) error {
			return mutateGraph(func(g *graph.Graph) error {
				obj, ok := g.Objects[id]
				if !ok {
					return fmt.Errorf("object %q not in graph", id)
				}
				// State-transition rule: must pass through implementing
				// first if currently declared. The hook would block a
				// direct declared→confirmed merge; we replicate the
				// sequence here so confirm_object can drive it.
				if obj.Status == graph.StatusDeclared {
					obj.Status = graph.StatusImplementing
					obj.StatusSession = nil
				}
				obj.Status = graph.StatusConfirmed
				obj.StatusSession = nil
				g.Objects[id] = obj
				return nil
			})
		},
	}
}

// fixImplViaLLM is the chain's inner repair step. v9.0.2 replaces the
// v9.0 "diagnose-only" first cut: an inner LLM call proposes a
// STRUCTURED edit, this function applies it to the impl file, and the
// chain returns "retry" so runCompile re-walks the chain on the freshly
// edited source. Falls back to "obstacle" when:
//   - the impl path cannot be resolved (graph missing)
//   - the LLM proposes obstacle (structural infeasibility)
//   - the LLM's edit didn't actually change the file (search-string miss)
//   - the LLM call itself errored
//
// The chain's retry budget (DefaultMaxRetries=5) caps how many fix
// rounds can fire per confirm_object invocation. After 5 the chain
// emits Obstacle terminal regardless.
//
// Tests stub this with their own logic by reassigning the field on a
// typecalchainProductionDeps before BuildChain.
func fixImplViaLLM(ctx context.Context, objectID, prompt string) (branch, reason string, err error) {
	implPath, implContent, loadErr := loadImplFor(objectID)
	if loadErr != nil {
		// Without the impl path we can't apply an edit. Fall back to
		// the v9.0 diagnose-only mode: surface the enriched feedback
		// so the outer agent can still act on it manually.
		return "obstacle", fmt.Sprintf(
			"Chain could not resolve impl for %s (%v). Outer agent: review the enriched feedback below and edit the impl, then re-invoke confirm_object. v9.2 — no waiver escape; you must make the verification actually pass.\n\n--- enriched feedback ---\n%s",
			objectID, loadErr, prompt), nil
	}

	branch, reason, err = invokeFixerLLM(ctx, objectID, implPath, implContent, prompt)
	if err != nil {
		// LLM failure → fall back to obstacle with the enriched feedback
		// so the outer agent still has actionable context.
		return "obstacle", fmt.Sprintf(
			"Chain failed to call inner repair LLM for %s (%v). Outer agent: review the enriched feedback and act manually.\n\n--- enriched feedback ---\n%s",
			objectID, err, prompt), nil
	}
	return branch, reason, nil
}

// loadImplFor reads the graph, finds objectID's impl path, and returns
// the path + current file content. Used by fixImplViaLLM to set up the
// repair LLM call with the actual source.
func loadImplFor(objectID string) (path, content string, err error) {
	g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if err != nil {
		return "", "", fmt.Errorf("load graph: %w", err)
	}
	obj, ok := g.Objects[objectID]
	if !ok {
		return "", "", fmt.Errorf("object %q not in graph", objectID)
	}
	if obj.Impl == nil || *obj.Impl == "" {
		return "", "", fmt.Errorf("object %q has no impl path set", objectID)
	}
	body, err := os.ReadFile(*obj.Impl)
	if err != nil {
		return "", "", fmt.Errorf("read impl %q: %w", *obj.Impl, err)
	}
	return *obj.Impl, string(body), nil
}

// invokeFixerLLM calls the LLM with a focused prompt asking for either
// a STRUCTURED EDIT or an OBSTACLE. The edit format is intentionally
// narrow (path + search + replace) so the model can't accidentally
// rewrite the whole file or introduce unrelated changes.
func invokeFixerLLM(ctx context.Context, objectID, implPath, implContent, enriched string) (branch, reason string, err error) {
	cfg, err := provider.ProviderFromEnv()
	if err != nil {
		return "", "", fmt.Errorf("load llm: %w", err)
	}
	cfg.Thinking = false // structured edit, no reasoning needed
	client := transport.NewClient(cfg)

	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Object id: %s\nImpl path: %s\n\n", objectID, implPath)
	fmt.Fprintf(&prompt, "## Current impl contents\n```\n%s\n```\n\n", clipMid(implContent, 12000))
	fmt.Fprintf(&prompt, "## Failure diagnosis (from the verification chain)\n%s\n\n", enriched)
	prompt.WriteString("Propose a minimal structured edit to fix THIS specific failure, OR declare Obstacle if the failure is structurally infeasible to fix by editing (e.g. requires Canvas / DOM / browser-only APIs).")

	msgs := []transport.Message{
		{Role: "system", Content: fixerSystemPrompt},
		{Role: "user", Content: prompt.String()},
	}
	resp, err := client.Chat(ctx, msgs, nil, transport.StreamHandler{})
	if err != nil {
		return "", "", fmt.Errorf("llm chat: %w", err)
	}

	verdict, payload := parseFixerVerdict(resp.Content)
	switch verdict {
	case "edit":
		edits, perr := parseFixerEdits(payload)
		if perr != nil || len(edits) == 0 {
			return "obstacle", fmt.Sprintf(
				"Inner repair LLM returned TYPE: Edit but the payload was unparseable (%v). Outer agent: please review the raw response and act manually.\n\nRaw response excerpt:\n%s\n\n--- original diagnosis ---\n%s",
				perr, clipMid(payload, 2000), enriched), nil
		}
		applied, applyErr := applyFixerEdits(implPath, edits)
		if applyErr != nil {
			return "obstacle", fmt.Sprintf(
				"Inner repair LLM proposed edits but they could not be applied (%v). Common cause: the `search` string did not match the file verbatim. Outer agent: please apply the fix manually.\n\nProposed edits (raw):\n%s\n\n--- original diagnosis ---\n%s",
				applyErr, clipMid(payload, 2000), enriched), nil
		}
		if applied == 0 {
			return "obstacle", fmt.Sprintf(
				"Inner repair LLM proposed %d edits but none changed the file (every `search` string was absent or replaced text identical). Treating as no-progress and escalating.\n\n--- original diagnosis ---\n%s",
				len(edits), enriched), nil
		}
		return "retry", fmt.Sprintf("applied %d edit(s) to %s — retrying chain", applied, implPath), nil
	case "obstacle":
		// Pass-through: chain marks terminal with this reason.
		return "obstacle", strings.TrimSpace(payload), nil
	default:
		// Unparseable reply — surface raw text in the obstacle so the
		// outer agent has at least the full LLM response.
		return "obstacle", fmt.Sprintf(
			"Inner repair LLM response did not start with TYPE: Edit or TYPE: Obstacle. Treating as no-progress.\n\nRaw response:\n%s\n\n--- original diagnosis ---\n%s",
			clipMid(resp.Content, 2000), enriched), nil
	}
}

const fixerSystemPrompt = `You are the inner repair step of the kcpos verification chain. A graph object's compile/test/review failed; the verification chain is now asking YOU to either propose a precise edit that fixes the failure, or declare it structurally infeasible to fix.

Output starts with one of two markers on its FIRST line:

  TYPE: Edit
  [ { "path": "<file>", "search": "<EXACT text to match>", "replace": "<replacement>" } ]

  TYPE: Obstacle
  <one paragraph: why this failure cannot be fixed by editing impl alone>

Rules for ` + "`TYPE: Edit`" + `:
1. ` + "`search`" + ` must appear LITERALLY in the file — same indentation, same whitespace, same line breaks. The host applies replacements by exact string match (not regex). A misplaced indent or wrong line ending = the edit silently does nothing → you get retried with the same failure.
2. Make ` + "`search`" + ` as small as possible while still uniquely identifying the location (typically 1–5 lines). Larger ` + "`search`" + ` strings are more brittle.
3. You may return multiple edits in the array if several disjoint changes are needed for the same fix. Apply order is array order.
4. The host writes the file with applied edits in one atomic operation, then re-runs compile→describe→synthesize→test→review. So edits must produce code that is syntactically valid AND compiles in isolation.
5. Don't refactor. Fix the specific failure called out in the diagnosis. Surrounding code may have other issues — leave them alone.

Rules for ` + "`TYPE: Obstacle`" + `:
- Use this ONLY when no edit can resolve the failure: language without a runner (extend kcpos's lang/ package to add one), side-effect-only ports without observable outputs (refactor the impl to expose at least one observable port), randomness that defeats deterministic synthesis (seed the impl deterministically or wire portObservation appropriately).
- One paragraph, specific. v9.2 — the outer agent will see this as a HARD FAILURE. There is no waiver escape; obstacle output exists only to help the human/agent decide the next code change.

Output nothing else — no Markdown fences, no commentary, no greeting. First line is the TYPE marker; subsequent lines are the payload.`

// parseFixerVerdict extracts the TYPE marker and the remaining payload.
// Returns (kind, payload). kind ∈ {"edit","obstacle",""} where "" means
// the response is unparseable (no recognised header).
func parseFixerVerdict(raw string) (kind, payload string) {
	s := strings.TrimSpace(raw)
	// strip optional ```...``` fences the LLM may have added
	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s, "\n"); idx > 0 {
			s = s[idx+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
		s = strings.TrimSpace(s)
	}
	line, rest, _ := strings.Cut(s, "\n")
	header := strings.ToLower(strings.TrimSpace(line))
	switch {
	case strings.HasPrefix(header, "type: edit"), strings.HasPrefix(header, "type:edit"):
		return "edit", strings.TrimSpace(rest)
	case strings.HasPrefix(header, "type: obstacle"), strings.HasPrefix(header, "type:obstacle"):
		return "obstacle", strings.TrimSpace(rest)
	}
	return "", s
}

// fixerEdit is one search-and-replace operation the inner LLM proposes.
type fixerEdit struct {
	Path    string `json:"path"`
	Search  string `json:"search"`
	Replace string `json:"replace"`
}

// parseFixerEdits parses the JSON array payload following a TYPE: Edit
// marker. Tolerates trailing ``` fences and surrounding whitespace.
func parseFixerEdits(payload string) ([]fixerEdit, error) {
	s := strings.TrimSpace(payload)
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	var edits []fixerEdit
	if err := json.Unmarshal([]byte(s), &edits); err != nil {
		return nil, fmt.Errorf("parse edits json: %w", err)
	}
	return edits, nil
}

// applyFixerEdits runs each edit in array order, doing a literal-string
// replace at the first occurrence of `search`. Returns the number of
// edits that actually changed the file. Errors only on I/O problems —
// a missing `search` is treated as "no change" (counted as 0 applied)
// so the caller can decide to escalate.
func applyFixerEdits(defaultPath string, edits []fixerEdit) (int, error) {
	// Group edits by path so we read+write each file once.
	byPath := map[string][]fixerEdit{}
	for _, e := range edits {
		p := e.Path
		if p == "" {
			p = defaultPath
		}
		byPath[p] = append(byPath[p], e)
	}
	applied := 0
	for path, list := range byPath {
		body, err := os.ReadFile(path)
		if err != nil {
			return applied, fmt.Errorf("read %s: %w", path, err)
		}
		updated := string(body)
		for _, e := range list {
			if e.Search == "" {
				continue
			}
			if !strings.Contains(updated, e.Search) {
				continue
			}
			next := strings.Replace(updated, e.Search, e.Replace, 1)
			if next != updated {
				updated = next
				applied++
			}
		}
		if updated == string(body) {
			continue
		}
		// Atomic-ish: write to a sibling tmp then rename, so a half-
		// written impl never sits on disk if the process dies mid-write.
		tmp := path + ".kcpos-fix.tmp"
		if err := os.WriteFile(tmp, []byte(updated), 0o644); err != nil {
			return applied, fmt.Errorf("write %s: %w", tmp, err)
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			return applied, fmt.Errorf("rename %s: %w", path, err)
		}
	}
	return applied, nil
}

// clipMid trims the middle of a string if it exceeds n bytes, preserving
// head and tail so both the start and the error context remain visible.
func clipMid(s string, n int) string {
	if len(s) <= n {
		return s
	}
	half := n / 2
	return s[:half] + "\n…[" + fmt.Sprintf("%d bytes elided", len(s)-n) + "]…\n" + s[len(s)-half:]
}

// formatChainResult renders the terminal TypedValue as a human-readable
// agent-facing summary.
func formatChainResult(out router.TypedValue) (string, error) {
	switch out.Type {
	case chains.TypeConfirmed:
		var p chains.ConfirmedPayload
		if err := out.Unmarshal(&p); err != nil {
			return "", err
		}
		return fmt.Sprintf("✓ confirm_object %s → Confirmed (compile + describe + synthesize + test + review all passed, status flipped to confirmed)", p.ObjectID), nil
	case chains.TypeObstacle:
		var p chains.ObstaclePayload
		if err := out.Unmarshal(&p); err != nil {
			return "", err
		}
		return fmt.Sprintf("⚠ confirm_object %s → Obstacle at %s\n%s\n\nv9.2 — this is a HARD failure (no waiver escape). Edit the impl per the diagnosis above, refactor the object into a verifiable form, or extend kcpos's lang/ package to add a runner for this language. Then re-invoke confirm_object.", p.ObjectID, p.LastType, p.Reason), nil
	default:
		// Non-terminal — router run completed at a non-terminal type,
		// which should never happen given the chain's closure.
		raw, _ := json.Marshal(out)
		return "", fmt.Errorf("confirm_object: chain ended at non-terminal type %q (programmer error): %s", out.Type, string(raw))
	}
}

// tailLines returns the last `n` newline-separated lines of s, joined.
func tailLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
