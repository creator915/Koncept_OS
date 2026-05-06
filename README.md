# kcpos — KonceptOS coding agent CLI

An AI coding agent that runs the **KonceptOS workflow**: design a project as a hypergraph, track work as sessions with rollback, verify against a frozen checkpoint before declaring "done." Single Go binary, OpenAI-compatible API, no daemon.

## Status

Personal alpha. Works well for small-to-medium greenfield projects driven by explicit type design. See [Limitations](#limitations) before relying on it.

## Install

Requires Go 1.22+.

```bash
git clone https://github.com/creator915/Koncept_OS.git
cd Koncept_OS
go install ./cmd/kcpos
# kcpos is now in $GOBIN (default ~/go/bin)
```

Or build a local binary:

```bash
go build -o kcpos ./cmd/kcpos
```

## Setup

```bash
export DEEPSEEK_API_KEY=sk-...
# Optional: pick a different OpenAI-compatible provider
# export KCPOS_PROVIDER=moonshot   # or qwen, openai
# export MOONSHOT_API_KEY=...
# export KCPOS_MODEL=kimi-k2-turbo-preview   # override default model
```

Supported providers (all OpenAI-compatible chat-completions):

| `KCPOS_PROVIDER` | API key env       | Default model           | Thinking mode |
|------------------|-------------------|-------------------------|---------------|
| `deepseek` (default) | `DEEPSEEK_API_KEY`  | `deepseek-v4-pro`       | ✓ |
| `moonshot`       | `MOONSHOT_API_KEY`  | `kimi-k2-turbo-preview` | — |
| `qwen`           | `DASHSCOPE_API_KEY` | `qwen3-max`             | — |
| `openai`         | `OPENAI_API_KEY`    | `gpt-5`                 | — |

## Quick start

```bash
# Interactive REPL with a fresh chat transcript
kcpos

# One-shot
kcpos "summarize all TODOs under src/"

# Resume the most recent transcript
kcpos chat --resume latest

# Direct CLI (no LLM, just reads/writes K/* files)
kcpos graph show <id>
kcpos graph validate
kcpos graph render --format mermaid
kcpos session list
kcpos checkpoint show
```

## Two operating modes

1. **AI agent** (`kcpos`, `kcpos chat`, `kcpos session resume <id>`) — LLM-driven REPL or one-shot. The agent has 35 tools spanning filesystem/shell, hypergraph (`graph_*`), work-sessions (`session_*`), and verification checkpoint (`checkpoint_*`).
2. **Direct CLI** (`kcpos graph|session|checkpoint <verb>`) — manipulate the same on-disk artifacts without going through the LLM. Useful for human inspection, scripts, and CI.

Both share the same `internal/*` packages — no duplicated logic.

## Subcommand overview

```
kcpos                            REPL chat (default)
kcpos "task"                     one-shot chat (legacy)
kcpos chat [--resume id] [task]  explicit chat

kcpos graph show <id>
kcpos graph create attribute|object --id ID --intent "..."
kcpos graph link refine|consume|produce ...
kcpos graph validate                                # 8 structural checks
kcpos graph preflight ID1 ID2 ...                   # parallel-safety
kcpos graph autowire --producer P --consumer C
kcpos graph render [--format mermaid|dot]

kcpos session list [--status STATUS]
kcpos session show <id>
kcpos session create --id ID --task "..." [--parent ID]
kcpos session status --id ID --to active|finished
kcpos session focus [--id ID | --clear]
kcpos session delete --id ID                        # rolls back graphDiff
kcpos session resume <id>                           # focus + REPL

kcpos checkpoint show [--id]
kcpos checkpoint add --id CHK-XXX --severity must|should|waiver --description "..."
kcpos checkpoint freeze
kcpos checkpoint fill --id CHK-XXX --proof "src/x.go:42 Sym"
kcpos checkpoint waive --id CHK-XXX --reason "..."
```

Run `kcpos <sub> --help` for per-subcommand details.

## KonceptOS workflow walkthrough

A normal end-to-end pass for a small project:

```bash
# 1. Plan verification first — what counts as done?
kcpos checkpoint add --id CHK-001 --severity must --description "Loader reads raw_data without errors"
kcpos checkpoint add --id CHK-002 --severity must --description "Cleaner produces non-empty clean_data"
kcpos checkpoint freeze

# 2. Sketch the type structure (or hand it to the agent)
kcpos graph create attribute --id raw_data --intent "raw sensor readings"
kcpos graph create attribute --id clean_data --intent "normalized & validated"
kcpos graph create object --id Loader --intent "reads sensors → raw_data"
kcpos graph create object --id Cleaner --intent "raw_data → clean_data"
kcpos graph link produce --object Loader --attribute raw_data
kcpos graph link consume --object Cleaner --attribute raw_data
kcpos graph link produce --object Cleaner --attribute clean_data
kcpos graph validate
kcpos graph render --format mermaid > docs/graph.md

# 3. Track the work
kcpos session create --id root --task "wire Loader → Cleaner pipeline"
kcpos session status --id root --to active
kcpos session focus --id root

# 4. Hand off to the agent (it'll record changes to root's graphDiff)
kcpos chat "implement Loader and Cleaner per K/graph.json. Write Go in src/, with src/loader_test.go and src/cleaner_test.go."

# 5. Verify
kcpos graph validate
go test ./src/...
kcpos checkpoint fill --id CHK-001 --proof "src/loader.go:12 Loader"
kcpos checkpoint fill --id CHK-002 --proof "src/cleaner.go:8 Cleaner"
kcpos checkpoint show

# 6. Final gate
kcpos session aggregate s_root  # via tool — collects descendant outputs
kcpos session show s_root        # check graphDiff + outputs
# (gate-check is currently agent-only: ask via `kcpos chat "session_gate_check s_root"`)
kcpos session status --id root --to finished
```

If the work fails: `kcpos session delete --id root` reverse-applies the captured graphDiff and removes session JSONs.

## File layout in your project

When you run kcpos in a project directory, it creates:

```
your-project/
├── K/
│   ├── graph.json           # the hypergraph
│   ├── checkpoint.json      # verification ledger
│   ├── sessions/            # one JSON per work-session
│   └── .current-session     # focus pointer (single id)
└── .kcpos/
    ├── transcripts/         # chat conversation logs
    └── history              # readline command history
```

Suggested `.gitignore`:

```
.kcpos/
K/.current-session
```

(Keep `K/graph.json`, `K/checkpoint.json`, and `K/sessions/` in git — they encode the project's design intent.)

## Architecture in 5 bullets

- `internal/chat` — LLM message protocol types (Message, ToolCall, ToolSpec)
- `internal/llm` — OpenAI-compatible streaming client with retry
- `internal/graph` — hypergraph data model + checker + preflight + render
- `internal/session` — KonceptOS work-sessions + diff capture + rollback + aggregate + gate-check
- `internal/checkpoint` — verification ledger
- `internal/tools` — agent tool wrappers (one per CLI capability)
- `internal/agent` — agent loop + system prompt (embedded `system.md`)
- `internal/repl` — interactive REPL with readline
- `internal/transcript` — chat conversation persistence
- `cmd/kcpos` — subcommand dispatch + per-subcommand handlers

## Limitations

Honest list of what does NOT work yet:

- **No "import existing code → graph" tool.** kcpos shines on greenfield; for retrofit you build the graph by hand.
- **No long-running task resilience.** Network blips during a multi-minute thinking turn aren't fully recoverable beyond the LLM-call retry; if kcpos is killed mid-run, the in-flight session may be in an awkward state.
- **`bash` tool has 30 s timeout.** Builds longer than that need to be split or the timeout raised.
- **`read_file` does not chunk large files.** Reading a 100 KB log dumps the whole thing into the LLM context.
- **No git integration beyond `git_status`.** commit/branch/diff need `bash`.
- **Single-process, no locking.** Two kcpos invocations in the same project will race on K/.
- **Sessions are saved per turn but not atomically.** A SIGKILL during Save can leave a half-written JSON.
- **System prompt is intentionally short.** Complex tasks may need the user to spell out the workflow explicitly. See `internal/agent/system.md`.

The prompt content is hash-pinned in `internal/agent/prompt_test.go` to prevent silent drift; PRs that change the prompt must update the hash, surfacing the change in review.

## License

Not yet declared. Treat as personal experimentation until otherwise specified.
