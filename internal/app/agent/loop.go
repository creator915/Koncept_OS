package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/shared/agentctx"
	"github.com/creator915/Koncept_OS/internal/snapshot"
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

	// Depth is the agent's nesting level. 0 = top-level user-facing
	// (main conversation); 1, 2, ... = subagents at increasing depth.
	// Threaded into ctx so tool Run functions can gate on "main
	// conversation only" rules — e.g. the v9.6 dispatch-mode hardening
	// that blocks the main conversation from writing impl files once
	// the graph has ≥5 objects (forces it to spawn subagents instead).
	Depth int

	// OnProgress, if set, is invoked at the end of every completed loop
	// iteration (after that turn's assistant message + tool results +
	// any hook-violation messages have been appended to *messages).
	// Callers use it to durably persist the transcript per turn, so a
	// SIGTERM/timeout kill mid-run still leaves the last completed turn
	// on disk (pre-fix: the one-shot path only saved AFTER the whole
	// loop returned, so timed-out runs produced no transcript at all).
	// Runs synchronously in the loop goroutine — no data race on
	// *messages.
	OnProgress func()
}

// Depth plumbing has moved to internal/shared/agentctx so tool packages
// can read it without an import cycle (agent → tools, not the other
// way). See agentctx.Depth() for the reader API.

// RunTurn executes one user turn — append the prompt, drive the agent loop
// until the model produces a turn with no tool_calls (or the iteration cap
// is reached). Uses defaults; for subagent calls or other customization see
// RunTurnOpts.
func RunTurn(ctx context.Context, client *transport.Client, messages *[]transport.Message, userPrompt string) error {
	return RunTurnOpts(ctx, client, messages, userPrompt, RunOptions{})
}

// RunTurnOpts is the workhorse. RunTurn is a thin wrapper.
func RunTurnOpts(ctx context.Context, client *transport.Client, messages *[]transport.Message, userPrompt string, opts RunOptions) error {
	// Thread Depth onto ctx so every tool Run downstream can branch on
	// "is this the main conversation?". Subagents at depth>=1 are
	// permitted to write impl files; the main conversation is not (once
	// the graph crosses a size threshold) — see write_file v9.6 guard.
	ctx = agentctx.WithDepth(ctx, opts.Depth)
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
	// Capability non-exposure (forensic §7.2): if a contract is active,
	// drop every run_tool:<name> the CapSet does not authorize BEFORE
	// computing specs, so the model never even sees `bash` (or any other
	// non-permitted exec tool) in its advertised tool list. This is
	// strictly stronger than the per-call authorizeToolCall gate (which
	// stays as defense-in-depth): a tool the model can't see is one it
	// can't try to smuggle a curl/git/docker through.
	builtins = filterToolsByCaps(builtins, opts.Caps)
	specs := tools.Specs(builtins)

	maxIters := opts.MaxIterations
	if maxIters <= 0 {
		maxIters = defaultMaxIterations
	}

	hooks := opts.Hooks
	if hooks == nil && !opts.HooksOptOut {
		hooks = DefaultHooks()
	}

	// ③ process-justice termination gate state (流程正义). Counts how
	// many times we refused to let a harnessed run finish without an
	// evidence-derived Confirmed deliverable; bounded by
	// maxConfirmGateNudges → explicit GateFailed.
	confirmNudges := 0

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

		// Snapshot hook (Phase 2b): if a Snapshotter is attached to
		// the ctx, record this assistant turn as an llm.turn event.
		// SLIM payload — only the NEW info (assistant's reasoning +
		// content + tool_calls). Full chat history stays in the
		// transcripts/ file; the event chain captures the per-turn
		// delta. Capture() logs to stderr on failure rather than
		// silently swallowing, so snapshotting can't degrade unseen.
		if s := snapshot.FromContext(ctx); s != nil {
			toolCallsJSON, _ := json.Marshal(assistant.ToolCalls)
			s.Capture("llm.turn", snapshot.EventTypeLLMTurn, snapshot.LLMTurnEvent{
				SubAgent:  fmt.Sprintf("depth-%d", opts.Depth),
				TurnIndex: i,
				Reasoning: assistant.ReasoningContent,
				Content:   assistant.Content,
				ToolCalls: toolCallsJSON,
			})
		}

		if len(assistant.ToolCalls) == 0 {
			// ③ PROCESS-JUSTICE TERMINATION GATE (流程正义). A harnessed
			// run (Caps != nil ⇒ scored/task run, not casual interactive
			// chat) may not end "successfully" without an evidence-derived
			// Confirmed deliverable. Given ①②, a confirmed object can only
			// exist because the confirm_object chain's behavioral-
			// equivalence oracle passed — never because the model declared
			// done. Bounded: nudge then explicit GateFailed (never silent
			// success, never infinite spin).
			if opts.Caps != nil && !hasConfirmedDeliverable() {
				if confirmNudges >= maxConfirmGateNudges {
					return fmt.Errorf(
						"process-justice (流程正义): harnessed run ended with NO confirmed deliverable after %d nudges — GateFailed. "+
							"status=confirmed is conferred ONLY by the confirm_object chain after the mechanical behavioral-equivalence oracle "+
							"(./executable == ./probe over a gate-generated battery) passes. The run did not produce one.",
						confirmNudges)
				}
				confirmNudges++
				*messages = append(*messages, transport.Message{
					Role: "user",
					Content: "STOP — you attempted to finish, but NO graph object has reached status=confirmed. " +
						"Per process-justice (流程正义) a harnessed run cannot end without an evidence-derived Confirmed deliverable. " +
						"status=confirmed is NOT hand-settable (graph_merge_object will refuse it); it is conferred ONLY by the " +
						"confirm_object verification chain AFTER its mechanical behavioral-equivalence oracle (your ./executable vs ./probe " +
						"over a gate-generated, non-model-controlled battery) passes. Action: ensure a graph object owns your deliverable's " +
						"impl (graph_create_object + graph_merge_object impl=<path>), then call confirm_object(object_id=<id>) and resolve " +
						"any Obstacle it reports (a mismatch means your reconstruction differs from the reference — fix the code). " +
						"Finishing without a Confirmed object will be refused again.",
				})
				continue
			}
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
		// Durable per-turn persistence: a timeout/SIGTERM kill mid-run
		// still leaves the last completed turn on disk.
		if opts.OnProgress != nil {
			opts.OnProgress()
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
		out := renderPermissionDenied(denied, tc.Function.Name)
		// Snapshot hook (Phase 2c): permission-denied is a real
		// observable event — agent saw it, may strategise around it,
		// and replay needs to know it happened. Captured with Err
		// set so forensic queries can filter for denials. No
		// side_effects — a denied call by definition didn't run.
		if s := snapshot.FromContext(ctx); s != nil {
			s.Capture("tool.exec", snapshot.EventTypeToolExec, snapshot.ToolExecEvent{
				Tool:   tc.Function.Name,
				Args:   json.RawMessage(tc.Function.Arguments),
				Result: out,
				Err:    "permission denied",
			})
		}
		return out
	}
	// Phase 3 — workdir delta side_effects.
	// Take a pre-call snapshot when snapshotting is on; after the
	// tool runs, diff it against a post-call snapshot to derive
	// FileWrite / FileDelete side_effects. This is the agnostic
	// alternative to per-tool side_effect emission: any state
	// mutation that touches the watched roots gets captured
	// regardless of which tool produced it. Read-only tools
	// (probe / read_file / grep / markdown_*) produce empty diffs
	// and emit a SideEffects=nil ToolExecEvent — preserving the
	// observation in the chain without inflating storage.
	//
	// CONCURRENCY CAVEAT: runBatchConcurrent fans out
	// Concurrent=true tools into parallel goroutines, each of which
	// runs this hook. If two concurrent tools both WRITE to the
	// same file, each goroutine's post-snapshot reflects the LAST
	// writer's content — the earlier goroutine's side_effect would
	// be wrong (records the final state instead of its own delta).
	// In current code Concurrent=true tools are read-only
	// (read_file, grep, glob, list_dir, markdown_*, probe), so this
	// race is theoretical. If future tooling marks any state-
	// mutating tool as concurrent, this hook MUST be moved out of
	// the goroutine into a serial post-batch capture, OR per-file
	// locking added.
	var preWorkdir *snapshot.WorkdirSnapshot
	if s := snapshot.FromContext(ctx); s != nil {
		preWorkdir, _ = s.TakeWorkdir()
	}
	result := tools.Execute(ctx, builtins, tc.Function.Name, tc.Function.Arguments)
	if s := snapshot.FromContext(ctx); s != nil {
		var sideEffects []snapshot.SideEffect
		if preWorkdir != nil {
			postWorkdir, err := s.TakeWorkdir()
			if err == nil {
				diff := preWorkdir.Diff(postWorkdir)
				sideEffects, _ = s.DiffToSideEffects(diff)
			}
		}
		s.Capture("tool.exec", snapshot.EventTypeToolExec, snapshot.ToolExecEvent{
			Tool:        tc.Function.Name,
			Args:        json.RawMessage(tc.Function.Arguments),
			Result:      result,
			SideEffects: sideEffects,
		})
	}
	return result
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

// filterToolsByCaps removes every run_tool:<name> the active CapSet does
// not authorize, so disallowed exec tools (notably `bash`) are not even
// advertised to the model. Empty caps = no contract = unchanged behavior
// (returns the map as-is). Path-verb tools (read_file/write_file/edit)
// and spawn_agent are kept here because their authorization depends on
// the per-call argument (path/role), which is unknown at advertise time;
// they remain constrained by the per-call authorizeToolCall gate.
//
// This is the capability-non-exposure layer of the forensic §7.2 fix.
// It is intentionally conservative: it only drops tools whose capability
// (verb,arg) is fully determined by the tool name (the run_tool class),
// so it can never false-drop a legitimately-needed path tool.
func filterToolsByCaps(builtins map[string]toolcall.Tool, caps core.CapSet) map[string]toolcall.Tool {
	if len(caps) == 0 {
		return builtins
	}
	out := make(map[string]toolcall.Tool, len(builtins))
	for name, t := range builtins {
		verb, arg := mapToolToCapability(name, "")
		if verb == "run_tool" && caps.Authorize(verb, arg) != nil {
			continue // not permitted → do not advertise it
		}
		out[name] = t
	}
	return out
}
