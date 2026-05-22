# kcpos snapshot system

Event-sourced state capture for kcpos agent runs. Every LLM turn, tool
execution, and outer-Router state transition becomes one event in an
append-only sha-chained log. The log lets you:

- **Replay** any historical state into a scratch workdir
- **Roll back** the chain to an earlier milestone and **archive** the
  failed branch
- **Synthesize lessons** from archived branches and inject them into
  the next attempt's system prompt — closing the agent's "try → fail →
  learn → retry" loop without manual intervention

This document is the user-facing reference for `kcpos snap` and the
`--max-attempts` retry workflow. See `internal/snapshot/` for the
implementation and `internal/snapshot/e2e_retry_workflow_test.go` for
the end-to-end scenario.

## Quick CLI reference

All `kcpos snap` subcommands operate on the snapshot store under
`./` (current workdir's `.kcpos/snapshots/`).

```
kcpos snap list [--type TYPE] [--limit N]
    List events in chain order. --type filters by
    llm.turn / tool.exec / outer.transition / milestone.

kcpos snap show <event-id>
    Show one event's payload. Accepts short ids (≥8 hex chars).

kcpos snap replay --to <event-id> --target <dir> [--clean]
    Reconstruct workdir state at the named event into <dir>.
    --to accepts ref names too (e.g. milestone/graph-declared-s_root).

kcpos snap rollback --to <event-id> [--name BRANCH]
    Rewind tip to <event-id>, archive current chain under
    attempt/<BRANCH> (auto-numbers if --name omitted), restore
    workdir to the target state.

kcpos snap lesson --branch <ref> [--write]
    Synthesize a lesson from an archived branch and either print to
    stdout or persist to .kcpos/snapshots/lessons/<branch>.md.

kcpos snap diff <event-a> <event-b>
    Show the workdir delta between two events as a file-by-file
    added/modified/deleted summary.

kcpos snap milestone <name> <event-id>
    Name an event with a stable ref (under milestone/<name>).

kcpos snap refs
    List every named ref (tip, milestone/*, attempt/*, pinned/*).
```

## How auto-retry works

```
$ kcpos run-routed --max-attempts 3 "Rebuild this PB-30 task"
```

`--max-attempts >= 2` enables the Phase 7 retry loop. Without the flag
(or `--max-attempts 1`, the default), runs work exactly as before:
single attempt, terminal Obstacle exits non-zero.

When retries are enabled, kcpos:

1. **Captures** every LLM turn, tool call, and Outer.* transition as
   events (Phase 2). State-mutating tool calls record their workdir
   side-effects via diff-based detection (Phase 3).
2. **Auto-sets milestones** on key forward-progress Outer.*
   transitions (Phase 7): architecture, graph-declared,
   all-confirmed, aggregated, built, checkpointed.
3. **On terminal Obstacle**, picks the latest available milestone as
   rollback target (priority: checkpointed > built > aggregated >
   all-confirmed > graph-declared > architecture), rolls the chain
   back, archives the failed branch under `attempt/<N>`, restores
   the workdir to that milestone's state.
4. **Synthesizes a lesson** from the archived branch (heuristic
   pattern table over Obstacle reasons; Phase 6) and writes it to
   `.kcpos/snapshots/lessons/attempt-<N>.md`.
5. **Re-enters** the outer Router. The next H_architect run reads
   the lessons file and prepends it to the sub-agent's system
   prompt, so the agent sees "previous attempt failed because X,
   do Y this time" before deciding strategy.

## Storage shape

Under `<workdir>/.kcpos/snapshots/`:

```
objects/<2-char>/<rest-of-sha>     content-addressed blob pool
                                   (file contents, dedup'd by sha256)
events/<event-id>.json             append-only event log
                                   (one file per event, sha-chained
                                    via parentId field)
refs/                              mutable named pointers
  tip.txt                          current chain head
  milestone/<phase>-<session>.txt  auto-set on Outer.* transitions
  attempt/<N>.txt                  set by Rollback on archive
  pinned/<name>.txt                user-managed
lessons/                           Phase 6 lesson markdown files
  attempt-<N>.md                   synthesized failure record
```

Real-world sizing: a typical PB-30 batch (5 instances × ~300 turns
each) produces ~5-15 MB of snapshot data after dedup, vs ~1 MB
without snapshotting. Most of that is the blob pool (file content
versions); event metadata is ~2 MB across all instances.

## Event types

| Type | Payload | Captured at | Side effects? |
|---|---|---|---|
| `llm.turn` | LLMTurnEvent (subAgent, turnIndex, reasoning, content, toolCalls) | RunTurnOpts inner loop | No — observation only |
| `tool.exec` | ToolExecEvent (tool, args, result, err, sideEffects) | runOneToolCall wrapper | Yes — workdir diff |
| `outer.transition` | OuterTransitionEvent (from, to, payload) | RunRoutedTurnWithPersist loop | No |
| `milestone` | MilestoneEvent (name, reason) | SetMilestone calls | No |
| `branch.head` | (reserved Phase 5 future) | Rollback | No |
| `lesson.synthesized` | (reserved Phase 6 future) | SynthesizeLesson | No |

Side-effect kinds inside `tool.exec.sideEffects`:
- `file.write` — Path + ContentSha + Mode (replay writes blob to path)
- `file.delete` — Path (replay removes path)
- Plus reserved kinds for Phase 5+ container/graph/bundle work

## Replay semantics

Replay re-applies side_effects in chain order; it does NOT re-invoke
the LLM or external tools. The contract: "apply the recorded
side_effects to a fresh workdir and you'll land on a state byte-
identical to the capture moment".

Determinism is in the side-effect replay, not in re-computing the
cause. This means:
- LLM non-determinism is irrelevant on replay
- External tool outputs (probe, run_local) come from recorded
  events — not re-invocation
- Replay cost is proportional to the number of side-effect events,
  not the LLM-call count

**Observations vs side-effects** — read-only tools (probe, run_local,
read_file, grep, list_dir) produce empty `side_effects` arrays in
their `tool.exec` event. Their results are still recorded in the
event's `result` field for forensic inspection, but replay does NOT
write those results back to the workdir. Only `file.write` /
`file.delete` events mutate disk on replay. So when this doc says
"recorded outputs are source of truth", it means: anything DOWNSTREAM
of an observation (e.g. the next LLM turn that read `probe`'s output)
is replayed from its own events, not by re-running `probe` — but the
observation itself doesn't change the workdir, so there's nothing to
"replay" about it beyond preserving its position in the chain.

## Lesson injection

When `.kcpos/snapshots/lessons/*.md` exist at run start, the
`H_architect` handler reads them in timestamp order and prepends them
to its sub-agent's system prompt as a framed preamble:

```
=== Prior attempts failed N time(s). Read these lessons before
    deciding strategy: ===

[attempt/1]
<full lesson markdown body>

[attempt/2]
<...>

=== End of prior-attempt lessons. Apply the "Do this on retry"
    sections to avoid repeating these failures. ===
```

The agent sees this in every LLM turn inside H_architect. After
H_architect succeeds, subsequent handlers (H_graph_declare,
H_confirm_one) don't re-inject the preamble — their architectural
context is already informed by H_architect's output.

## Heuristic pattern table

`internal/snapshot/lesson_heuristic.go` maps Obstacle reason
substrings to canned diagnostics + retry advice. Current patterns
(from PB-30 batches #1-#8):

- `vacuous-oracle-guard` → "compile + verify binary differs from reference"
- `[method-use-rule] artifact changed` → "re-characterize after edit"
- `[method-use-rule] zero behavior` → "characterize with observable ports"
- `reconstruction mode ... only N object(s)` → "decompose into 2+ testable sub-functions"
- `compile-not-enough` / `typecalc-test-required` → run-the-test path
- `runtime-trace-missing` → "synthesize tests with appendTrace"
- `no in-tree compile invoker` / `no in-tree test runner` → lang restructure
- `did not reach confirmed after sub-agent loop` → split / fix impl
- `gate FAIL` → generic gate-rule guidance
- `per-step inactivity timeout` → LLM stall diagnostics

LLM fallback synthesis is reserved as a Phase 7 follow-on hook
(`LessonSynthesizer` interface in `lesson.go`). Phase 6 ships
heuristic-only.

## Limitations & future work

- **Compile artifacts** (built binaries under `/workspace/executable`)
  are NOT in the snapshot pool — Phase 5 scope decision. Replay
  reconstructs sources + compile.sh; the user/agent re-runs compile
  to get the binary back. Phase 5+ can add `ExecutableReplace` side-
  effect support if rollback-with-binary becomes critical.
- **Lesson preamble size** can balloon at higher retry counts
  (~5 KB per lesson × 3-5 attempts). Future: dedup by heuristic
  key, truncate to DosAndDonts section, or limit to N most-recent.
- **Concurrent state-mutating tool batches** (currently absent in
  kcpos — all Concurrent=true tools are observation-only) would
  race the workdir-diff side-effect inference. The hook is
  documented with a CONCURRENCY CAVEAT comment; if such tools are
  added, move the hook serial.
- **Workdir restore during rollback** is non-atomic (clean → copy
  via tmp dir). Disk-full mid-copy leaves partial state. Phase 8+
  could add same-fs rename-based atomic swap.
- **`gate-passed`** milestone is set but intentionally excluded
  from the retry-loop priority list (its successor is Finished;
  rolling back there to retry yields the same outcome).
