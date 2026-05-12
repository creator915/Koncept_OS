package router

import (
	"context"
	"fmt"
)

// LLMInvoker is the seam the router uses to talk to an LLM. Tests
// inject deterministic stubs; production wires it to internal/llm.
// One round-trip per handler invocation — prompt in, raw reply out.
type LLMInvoker func(ctx context.Context, systemPrompt, userPrompt string) (string, error)

// LLMHandler is the canonical Handler shape for steps that route via
// LLM sum-branch selection. It implements Handler by:
//
//  1. Building a user prompt from the input TypedValue (PromptBuilder).
//  2. Calling the LLM with the configured system prompt.
//  3. Parsing the reply via ParseFromLLM, constrained to the allowed
//     output sum-type branches.
//
// LLMHandler enforces the design contract that LLMs "pick branches,
// not workflows": the AllowedOutputs list is the same set the router
// validates against. An LLM that produces an off-list TYPE tag fails
// at the parser, before the router sees the result.
//
// Use LLMHandler for handlers like:
//   - generate_code (Description → Uncompiled<Code>)
//   - review_test_error (TestError → TestCorrect | TestWrong | DescriptionUnclear)
//   - debug_from_test (TestCorrect → Uncompiled<Code>)
//
// For system handlers (compile, test, checker — non-LLM), implement
// Handler directly.
type LLMHandler struct {
	// In is the input type tag this handler dispatches on.
	In string

	// AllowedOutputs lists every sum-branch the LLM may pick. Router
	// validation rejects outputs outside this set; ParseFromLLM
	// rejects LLM-side picks outside this set; together they
	// guarantee the LLM cannot escape the typed graph.
	AllowedOutputs []string

	// SystemPrompt is the LLM-side instruction context. Constant per
	// handler. Tells the LLM what types it's choosing between and the
	// branch-selection convention (`TYPE: <tag>` first line).
	SystemPrompt string

	// BuildPrompt produces the user-side prompt from the input value.
	// Implementations typically extract Content into a typed struct
	// and format the relevant fields.
	BuildPrompt func(in TypedValue) (string, error)

	// Invoke calls the LLM with system+user prompts. Tests stub this;
	// production wraps an internal/llm client.
	Invoke LLMInvoker
}

func (h *LLMHandler) Accepts() string { return h.In }

func (h *LLMHandler) Outputs() []string { return h.AllowedOutputs }

func (h *LLMHandler) Handle(ctx context.Context, in TypedValue) (TypedValue, error) {
	if h.Invoke == nil {
		return TypedValue{}, fmt.Errorf("LLMHandler %q: no Invoke registered (test wiring forgotten?)", h.In)
	}
	if h.BuildPrompt == nil {
		return TypedValue{}, fmt.Errorf("LLMHandler %q: no BuildPrompt registered", h.In)
	}
	user, err := h.BuildPrompt(in)
	if err != nil {
		return TypedValue{}, fmt.Errorf("LLMHandler %q build prompt: %w", h.In, err)
	}
	raw, err := h.Invoke(ctx, h.SystemPrompt, user)
	if err != nil {
		return TypedValue{}, fmt.Errorf("LLMHandler %q invoke: %w", h.In, err)
	}
	out, err := ParseFromLLM(raw, h.AllowedOutputs)
	if err != nil {
		return TypedValue{}, fmt.Errorf("LLMHandler %q parse: %w (raw=%q)", h.In, err, truncate(raw, 200))
	}
	// Preserve carrier metadata from the input — channel and lang
	// describe context that handlers shouldn't have to re-derive.
	out.Channel = in.Channel
	if out.Lang == "" {
		out.Lang = in.Lang
	}
	return out, nil
}
