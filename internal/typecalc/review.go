package typecalc

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/creator915/Koncept_OS/internal/llm"
)

// ReviewInputs bundles everything the reasonableness reviewer needs.
// Fields are passed flat (rather than reading off disk inside the
// reviewer) so callers can run the review against synthetic inputs in
// tests without touching the project tree.
type ReviewInputs struct {
	ObjectID    string
	Intent      string // graph.Object.Intent — the immutable user contract
	Description string // auto-generated from impl + signature; may be empty
	Signature   string // contents of the def file
	Impl        string // contents of the impl file
	TestCode    string // last test suite that ran
	TestLog     string // raw output of the test runner
}

// ReviewReasonableness asks an LLM whether the implementation, as
// witnessed by intent + description + signature + tests, is broadly
// reasonable for the stated need. Returns a structured verdict.
//
// This is the user's design proposal's "standard 2": no absolute
// right/wrong — only "broadly does it make sense?" The LLM acts as a
// design reviewer, not a unit tester.
//
// Cost: one chat-completion round-trip. The LLM is constructed via
// llm.ProviderFromEnv so the same provider/key the agent uses is reused.
// In test environments without an API key, a stub reviewer
// (see ReviewWithInvoker) lets callers inject deterministic verdicts.
func ReviewReasonableness(ctx context.Context, in ReviewInputs) (ReviewVerdict, error) {
	cfg, err := llm.ProviderFromEnv()
	if err != nil {
		return ReviewVerdict{}, fmt.Errorf("review: load llm config: %w", err)
	}
	cfg.Thinking = false // sub-tool: structured verdict, no thinking
	client := llm.NewClient(cfg)
	return ReviewWithInvoker(ctx, in, func(ctx context.Context, prompt string) (string, error) {
		msgs := []llm.Message{
			{Role: "system", Content: reviewerSystemPrompt},
			{Role: "user", Content: prompt},
		}
		resp, err := client.Chat(ctx, msgs, nil, llm.StreamHandler{})
		if err != nil {
			return "", err
		}
		return resp.Content, nil
	})
}

// Invoker is the small seam used by ReviewWithInvoker for testability:
// take a prompt, return the assistant's text reply (whatever the LLM
// produces).
type Invoker func(ctx context.Context, prompt string) (string, error)

// ReviewWithInvoker is the testable core. ReviewReasonableness is a
// thin wrapper that injects a real LLM client; tests inject a stub.
func ReviewWithInvoker(ctx context.Context, in ReviewInputs, invoke Invoker) (ReviewVerdict, error) {
	prompt := buildReviewPrompt(in)
	reply, err := invoke(ctx, prompt)
	if err != nil {
		return ReviewVerdict{}, fmt.Errorf("review: llm call: %w", err)
	}
	verdict, perr := parseReviewReply(reply)
	if perr != nil {
		// Don't drop the model's reply — fold it into the error so the
		// agent can decide whether to retry or give up.
		return ReviewVerdict{}, fmt.Errorf("review: parse reply: %w (raw: %s)", perr, truncate(reply, 500))
	}
	if verdict.Verdict != "pass" && verdict.Verdict != "fail" {
		return ReviewVerdict{}, fmt.Errorf("review: invalid verdict %q (must be pass | fail)", verdict.Verdict)
	}
	return verdict, nil
}

const reviewerSystemPrompt = `You are a senior code reviewer judging ONE narrow question:

  "Does the implementation, read against the stated design intent, do
   what the intent describes?"

You judge SEMANTIC FIT only. You do NOT judge:
- whether the test harness can call the function (that's the test runner's job; its output is given to you only as context)
- whether the file is an ES module, importable, exported, etc. (those are mechanical facts handled by the harness; do not speculate)
- whether the test cases themselves are well-designed
- whether tests are passing or failing (the harness reports that; your job is the orthogonal question of whether the implementation is the RIGHT implementation for the intent)

If the test runner reports failures BUT the implementation clearly satisfies the intent, your verdict is "pass" — explain in reasons that the failures are test-side concerns and the impl is semantically correct. The agent will route mismatches between test-harness signal and review verdict through the obstacle/waiver path; that's not your concern.

Your verdict has only two values:
- "pass" — the implementation broadly accomplishes the intent.
- "fail" — the implementation contradicts the intent, omits a load-bearing requirement from the intent, or solves a different problem than the intent describes.

NEVER use phrases like "not a module", "cannot be imported", "not exported", "test harness cannot", "browser environment required", "DOM not available". Those are mechanical observations belonging to the test runner, not semantic judgments. If you find yourself reaching for such reasons, your verdict is wrong — the intent doesn't ask for export-ability, it asks for behavior, and you can read behavior straight from the implementation source.

Reply with strict JSON only. No prose before or after. Schema:

{
  "verdict": "pass" | "fail",
  "reasons": ["one short sentence about semantic fit to intent", "..."],
  "confidence": 0.0..1.0
}

reasons MUST be 1–4 items, each ≤ 120 characters, plain English, each grounded in a specific clause of the intent (or a specific line of the impl). confidence is your self-stated certainty; use 0.5 when the intent itself is ambiguous.`

func buildReviewPrompt(in ReviewInputs) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Object id: %s\n\n", in.ObjectID)
	fmt.Fprintf(&b, "## Original design intent (immutable user contract — JUDGE THE IMPL AGAINST THIS)\n%s\n\n", nonEmpty(in.Intent, "(empty)"))
	fmt.Fprintf(&b, "## Auto-generated description (post-hoc, what the impl actually does)\n%s\n\n", nonEmpty(in.Description, "(none yet)"))
	if in.Signature != "" {
		fmt.Fprintf(&b, "## Signature (def file)\n```\n%s\n```\n\n", clip(in.Signature, 4000))
	}
	if in.Impl != "" {
		fmt.Fprintf(&b, "## Implementation (impl file — read this to judge semantic fit)\n```\n%s\n```\n\n", clip(in.Impl, 8000))
	}
	if in.TestCode != "" {
		fmt.Fprintf(&b, "## Test cases (advisory only — they may or may not match what the intent really requires)\n```\n%s\n```\n\n", clip(in.TestCode, 4000))
	}
	if in.TestLog != "" {
		fmt.Fprintf(&b, "## Test runner output (advisory only — failures here do NOT necessarily mean the impl is wrong; they may mean the test was wrong, or the harness can't model this object)\n```\n%s\n```\n\n", clip(in.TestLog, 4000))
	}
	b.WriteString("Now judge: does the IMPL satisfy the INTENT? Answer in strict JSON only.")
	return b.String()
}

// jsonObjectPattern picks the first {...} JSON object out of the model's
// reply. Lenient: tolerates Markdown fences (```json ... ```) and minor
// trailing chatter. We don't try to handle nested objects fully — the
// schema is shallow (one level) so a greedy outer match works.
var jsonObjectPattern = regexp.MustCompile(`(?s)\{.*\}`)

func parseReviewReply(reply string) (ReviewVerdict, error) {
	reply = strings.TrimSpace(reply)
	// Strip ```json fences if present.
	if strings.HasPrefix(reply, "```") {
		if idx := strings.Index(reply, "\n"); idx > 0 {
			reply = reply[idx+1:]
		}
		reply = strings.TrimSuffix(reply, "```")
		reply = strings.TrimSpace(reply)
	}
	match := jsonObjectPattern.FindString(reply)
	if match == "" {
		return ReviewVerdict{}, fmt.Errorf("no JSON object found in reply")
	}
	var v ReviewVerdict
	if err := json.Unmarshal([]byte(match), &v); err != nil {
		return ReviewVerdict{}, err
	}
	return v, nil
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n... [truncated]"
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
