package synthesize

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/creator915/Koncept_OS/internal/llm/provider"
	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// DescribeInputs is what an LLM needs to write a precise post-hoc
// description of a graph object's implementation.
type DescribeInputs struct {
	ObjectID  string
	Intent    string // graph.Object.Intent — for *grounding* not copying
	Signature string // contents of the def file
	Impl      string // contents of the impl file
}

// DescribeOutput pairs the prose Description (legacy v8.x output) with
// the new structured Contract clauses (Step 2 of contract landing).
//
// Description remains as the human-readable catalogue; Contract is the
// testable decomposition. Synth (Step 3) consumes Contract; gate (Step
// 4) enforces test↔clause traceability. Description stays primarily
// for human inspection + the brownfield prompts that still need prose.
//
// Contract may be nil for legacy LLM replies that don't emit the
// CONTRACT block — caller must tolerate nil and downgrade gracefully.
type DescribeOutput struct {
	Description string
	Contract    []core.ContractClause
}

// contractSentinel separates the prose description from the JSON
// contract block in the LLM reply. The LLM is instructed to emit:
//
//	<prose description paragraphs>
//
//	---CONTRACT---
//	{"clauses":[{"id":"c1","kind":"example","body":"...","source":"..."}]}
//
// Pre-sentinel content is the Description; post-sentinel content is
// parsed as JSON for Contract clauses. If the sentinel is absent, the
// reply is treated as legacy prose-only (Contract = nil).
const contractSentinel = "---CONTRACT---"

// Describe asks an LLM to generate a precise, non-evaluative description
// of an implementation. The result joins the original `intent` field as
// the second source of truth for the spec — `intent` is the contract,
// `description` is the (auto-generated) catalogue of what the code
// actually does.
//
// The system prompt is deliberately asymmetric to ReviewReasonableness:
// description should NOT editorialize about correctness — only catalogue
// behavior. Correctness judgement happens in the review step.
//
// Sub-tool LLM calls (this one, plus SynthesizeTests / ReviewReasonable-
// ness) explicitly disable thinking mode — these tasks are well-defined
// contracts that don't benefit from reasoning_content, and the latency
// difference (~30s per call) compounds badly when describe runs on every
// object.
func Describe(ctx context.Context, in DescribeInputs) (*DescribeOutput, error) {
	cfg, err := provider.ProviderFromEnv()
	if err != nil {
		return nil, fmt.Errorf("describe: load llm config: %w", err)
	}
	cfg.Thinking = false
	client := transport.NewClient(cfg)
	return DescribeWithInvoker(ctx, in, func(ctx context.Context, prompt string) (string, error) {
		msgs := []transport.Message{
			{Role: "system", Content: describerSystemPrompt},
			{Role: "user", Content: prompt},
		}
		resp, err := client.Chat(ctx, msgs, nil, transport.StreamHandler{})
		if err != nil {
			return "", err
		}
		return resp.Content, nil
	})
}

// DescribeWithInvoker is the testable core; production wires a real LLM
// client, tests can inject deterministic invokers.
func DescribeWithInvoker(ctx context.Context, in DescribeInputs, invoke Invoker) (*DescribeOutput, error) {
	prompt := buildDescribePrompt(in)
	reply, err := invoke(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("describe: llm call: %w", err)
	}
	desc, contract, perr := parseDescribeReply(reply)
	if perr != nil {
		return nil, fmt.Errorf("describe: %w", perr)
	}
	return &DescribeOutput{Description: desc, Contract: contract}, nil
}

// parseDescribeReply splits the LLM reply on contractSentinel.
//
//   - No sentinel  → entire reply is the description; Contract = nil
//     (legacy behavior, also the fallback when the LLM forgets the
//     contract block).
//   - Sentinel present → pre-sentinel is description, post-sentinel is
//     JSON `{"clauses": [...]}`. Malformed JSON returns an error so
//     the caller can retry rather than silently writing an empty
//     contract — the Step 4 gate would then reject every test for
//     "no clause to cite".
//
// Clauses with missing ID, Kind, or Body are dropped (with a synthetic
// "c<idx>" ID assigned if only the ID is missing) so a sloppy LLM
// reply still yields a usable partial contract.
func parseDescribeReply(reply string) (string, []core.ContractClause, error) {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return "", nil, fmt.Errorf("empty response from llm")
	}
	idx := strings.Index(reply, contractSentinel)
	if idx < 0 {
		// Legacy reply, no structured contract.
		return reply, nil, nil
	}
	desc := strings.TrimSpace(reply[:idx])
	tail := strings.TrimSpace(reply[idx+len(contractSentinel):])
	tail = stripCodeFences(tail)
	if tail == "" {
		return desc, nil, nil
	}
	var blob struct {
		Clauses []core.ContractClause `json:"clauses"`
	}
	if err := json.Unmarshal([]byte(tail), &blob); err != nil {
		return desc, nil, fmt.Errorf("parse contract JSON: %w (raw: %s)", err, clip(tail, 200))
	}
	clean := make([]core.ContractClause, 0, len(blob.Clauses))
	for i, c := range blob.Clauses {
		if c.Kind == "" || strings.TrimSpace(c.Body) == "" {
			continue
		}
		if c.ID == "" {
			c.ID = fmt.Sprintf("c%d", i+1)
		}
		clean = append(clean, c)
	}
	return desc, clean, nil
}

const describerSystemPrompt = `You are a precise technical writer. Given a function's signature and implementation, you produce TWO things in one reply:

1. A prose description that catalogues exactly what the code does.
2. A structured **contract** — a list of atomic, testable clauses that downstream test synthesis will use as the SOLE source for generating tests.

## Part 1 — Prose description

Strict rules:

1. Describe what the code DOES, not what it SHOULD do. Do not editorialize about correctness or fitness.
2. **Stay at the contract level — describe shapes, types, ranges, and invariants. NEVER quote a numeric magic constant from the implementation unless that constant is a declared invariant (named global, ` + "`const X = ...`" + `, comment marking it as a contract). For derived/computed values say "a sample uniformly distributed over […]", "scaled by canvas height", "clamped to bounds" — NEVER "300" or "8 + Math.random() * (width - 16)".**
3. Note side effects (mutation, I/O, randomness, time-dependence).
4. Note sequencing / temporal relationships if the code has them (frame-stepping, before/after, retry loops).
5. Do not copy the intent verbatim. Your output is the *complement* to intent — the concrete behavior that pairs with the abstract goal.
6. **Failure mode to avoid (v9.0.2): a description that quotes implementation numbers like "ball_x = 300" or "angle uniformly sampled from [π*2/18, π*7/18]" lets downstream test synthesis re-derive those exact numbers, producing brittle ` + "`equals: 300`" + ` assertions. Tests would then verify "the code did what the code did," not "the code did what the contract says." Stay at the contract level so tests stay contract-anchored.**

Plain text. 4–12 sentences. No JSON, no Markdown headers.

## Part 2 — Contract clauses

After the prose, on its OWN LINE, emit the sentinel ` + "`---CONTRACT---`" + ` then a single JSON object with this shape (no fences, no prose):

{"clauses":[
  {"id":"c1","kind":"example","body":"<one concrete I/O pair the spec pins>","source":"spec:S§N or intent or readme"},
  {"id":"c2","kind":"invariant","body":"<a structural property testable without knowing the answer>","source":"spec or intent"},
  {"id":"c3","kind":"characterization","body":"<a behavior observed via probe that we now LOCK as expected>","source":"char:probe_X"}
]}

### How to pick a clause's kind — strict criteria:

- **example** — a CONCRETE input-output pair lifted verbatim from the spec / README / intent / @example tag. Generates a deterministic test case. "fib(7) returns 13", "parse('') returns []", "Add(-1, 1) = 0".
  ❌ NOT example: "the function returns an int" (no concrete value); "the result is reasonable" (no observable check).

- **invariant** — a structural property that's testable WITHOUT knowing the full answer. Idempotence, commutativity, associativity, round-trip equivalence (parse→print→parse), conservation (sum-before == sum-after), monotonicity, valid state transitions, no-crash on declared input range. Generates property-style tests.
  ❌ NOT invariant: "returns the correct answer" (this is "specification", not invariant); "is fast" (performance, not behavior).

- **characterization** — a behavior actually observed via probe / inspection, that we now LOCK as the accepted answer. Used both in brownfield (lock legacy) and greenfield (lock the design's first accepted behavior). MUST cite a source like "char:probe_N" or "char:run_local_N" so audit can re-derive.
  ❌ NOT characterization: a guess based on reading the impl; a behavior that hasn't been concretely observed.

### Quantity guidance:

- Aim for 3–10 clauses per object. ≤2 is suspicious unless the function is genuinely trivial; ≥15 means the object is too coarse (split in graph_declare).
- Cover the spec's MUST-haves first; characterization clauses are added later by the agent via the characterize tool (Step 5 wires this — for now, only emit characterization clauses if you can cite a probe call from the impl-reading context).
- Mark clauses ` + "`\"optional\":true`" + ` if they're listed for context but the gate shouldn't fail on missing coverage (e.g. "the function also accepts an X but the spec marks X handling as best-effort").

### Bad clause examples (don't do this):

- ❌ ` + "`{\"kind\":\"example\",\"body\":\"the function adds numbers\"}`" + ` — no concrete I/O pair
- ❌ ` + "`{\"kind\":\"invariant\",\"body\":\"the function works correctly\"}`" + ` — not testable
- ❌ ` + "`{\"kind\":\"example\",\"body\":\"Add(MAX_INT,1) panics\"}`" + ` without ` + "`\"source\":\"char:...\"`" + ` — if observed, mark characterization; if pinned by spec, source must say so
- ❌ ` + "`{\"kind\":\"characterization\",\"body\":\"the impl uses base-10\",\"source\":\"reading the code\"}`" + ` — characterization requires probe-level observation, not code reading

### When uncertain:

If you genuinely can't extract enough clauses (e.g. the function is too abstract to derive concrete examples), emit fewer rather than fabricating. The empty array is allowed: ` + "`{\"clauses\":[]}`" + `. The gate will then refuse confirm with "no contract clauses — re-describe with concrete clauses or split the object". This is the correct outcome — better than fake clauses leading to vacuous tests.`

func buildDescribePrompt(in DescribeInputs) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Object id: %s\n\n", in.ObjectID)
	fmt.Fprintf(&b, "## Original intent (for grounding only — do NOT copy)\n%s\n\n", nonEmpty(in.Intent, "(empty)"))
	if in.Signature != "" {
		fmt.Fprintf(&b, "## Signature\n```\n%s\n```\n\n", clip(in.Signature, 4000))
	}
	if in.Impl != "" {
		fmt.Fprintf(&b, "## Implementation\n```\n%s\n```\n\n", clip(in.Impl, 12000))
	}
	b.WriteString("Write the description, then the ---CONTRACT--- sentinel, then the contract JSON.")
	return b.String()
}
