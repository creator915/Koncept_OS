package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
	"github.com/creator915/Koncept_OS/internal/tools"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// defaultMaxIterations: 150 — v7 pong used 112/120 (93%) and a single
// extra obstacle would have run out. With v8 routing HTML directly
// through the harness (eliminating the dual-impl rewrite phase) we'd
// expect runs to drop, but the safer move is a wider buffer: D4 still
// caps per-object retries at CycleCap=5, so a 150 turn budget can't
// be silently spent grinding on one stuck object — it can only be
// spent on multi-object surface area.
const defaultMaxIterations = 150

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
	Caps core.CapSet
}

// RunTurn executes one user turn — append the prompt, drive the agent loop
// until the model produces a turn with no tool_calls (or the iteration cap
// is reached). Uses defaults; for subagent calls or other customization see
// RunTurnOpts.
func RunTurn(ctx context.Context, client *transport.Client, messages *[]transport.Message, userPrompt string) error {
	return RunTurnOpts(ctx, client, messages, userPrompt, RunOptions{})
}

// RunTurnOpts is the workhorse. RunTurn is a thin wrapper.
func RunTurnOpts(ctx context.Context, client *transport.Client, messages *[]transport.Message, userPrompt string, opts RunOptions) error {
	if !opts.SkipSystem {
		ensureSystem(messages)
	}
	*messages = append(*messages, transport.Message{Role: "user", Content: userPrompt})

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
		handler := transport.StreamHandler{
			OnReasoning: func(s string) {
				if !reasoningStarted {
					fmt.Fprintf(os.Stderr, "%s%s\x1b[2m[thinking]\n", Stamp(), opts.Indent)
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
		// Provider degenerate response: empty content + empty reasoning
		// + no tool_calls is not "task complete" — it's an upstream
		// glitch (observed with DeepSeek occasionally). Returning nil
		// here would silently terminate mid-workflow. Instead, drop the
		// empty turn from history, log, and retry.
		if len(assistant.ToolCalls) == 0 && assistant.Content == "" && assistant.ReasoningContent == "" {
			fmt.Fprintf(os.Stderr, "%s%s\x1b[33m⚠ provider returned an empty turn (no content, no tool_calls); retrying\x1b[0m\n", Stamp(), opts.Indent)
			continue
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

		// Dispatcher: process tool_calls preserving emit order, but
		// batch CONSECUTIVE Concurrent=true tools into goroutines.
		// Non-concurrent tools (graph_*, write_file, session_*, etc.)
		// execute one at a time.
		//
		// Why "consecutive only": we MUST NOT reorder operations. A
		// turn like [graph_merge, describe, graph_merge] keeps the
		// merges in original order with describe between them; not
		// all-merges-first or all-describes-first. We run merge-1,
		// then describe alone (1-element batch), then merge-2.
		//
		// Why the "distinct (name+target)" guard: two parallel calls to
		// the same tool with the same object_id race even when the tool
		// is Concurrent. We batch only when (name + object_id) is
		// distinct across the batch.
		turnResults := make([]string, len(assistant.ToolCalls))
		idx := 0
		for idx < len(assistant.ToolCalls) {
			if isConcurrent(builtins, assistant.ToolCalls[idx].Function.Name) {
				end := idx + 1
				seen := map[string]bool{batchKey(assistant.ToolCalls[idx]): true}
				for end < len(assistant.ToolCalls) {
					tc := assistant.ToolCalls[end]
					if !isConcurrent(builtins, tc.Function.Name) {
						break
					}
					k := batchKey(tc)
					if seen[k] {
						break
					}
					seen[k] = true
					end++
				}
				if end-idx > 1 {
					runBatchConcurrent(ctx, opts, builtins, assistant.ToolCalls[idx:end], turnResults[idx:end])
				} else {
					turnResults[idx] = runOneToolCall(ctx, opts, builtins, assistant.ToolCalls[idx])
				}
				idx = end
			} else {
				turnResults[idx] = runOneToolCall(ctx, opts, builtins, assistant.ToolCalls[idx])
				idx++
			}
		}
		for k, tc := range assistant.ToolCalls {
			*messages = append(*messages, transport.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    turnResults[k],
			})
			calls = append(calls, turnCall{tc.Function.Name, tc.Function.Arguments, turnResults[k]})
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
					fmt.Fprintf(os.Stderr, "%s%s\x1b[33m⚠ %s\x1b[0m\n", Stamp(), opts.Indent, truncate(v, 160))
				}
				*messages = append(*messages, transport.Message{
					Role:    "user",
					Content: FormatViolations(violations),
				})
			}
		}
	}
	return fmt.Errorf("agent exceeded max iterations (%d)", maxIters)
}

// runOneToolCall executes a single tool call (sequential path) — emits
// the » banner, runs permission gate, then dispatches.
func runOneToolCall(ctx context.Context, opts RunOptions, builtins map[string]toolcall.Tool, tc transport.ToolCall) string {
	fmt.Fprintf(os.Stderr, "%s%s» %s(%s)\n", Stamp(), opts.Indent, tc.Function.Name, truncate(tc.Function.Arguments, 200))
	if denied := authorizeToolCall(opts.Caps, tc.Function.Name, tc.Function.Arguments); denied != nil {
		fmt.Fprintf(os.Stderr, "%s%s\x1b[31m✗ permission denied\x1b[0m\n", Stamp(), opts.Indent)
		return renderPermissionDenied(denied, tc.Function.Name)
	}
	return tools.Execute(ctx, builtins, tc.Function.Name, tc.Function.Arguments)
}

// runBatchConcurrent runs the batch in parallel goroutines and writes
// each result back to its slot in `out` (which the caller has already
// sized to len(batch)). Output banners interleave — that's intentional
// and the timestamp prefix keeps the log readable.
func runBatchConcurrent(ctx context.Context, opts RunOptions, builtins map[string]toolcall.Tool, batch []transport.ToolCall, out []string) {
	fmt.Fprintf(os.Stderr, "%s%s\x1b[2m┄ parallel batch (%d) ┄\x1b[0m\n", Stamp(), opts.Indent, len(batch))
	var wg sync.WaitGroup
	for i := range batch {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = runOneToolCall(ctx, opts, builtins, batch[i])
		}()
	}
	wg.Wait()
}

// isConcurrent looks up Tool.Concurrent for tc.Name. Unknown tools
// default to false (safe).
func isConcurrent(builtins map[string]toolcall.Tool, name string) bool {
	t, ok := builtins[name]
	if !ok {
		return false
	}
	return t.Concurrent
}

// batchKey identifies a tool_call within a batch for distinct-target
// dedup. We use (name, object_id-or-path-or-id) — most concurrent tools
// take an `object_id`, but a few (read_file/grep/glob) take `path` or
// `pattern` which serves the same role. Falling back to the full
// argument JSON guarantees uniqueness when nothing else applies.
func batchKey(tc transport.ToolCall) string {
	var args map[string]interface{}
	_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
	for _, key := range []string{"object_id", "id", "path", "pattern"} {
		if v, ok := args[key].(string); ok && v != "" {
			return tc.Function.Name + ":" + key + "=" + v
		}
	}
	return tc.Function.Name + ":" + tc.Function.Arguments
}

func ensureSystem(messages *[]transport.Message) {
	if len(*messages) == 0 || (*messages)[0].Role != "system" {
		*messages = append([]transport.Message{{Role: "system", Content: SystemPrompt}}, (*messages)...)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
