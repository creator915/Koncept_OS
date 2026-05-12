package typecalc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/creator915/Koncept_OS/internal/llm"
)

// SynthesizeInputs is what an LLM needs to write a test suite from the
// spec — NOT from the impl source. Tests should test the contract,
// independent of implementation. The reviewer's reasonableness pass
// also benefits from knowing the tests are spec-derived.
type SynthesizeInputs struct {
	ObjectID    string
	Lang        string
	Intent      string
	Description string
	Signature   string
	// Consumes / Produces / Mutates carry the graph's port declarations
	// so the LLM can name them consistently when emitting the runtime
	// trace JSON.
	Consumes []string
	Produces []string
	Mutates  []string
	// ValueSpace is a JSON snippet of declared per-attribute valueSpace
	// constraints. Folded into the prompt so the LLM can produce edge-
	// case tests aligned with declared types/ranges.
	ValueSpace string
	// PortObservation tells the LLM HOW each port becomes observable at
	// runtime — drives the "call" expression style. e.g. if every port
	// is "return.X", the call should be `IMPL.fn(...)` so the harness
	// captures the return value. If "global", the call mutates globals.
	// Folded into the prompt as a JSON object {port: extractor}.
	PortObservation string
	// Examples (v9.0.5.4) are concrete `@example` blocks extracted from
	// the JS def file. Far stronger grounding than @param / @returns
	// alone — the synthesized cases inherit the shape of these instead
	// of inventing it. Each entry is the raw text of one example.
	Examples []string
}

// SynthesizeTests asks an LLM to generate test cases from the spec
// (intent + description + signature + port declarations) as structured
// JSON. The LLM is forbidden from looking at the impl source — the
// prompt does not include it. The harness layer renders the cases into
// runnable test code, eliminating the class of bugs caused by LLM-
// written test framework code.
func SynthesizeTests(ctx context.Context, in SynthesizeInputs) (*SynthesizeOutput, error) {
	cfg, err := llm.ProviderFromEnv()
	if err != nil {
		return nil, fmt.Errorf("synthesize: load llm config: %w", err)
	}
	cfg.Thinking = false // sub-tool: structured output, no thinking needed
	client := llm.NewClient(cfg)
	return SynthesizeWithInvoker(ctx, in, func(ctx context.Context, prompt string) (string, error) {
		msgs := []llm.Message{
			{Role: "system", Content: synthesizerSystemPrompt},
			{Role: "user", Content: prompt},
		}
		resp, err := client.Chat(ctx, msgs, nil, llm.StreamHandler{})
		if err != nil {
			return "", err
		}
		return resp.Content, nil
	})
}

// SynthesizeOutput is the parsed result of one synthesize round. Either
// `Cases` (preferred, schema-driven) is non-nil, or `TestCode` (legacy
// raw source) is non-empty. Exactly one is expected per call.
type SynthesizeOutput struct {
	Cases    []TestCase
	TestCode string
}

// SynthesizeWithInvoker is the testable seam. The Invoker takes a
// prompt and returns the assistant's reply. The reply is expected to
// be either:
//   - JSON `{"objectId":"...","cases":[...]}` (preferred)
//   - JSON `{"objectId":"...","testCode":"..."}` (legacy fallback)
//   - the literal token `CANNOT_SYNTHESIZE` followed by a reason
func SynthesizeWithInvoker(ctx context.Context, in SynthesizeInputs, invoke Invoker) (*SynthesizeOutput, error) {
	prompt := buildSynthesizePrompt(in)
	reply, err := invoke(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("synthesize: llm call: %w", err)
	}
	cleaned := stripCodeFences(reply)
	if strings.HasPrefix(strings.TrimSpace(cleaned), "CANNOT_SYNTHESIZE") {
		// Surface as an output with empty Cases AND empty TestCode —
		// caller maps this to a special "skip" path. Preserve the
		// reason in TestCode so the agent sees it.
		return &SynthesizeOutput{TestCode: cleaned}, nil
	}
	// Try parsing the JSON envelope.
	type envelope struct {
		Cases    []TestCase `json:"cases"`
		TestCode string     `json:"testCode"`
	}
	var env envelope
	if err := json.Unmarshal([]byte(cleaned), &env); err != nil {
		// Tighter handling (2026-05-09 v7→v8): legacy fallback was
		// quietly accepting LLM output that "looked like raw test
		// code" but contained JSON syntax errors. The v7 ReadInput
		// run hit this — the fallback wrote `code.test.js` with
		// `"objectId": "..."` at line 2 and the test runner crashed
		// on `Unexpected token ':'`. Now: treat any reply that
		// starts with `{` (or has internal `:` in the first 200
		// chars) as a malformed JSON envelope and surface a hard
		// error so the caller can decide to retry rather than
		// silently shipping broken test source.
		if strings.TrimSpace(cleaned) == "" {
			return nil, fmt.Errorf("synthesize: empty response from llm")
		}
		looksLikeJSON := strings.HasPrefix(strings.TrimSpace(cleaned), "{") ||
			strings.Contains(firstN(cleaned, 200), `"objectId"`) ||
			strings.Contains(firstN(cleaned, 200), `"cases"`)
		if looksLikeJSON {
			return nil, fmt.Errorf("synthesize: malformed JSON envelope (%w) — output starts like JSON but failed to parse; retry typecalc_synthesize_tests for this object", err)
		}
		// True legacy raw test code: looks nothing like JSON envelope.
		// Accept it but flag in the output. Caller may later refuse
		// to run if the test runner can't make sense of it.
		return &SynthesizeOutput{TestCode: cleaned}, nil
	}
	if len(env.Cases) == 0 && strings.TrimSpace(env.TestCode) == "" {
		return nil, fmt.Errorf("synthesize: response has neither cases nor testCode")
	}
	return &SynthesizeOutput{Cases: env.Cases, TestCode: env.TestCode}, nil
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

const synthesizerSystemPrompt = `You generate test suites from a function's specification (intent + description + signature + port declarations). You DO NOT see the implementation source — that is intentional. Tests must verify the *contract*, not the implementation.

You output **structured test cases as JSON**, not test framework code. The host (kcpos) renders your cases into a runnable test file using a language-specific harness that handles trace logging, assertion ordering, and runner integration. Your job is purely to declare what to call and what to expect.

Output schema (strict JSON, no Markdown fences, no prose):

{
  "objectId": "<id>",
  "cases": [
    {
      "name": "human-readable case description",
      "setup": [
        { "set": "<input_port_name>", "value": <any JSON value> }
      ],
      "call": "<one JS expression that invokes the function under test. ALWAYS prefix the function name with IMPL. — the harness binds the impl module to globalThis.IMPL, and bare names like 'InitGame()' will throw ReferenceError. Examples: 'IMPL.InitGame()' or 'IMPL.movePaddle(5, paddleX)'>",
      "expect": [
        { "port": "<output_port>", "equals": <value> }
        // OR { "port": "<port>", "between": [<min>, <max>] }
        // OR { "port": "<port>", "type": "number" | "string" | "boolean" | "object" | "array" }
        // OR { "port": "<port>", "enum": [<v1>, <v2>, ...] }
        // OR { "port": "<port>", "truthy": true | false }
      ]
    }
  ]
}

Rules:

1. Use the EXACT port names from the consumes/produces/mutates blocks. Setup port names should appear in consumes; expect port names should appear in produces or mutates.
2. The "call" string is a single expression in the project's primary language. For JavaScript / HTML+script projects, write valid JS; for other languages, the harness may not be available yet — in that case fall back to the legacy raw-test-code mode by setting "testCode" instead of "cases".
2a. CRITICAL — call-expression naming: for JS/HTML, the function name in "call" MUST match the graph object id EXACTLY (same case, same spelling) and MUST be prefixed with "IMPL.". The harness binds the impl module to globalThis.IMPL and looks up IMPL.<exactObjectId>. Common failure mode: synthesizer writes "IMPL.initGame(...)" while the graph object is "InitGame" — harness throws "is not a function" and every case fails. Always copy the object id verbatim from the "Object id:" header above.
3. Cover: a typical happy-path case, boundary cases (empty, max value, edges of declared valueSpace), and any explicit invariants from the description.
4. Each "case" should test ONE specific behavior. Use multiple cases for multiple invariants — do not pile assertions for unrelated behaviors into one case.
5. The harness automatically: snapshots input ports before each call, snapshots output ports after, appends to .kcpos/typecalc-runtime/<id>.json BEFORE running assertions (so traces are captured even on assertion failure), and runs the assertions. You do NOT need to write any of that.
6. If the spec is too underspecified to derive meaningful cases, return the literal text CANNOT_SYNTHESIZE on a single line followed by a one-sentence reason. Do not return JSON in that case.

**v9.0.2 assertion-style rules (CRITICAL — these prevent brittle tests):**

7. **Only use ` + "`equals`" + ` for values that are DECLARED INVARIANTS** — named constants the contract pins (e.g. ` + "`radius=8`" + ` if the description says "radius is fixed at 8"), enum members, status strings ("playing"/"game_over"), boolean flags. NEVER use ` + "`equals`" + ` for computed/derived values.
8. **For arithmetic-derived values use ` + "`between: [lo, hi]`" + `** with a ±1% tolerance band around the expected value. Example: if the description says "paddle moves at 500 px/s for dt=0.016s" expecting `+ "`paddleX = 200 - 8 = 192`" + `, write ` + "`{between: [190, 194]}`" + ` not ` + "`{equals: 192}`" + `. This protects against floating-point precision drift across impl rewrites.
9. **For values whose specific magnitude is implementation-chosen (e.g. random starting position drawn from a range), use ` + "`between`" + ` over the FULL declared range**, not the specific sample. If description says "ball spawns at x in [8, width-8]", write ` + "`{between: [8, 792]}`" + ` for a 800px canvas — NEVER ` + "`{equals: 400}`" + ` even if a representative sample value appears in the description.
10. **For shape/structure validation use ` + "`type`" + ` not ` + "`equals: {}`" + `**: ` + "`{type: \"object\"}`" + ` / ` + "`{type: \"array\"}`" + ` / ` + "`{type: \"number\"}`" + `. ` + "`equals` on an object" + ` requires exact key-order match — gratuitously brittle.

**Why these rules**: tests must verify the *contract* (description-level invariants), not the *implementation* (specific numeric coincidences). A test that fires ` + "`equals: 192`" + ` breaks when the impl is rewritten with a different rounding order producing 192.00001 — even though the contract is satisfied. The harness supports ` + "`between` / `type` / `enum` / `truthy`" + ` precisely so synthesized tests can stay contract-anchored.

Legacy fallback (only when a harness for the language doesn't exist): instead of "cases", output:
{
  "objectId": "<id>",
  "testCode": "<raw test source in the target language, including any framework imports, runner integration, and the appendTrace helper exactly as previously documented>"
}
The host inspects the field and routes accordingly. Prefer "cases" — the harness path is more robust.`

func buildSynthesizePrompt(in SynthesizeInputs) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Object id: %s\nLanguage: %s\n\n", in.ObjectID, nonEmpty(in.Lang, "(unknown)"))
	fmt.Fprintf(&b, "## Intent (immutable user contract)\n%s\n\n", nonEmpty(in.Intent, "(empty)"))
	fmt.Fprintf(&b, "## Description (post-hoc behaviour catalogue)\n%s\n\n", nonEmpty(in.Description, "(none)"))
	if in.Signature != "" {
		fmt.Fprintf(&b, "## Signature (def file)\n```\n%s\n```\n\n", clip(in.Signature, 4000))
	}
	if len(in.Consumes) > 0 {
		fmt.Fprintf(&b, "## Consumes (input ports — use these names verbatim)\n%s\n\n", strings.Join(in.Consumes, ", "))
	}
	if len(in.Produces) > 0 {
		fmt.Fprintf(&b, "## Produces (output ports — use these names verbatim)\n%s\n\n", strings.Join(in.Produces, ", "))
	}
	if len(in.Mutates) > 0 {
		fmt.Fprintf(&b, "## Mutates (in-place ports — use these names verbatim)\n%s\n\n", strings.Join(in.Mutates, ", "))
	}
	if in.ValueSpace != "" {
		fmt.Fprintf(&b, "## valueSpace declarations\n```\n%s\n```\n\n", clip(in.ValueSpace, 2000))
	}
	if len(in.Examples) > 0 {
		b.WriteString("## Concrete examples from the def file (ground truth — use these as case seeds)\n\n")
		for i, ex := range in.Examples {
			fmt.Fprintf(&b, "Example %d:\n```\n%s\n```\n\n", i+1, clip(ex, 1000))
		}
		b.WriteString("These examples come from the def's JSDoc @example tags and are AUTHORITATIVE input/output pairs for this function. Your generated cases MUST cover at least every example here verbatim (preserve the exact inputs and assert the exact outputs). You may add ADDITIONAL boundary/edge cases on top.\n\n")
	}
	if in.PortObservation != "" {
		fmt.Fprintf(&b, "## portObservation (how to read each port at runtime)\n```\n%s\n```\n\n"+
			"This dictates the SHAPE of your `call` expression:\n"+
			"  - `\"return.X\"` ports ⇒ call must RETURN a value with field X. Write the call as a single expression that the harness wraps in `return (...)`. Use `IMPL.<fnName>(args)` to access exported functions.\n"+
			"  - `\"global\"` ports ⇒ call should MUTATE globalThis[port]. Use `IMPL.<fnName>(args)` and rely on the impl to set globals.\n"+
			"  - `\"args.<n>.<path>\"` ports ⇒ call passes a mutable object at position n; impl mutates it; the harness reads back.\n"+
			"  - `\"side_effect\"` ports ⇒ no runtime value to check; you SHOULD NOT write expectations on these ports (they exist for human/integration verification).\n\n", clip(in.PortObservation, 1500))
	}
	b.WriteString("Write the JSON cases per the system prompt's strict schema. No fences, no prose.")
	return b.String()
}

// stripCodeFences trims a leading ```<lang>\n and trailing ``` if the
// LLM ignored "no fences" instruction.
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// Cut the first line entirely (```lang).
		if idx := strings.Index(s, "\n"); idx > 0 {
			s = s[idx+1:]
		} else {
			s = strings.TrimPrefix(s, "```")
		}
	}
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	return s
}
