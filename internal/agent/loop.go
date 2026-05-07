package agent

import (
	"context"
	"fmt"
	"os"

	"github.com/creator915/Koncept_OS/internal/llm"
	"github.com/creator915/Koncept_OS/internal/tools"
	"github.com/creator915/Koncept_OS/internal/typecalc"
)

// defaultMaxIterations: 60 was chosen empirically — a non-trivial web app
// (pong / tic-tac-toe / counter) takes ~30–45 turns to design, implement,
// and finalize through the kcpos workflow when subagents are NOT used. 25
// (the prior default) routinely ran out mid-finalization, leaving objects
// stuck in declared/implementing while index.html was already written.
// Top-level callers can still tune via RunOptions.MaxIterations.
const defaultMaxIterations = 60

// RunOptions tunes agent execution. The zero value is valid: it uses the
// default tool set, default spec hooks, no indentation, the default
// iteration cap, and adds the system prompt automatically.
type RunOptions struct {
	// Tools, if non-nil, overrides the default tools.Builtins(). Used by
	// subagents to inject a depth-aware spawn_subagent variant.
	Tools map[string]tools.Tool

	// Indent prefixes every status line emitted to stderr (banners, [thinking]
	// markers, » tool-call markers). Lets nested subagent output be visually
	// distinguished from parent output. Streaming content/reasoning bytes are
	// NOT line-rewritten — too fiddly with partial chunks; the prefix on
	// markers is enough to read the output as nested.
	Indent string

	// MaxIterations caps the number of LLM round-trips. 0 = use the default
	// (25). Subagents may set their own.
	MaxIterations int

	// SkipSystem, when true, suppresses the auto-inserted system prompt.
	// The caller is then responsible for putting a system message at index 0
	// (e.g. a subagent uses its own focused system message).
	SkipSystem bool

	// Hooks override the spec-enforcement post-tool-call audits. Nil = use
	// DefaultHooks() (the hardcoded built-in set). Pass an empty slice to
	// disable enforcement entirely (debugging only — not recommended).
	Hooks []SpecHook

	// HooksOptOut, if true, completely disables hook execution. Distinguished
	// from Hooks: nil so users can detect the "I want defaults" intent.
	HooksOptOut bool

	// Caps is the §6 capability set scoping every tool call. nil/empty
	// disables the gate entirely (top-level user-facing agent default —
	// the user is implicitly trusted with everything). Sub-agents that
	// opt into capability scoping pass a CapSet here; every tool call
	// is then authorized against this set before execution, and a denial
	// becomes a PermissionDenied tool result the model can react to.
	Caps typecalc.CapSet
}

// RunTurn executes one user turn — append the prompt, drive the agent loop
// until the model produces a turn with no tool_calls (or the iteration cap
// is reached). Uses defaults; for subagent calls or other customization see
// RunTurnOpts.
func RunTurn(ctx context.Context, client *llm.Client, messages *[]llm.Message, userPrompt string) error {
	return RunTurnOpts(ctx, client, messages, userPrompt, RunOptions{})
}

// RunTurnOpts is the workhorse. RunTurn is a thin wrapper.
func RunTurnOpts(ctx context.Context, client *llm.Client, messages *[]llm.Message, userPrompt string, opts RunOptions) error {
	if !opts.SkipSystem {
		ensureSystem(messages)
	}
	*messages = append(*messages, llm.Message{Role: "user", Content: userPrompt})

	builtins := opts.Tools
	if builtins == nil {
		// Top-level callers (RunTurn / RunTurnOpts with empty Tools) get the
		// full set including spawn_subagent at depth 0. Subagents bypass
		// this fallback because their parent already injected a depth+1
		// tools map via opts.Tools.
		runner := NewSubAgentRunner(client, 0)
		builtins = tools.BuiltinsWithSubAgent(runner)
	}
	specs := tools.Specs(builtins)

	maxIters := opts.MaxIterations
	if maxIters <= 0 {
		maxIters = defaultMaxIterations
	}

	hooks := opts.Hooks
	if hooks == nil && !opts.HooksOptOut {
		hooks = DefaultHooks()
	}

	for i := 0; i < maxIters; i++ {
		var (
			reasoningStarted bool
			contentStarted   bool
		)
		handler := llm.StreamHandler{
			OnReasoning: func(s string) {
				if !reasoningStarted {
					fmt.Fprintf(os.Stderr, "%s\x1b[2m[thinking]\n", opts.Indent)
					reasoningStarted = true
				}
				fmt.Fprint(os.Stderr, s)
			},
			OnContent: func(s string) {
				if reasoningStarted && !contentStarted {
					fmt.Fprint(os.Stderr, "\x1b[0m\n")
				}
				contentStarted = true
				fmt.Print(s)
			},
		}

		assistant, err := client.Chat(ctx, *messages, specs, handler)
		if reasoningStarted && !contentStarted {
			fmt.Fprint(os.Stderr, "\x1b[0m\n")
		}
		if err != nil {
			fmt.Println()
			return err
		}
		if contentStarted {
			fmt.Println()
		}
		*messages = append(*messages, *assistant)

		if len(assistant.ToolCalls) == 0 {
			return nil
		}

		// Track this turn's calls for post-execution hook auditing. Hooks
		// run AFTER all tool calls in the turn complete, so parallel calls
		// (e.g. graph_create_object + write_file<def> in one turn) can
		// satisfy each other's preconditions before the audit runs.
		type turnCall struct {
			name, args, result string
		}
		var calls []turnCall

		for _, tc := range assistant.ToolCalls {
			fmt.Fprintf(os.Stderr, "%s» %s(%s)\n", opts.Indent, tc.Function.Name, truncate(tc.Function.Arguments, 200))
			var result string
			if denied := authorizeToolCall(opts.Caps, tc.Function.Name, tc.Function.Arguments); denied != nil {
				// §6.2 permission_gate: refuse before execute, surface as
				// PermissionDenied so the model can react.
				result = renderPermissionDenied(denied, tc.Function.Name)
				fmt.Fprintf(os.Stderr, "%s\x1b[31m✗ permission denied\x1b[0m\n", opts.Indent)
			} else {
				result = tools.Execute(ctx, builtins, tc.Function.Name, tc.Function.Arguments)
			}
			*messages = append(*messages, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    result,
			})
			calls = append(calls, turnCall{tc.Function.Name, tc.Function.Arguments, result})
		}

		// Run spec-compliance hooks against everything that happened this turn.
		if len(hooks) > 0 && len(calls) > 0 {
			var violations []string
			seen := map[string]bool{}
			for _, c := range calls {
				for _, h := range hooks {
					if v := h.After(c.name, c.args, c.result); v != "" && !seen[v] {
						seen[v] = true
						violations = append(violations, v)
					}
				}
			}
			if len(violations) > 0 {
				for _, v := range violations {
					fmt.Fprintf(os.Stderr, "%s\x1b[33m⚠ %s\x1b[0m\n", opts.Indent, truncate(v, 160))
				}
				*messages = append(*messages, llm.Message{
					Role:    "user",
					Content: FormatViolations(violations),
				})
			}
		}
	}
	return fmt.Errorf("agent exceeded max iterations (%d)", maxIters)
}

func ensureSystem(messages *[]llm.Message) {
	if len(*messages) == 0 || (*messages)[0].Role != "system" {
		*messages = append([]llm.Message{{Role: "system", Content: SystemPrompt}}, (*messages)...)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
