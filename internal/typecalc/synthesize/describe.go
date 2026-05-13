package synthesize

import (
	"context"
	"fmt"
	"strings"

	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/llm/provider"
)

// DescribeInputs is what an LLM needs to write a precise post-hoc
// description of a graph object's implementation.
type DescribeInputs struct {
	ObjectID  string
	Intent    string // graph.Object.Intent — for *grounding* not copying
	Signature string // contents of the def file
	Impl      string // contents of the impl file
}

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
func Describe(ctx context.Context, in DescribeInputs) (string, error) {
	cfg, err := provider.ProviderFromEnv()
	if err != nil {
		return "", fmt.Errorf("describe: load llm config: %w", err)
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
func DescribeWithInvoker(ctx context.Context, in DescribeInputs, invoke Invoker) (string, error) {
	prompt := buildDescribePrompt(in)
	reply, err := invoke(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("describe: llm call: %w", err)
	}
	desc := strings.TrimSpace(reply)
	if desc == "" {
		return "", fmt.Errorf("describe: empty response from llm")
	}
	return desc, nil
}

const describerSystemPrompt = `You are a precise technical writer. Given a function's signature and implementation, you produce a description that catalogues exactly what the code does — inputs consumed, outputs produced, side effects, sequencing, invariants.

Strict rules:

1. Describe what the code DOES, not what it SHOULD do. Do not editorialize about correctness or fitness.
2. **Stay at the contract level — describe shapes, types, ranges, and invariants. NEVER quote a numeric magic constant from the implementation unless that constant is a declared invariant (named global, ` + "`const X = ...`" + `, comment marking it as a contract). For derived/computed values say "a sample uniformly distributed over […]", "scaled by canvas height", "clamped to bounds" — NEVER "300" or "8 + Math.random() * (width - 16)".**
3. Note side effects (mutation, I/O, randomness, time-dependence).
4. Note sequencing / temporal relationships if the code has them (frame-stepping, before/after, retry loops).
5. Do not copy the intent verbatim. Your output is the *complement* to intent — the concrete behavior that pairs with the abstract goal.
6. **Failure mode to avoid (v9.0.2): a description that quotes implementation numbers like "ball_x = 300" or "angle uniformly sampled from [π*2/18, π*7/18]" lets downstream test synthesis re-derive those exact numbers, producing brittle ` + "`equals: 300`" + ` assertions. Tests would then verify "the code did what the code did," not "the code did what the contract says." Stay at the contract level so tests stay contract-anchored.**

Output is plain text. Aim for 4–12 sentences. No JSON, no Markdown headers — just a focused descriptive paragraph (or two).`

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
	b.WriteString("Write the description.")
	return b.String()
}
