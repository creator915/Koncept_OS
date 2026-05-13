# kcpos — KonceptOS coding agent CLI

An AI coding agent that runs the **KonceptOS workflow**: design a project as a typed hypergraph, drive each function-object through a mechanical verification chain (compile → describe → synthesize tests → test → review → confirm), track work as rollback-able sessions, and gate the whole project against a frozen checkpoint before declaring "done."

Single Go binary, OpenAI-compatible API, no daemon, no external state.

## Status

Personal alpha — **v9.3.2** (2026-05-13). The protocol has been through ~12 documented retro iterations on toy and real projects (pong batches v6→v9.0.1; Terraria batches v9.0.2→v9.3); each iteration removed an escape hatch or closed a silent-pass gap. See [`docs/v9.3-2026-05-13/README.md`](docs/v9.3-2026-05-13/README.md) for the most recent retro and `docs/` for the full history.

## Install

Requires Go 1.22+.

```bash
git clone https://github.com/creator915/Koncept_OS.git
cd Koncept_OS/kcpos
go install ./cmd/kcpos
# kcpos is now in $GOBIN (default ~/go/bin)
```

Or build locally: `go build -o bin/kcpos ./cmd/kcpos`.

## Setup

```bash
export DEEPSEEK_API_KEY=sk-...
# Optional: pick another OpenAI-compatible provider
# export KCPOS_PROVIDER=moonshot   # or qwen, openai
# export KCPOS_MODEL=kimi-k2-turbo-preview
```

| `KCPOS_PROVIDER`     | API key env         | Default model           | Thinking |
|----------------------|---------------------|-------------------------|---------|
| `deepseek` (default) | `DEEPSEEK_API_KEY`  | `deepseek-v4-pro`       | ✓ |
| `moonshot`           | `MOONSHOT_API_KEY`  | `kimi-k2-turbo-preview` | — |
| `qwen`               | `DASHSCOPE_API_KEY` | `qwen3-max`             | — |
| `openai`             | `OPENAI_API_KEY`    | `gpt-5`                 | — |

For HTML deliverables, kcpos also needs Playwright + Chromium under `~/.kcpos/cache/playwright/`. `kcpos doctor` discovers existing installs and `runtime_link` / `runtime_install` (agent-side) wire them in.

## Quick start

```bash
kcpos                                    # REPL with a fresh transcript
kcpos "implement the spec in SPEC.md"    # one-shot
kcpos chat --resume latest               # continue most recent transcript

# Direct CLI (no LLM — read/write the same K/* files agents use)
kcpos graph show <id>
kcpos graph validate
kcpos graph render --format mermaid
kcpos session list
kcpos checkpoint show
kcpos doc protocol                       # print the runtime protocol
kcpos doc system                         # print the full LLM system prompt
```

## Two operating modes

1. **AI agent** (`kcpos`, `kcpos chat`, `kcpos session resume <id>`) — LLM-driven REPL or one-shot. ~40 tools spanning filesystem, hypergraph (`graph_*`), work-sessions (`session_*`), checkpoint (`checkpoint_*`), type calculator (`typecalc_*` + the high-level `confirm_object`), browser smoke (`runtime_smoke`), and sub-agent delegation (`spawn_subagent`).
2. **Direct CLI** (`kcpos graph|session|checkpoint <verb>`) — manipulate the same on-disk artifacts without going through the LLM. Useful for inspection, scripts, CI.

Both share the same `internal/*` packages — no duplicated logic.

## The KonceptOS workflow

The protocol is the **source of truth**, stored in code at [`internal/protocol/protocol.go`](internal/protocol/protocol.go) and rendered by `kcpos doc protocol`. What follows is a high-level summary.

### L0 → L4 product layers

| Layer | What exists at this layer |
|---|---|
| **L0** | `K/defs/<id>.*` signatures + `K/graph.json` node declarations |
| **L1** | `src/*.impl.*` (or `K/frags/<id>.js` for single-file HTML) + graph `status=confirmed` |
| **L2** | unit/contract tests passing |
| **L3** | bundled artifact (when relevant) |
| **L4** | `checkpoint.json` PASS + root session aggregates outputs + deliverable file exists |

### The verification chain — `confirm_object`

The canonical way to mark a single graph object as confirmed:

```
compile  →  describe  →  synthesize_tests  →  test  →  review  →  status=confirmed
                                                                       ↑
                                                              (gate also requires
                                                               accepted-evidence ok)
```

Failures at any step surface enriched diagnostics and route through automatic enrich-retry up to **CycleCap=5** attempts. Exhaustion is a hard `Obstacle`; **v9.2 removed waiver/obstacle escape paths**, so the only way past a stuck object is to fix the code, refactor, or extend `internal/typecalc/lang/` for a new runner.

#### HTML branch (v9.3+)

When `obj.Impl` ends in `.html` / `.htm`, the chain skips `synthesize_tests + test` (vm.Script can't model the browser) and runs:

```
compile  →  describe  →  session_build  →  runtime_smoke  →  review  →  confirmed
                            (reference-mode `<script src>` assembly; auto-invoked)
                                                ↑
                                       headless Chromium boot;
                                       OK = loadFired ∧ no pageErrors
```

`graph_merge_object` **rejects** `impl=*.html` unless `implFragment=K/frags/<id>.js` is set in the same patch (the v93-04 monolithic anti-pattern is blocked at write time). `session_build` emits `<script src>` references by default — cheap to re-run, so the chain can incrementally assemble before every smoke.

### Path A vs Path B (decomposition)

A session is "path B" (delegate via `spawn_subagent`) when **any** of:

- ≥3 declared objects in current scope, OR
- ≥400 estimated impl LOC across decomposition, OR
- spans ≥2 SPEC chapters.

Path B canonical pattern: one `spawn_subagent` per object, all in a single parallel batch, with `session_id=s_impl_<lower_objectid>` (session auto-created with `parent=FindRoot(focus)` — the v9.3 chain-spawn fix prevents sibling-becomes-descendant degradation).

### Session lifecycle (v9.3)

- `session_start` — atomic create + activate + focus
- `session_dismiss` — additive cleanup; refuses active sessions / active children
- `session_rollback` — **destructive**: depth-first reverse-applies graphDiff and deletes def/impl files this session created

The pre-v9.3 single `session_delete` conflated these and caused 4 incidents (v9.0.6 terraria-03, v92-01, v92-02 destroyed root deliverables). Now they're separate tools with explicit guarantees.

### Root finish flow

```
R1  session_aggregate          collect implementations / tests / newSignatures from child sessions
R2  session_build  +  build/test    (HTML: reference assembly; multi-file: npm/cargo/etc.)
R3  checkpoint_fill                  every `must` item gets codeProof (file:line + symbol)
R4  session_gate_check               fixed-point — any FAIL means iterate, only PASS allows `session_status finished`
```

Every confirmed object must produce **real passing evidence** at the gate: `kind=test ok=true` for testable languages, `kind=runtime ok=true` (smoke) for HTML deliverables. There is no waiver path.

## Check coverage model (v9.3.2)

Every rule in `StaticCheck`, `RuntimeCheck`, agent `hooks.go`, and the per-object gate emits **one of three explicit states**: `Pass`, `Fail`, or `Skipped<reason>`. The aggregator (`typecalc.AggregateOK`) treats a missing emission as fail — closing the silent-pass class where "no issue reported" was indistinguishable from "check didn't run".

This was the root cause of the v9.3 P0 bug: `RuntimeCheck` looked for a trace file the HTML branch by design never produced; absence was conflated with "pass" → every HTML object got stuck in review-retry. The fix isn't just the carve-out, it's the model: **no evidence = no pass.**

## Subcommand overview

```
kcpos                              REPL chat (default)
kcpos "task"                       one-shot chat
kcpos chat [--resume id] [task]    explicit chat

kcpos graph show <id>
kcpos graph create attribute|object --id ID --intent "..."
kcpos graph link refine|consume|produce|mutate ...
kcpos graph validate                                   # structural checks
kcpos graph preflight ID1 ID2 ...                      # parallel-safety check
kcpos graph autowire --producer P --consumer C
kcpos graph render [--format mermaid|dot]

kcpos session list [--status STATUS]
kcpos session show <id>
kcpos session start  --id ID --task "..." [--parent ID]   # atomic create+active+focus
kcpos session create --id ID --task "..." [--parent ID]
kcpos session status --id ID --to active|finished
kcpos session focus [--id ID | --clear]
kcpos session delete --id ID                              # rolls back graphDiff
kcpos session resume <id>                                 # focus + REPL

kcpos checkpoint show [--id]
kcpos checkpoint add --id CHK-XXX --severity must|should --description "..."
kcpos checkpoint freeze
kcpos checkpoint fill --id CHK-XXX --proof "src/x.go:42 Sym"

kcpos doc protocol                  # the protocol struct table as markdown
kcpos doc system                    # full LLM system prompt = protocol + tool catalog
kcpos doctor [--install] [-y]       # detect/install external toolchain (playwright etc.)
```

Run `kcpos <sub> --help` for per-subcommand details.

## File layout in your project

When you run kcpos in a project directory, it creates and maintains:

```
your-project/
├── SPEC.md                       # (your input — what you want built)
├── K/                            # graph + sessions + checkpoint (commit this)
│   ├── graph.json                # the hypergraph: attributes + objects + edges
│   ├── checkpoint.json           # frozen verification ledger
│   ├── sessions/                 # one JSON per work-session (parent/children + graphDiff)
│   │   └── s_*.json
│   ├── defs/                     # per-entity signature/contract files (.ts / .go / .py / .js JSDoc)
│   │   └── <Id>.{ts,go,py,js,…}
│   ├── frags/                    # per-object impl fragments (HTML single-file projects only)
│   │   └── <Id>.js
│   └── .current-session          # focus pointer — single id, plain text
├── src/                          # (optional) for multi-file projects' impl files
├── index.html                    # (HTML projects) deliverable assembled by session_build
├── .kcpos/                       # kcpos runtime state (gitignore this)
│   ├── transcripts/              # chat conversation logs (JSON per session)
│   ├── typecalc/                 # evidence bundles: <id>.json per graph object
│   │   ├── <Id>.json             #   compile / spec / tests / test / accepted / runtimeSmoke / cycles
│   │   └── …
│   ├── typecalc-runtime/         # runtime traces written by synthesized tests
│   └── history                   # readline command history
└── ~/.kcpos/cache/playwright/    # (~200MB) headless Chromium for runtime_smoke (machine-wide)
```

Suggested `.gitignore`:

```
.kcpos/
K/.current-session
```

Commit `K/graph.json`, `K/defs/`, `K/frags/`, `K/sessions/`, `K/checkpoint.json` — they encode design intent and verification history.

## Source tree

Build artifacts (`bin/`, `target/`, `kcpos` binary, `.kcpos/` dev runtime state) are gitignored and omitted.

> **v9.4 (2026-05-13) architecture refactor.** The tree below reflects a full DDD layering pass: `internal/{app,domain,infra,llm,router,runtime,shared,tools,typecalc}/`. Domain types are pure; orchestration lives in `app/workflow/`; persistence lives in `infra/persistence/`. Each new placeholder dir holds a `doc.go` until code populates it.

```
kcpos/
├── .gitignore
├── cmd/
│   └── kcpos/
│       ├── commands/
│       │   ├── chat.go
│       │   ├── checkpoint.go
│       │   ├── doc.go
│       │   ├── doctor.go
│       │   ├── graph.go
│       │   └── session.go
│       └── main.go
├── docs/
│   ├── architecture/
│   │   ├── legacy-checkpoint-2026-05-09.md
│   │   ├── legacy-protocol-2026-05-09.md
│   │   └── TypeCalculator.md
│   ├── audits/
│   │   ├── pong-01-v8.7-claudemd-audit-2026-05-11.md
│   │   ├── pong-02-v8.7-claudemd-audit-2026-05-11.md
│   │   ├── pong-03-v8.7-claudemd-audit-2026-05-11.md
│   │   ├── pong-04-v8.7-claudemd-audit-2026-05-11.md
│   │   └── pong-05-v8.7-claudemd-audit-2026-05-11.md
│   ├── experiments/
│   │   └── README.md
│   ├── protocol/
│   │   └── README.md
│   ├── releases/
│   │   └── README.md
│   └── reports/
│       ├── pong-batch-v8-2026-05-09.md
│       ├── pong-batch-v8.5-2026-05-09.md
│       ├── pong-batch-v8.7-2026-05-11.md
│       ├── pong-batch-v9.0-2026-05-11.md
│       ├── pong-batch-v9.0.1-2026-05-11.md
│       ├── pong-smoke-2026-05-09.md
│       ├── pong-smoke-v6-2026-05-09.md
│       ├── pong-smoke-v7-2026-05-09.md
│       ├── terraria-batch-v9.0.2-2026-05-11.md
│       ├── v9.0.6-2026-05-12/
│       │   ├── README.md
│       │   ├── terraria-01.md
│       │   ├── terraria-02.md
│       │   ├── terraria-03.md
│       │   ├── terraria-04.md
│       │   └── terraria-05.md
│       ├── v9.2-2026-05-12/
│       │   ├── README.md
│       │   ├── terraria-v92-01.md
│       │   ├── terraria-v92-02.md
│       │   ├── terraria-v92-03.md
│       │   ├── terraria-v92-04.md
│       │   └── terraria-v92-05.md
│       └── v9.3-2026-05-13/
│           └── README.md
├── go.mod
├── go.sum
├── internal/
│   ├── app/
│   │   ├── agent/
│   │   │   ├── hooks.go
│   │   │   ├── hooks_test.go
│   │   │   ├── log.go
│   │   │   ├── log_test.go
│   │   │   ├── loop.go
│   │   │   ├── loop_parallel_test.go
│   │   │   ├── permission.go
│   │   │   ├── permission_test.go
│   │   │   ├── prompt.go
│   │   │   ├── prompt_test.go
│   │   │   ├── subagent.go
│   │   │   ├── subagent_caps_test.go
│   │   │   └── system.md
│   │   ├── repl/
│   │   │   └── chat.go
│   │   ├── services/
│   │   │   ├── checkpoint_builtins.go
│   │   │   ├── checkpoint_service.go
│   │   │   ├── confirm_object_service.go
│   │   │   ├── review_service.go
│   │   │   ├── review_service_test.go
│   │   │   ├── synthesize_service.go
│   │   │   ├── synthesize_service_jsdoc_test.go
│   │   │   ├── typecalc_builtins.go
│   │   │   └── typecalc_service.go
│   │   └── workflow/
│   │       ├── checkpoint_lifecycle.go
│   │       ├── checkpoint_lifecycle_test.go
│   │       ├── session_aggregate.go
│   │       ├── session_aggregate_test.go
│   │       ├── session_architecture.go
│   │       ├── session_architecture_test.go
│   │       ├── session_capture_test.go
│   │       ├── session_diff.go
│   │       ├── session_findroot_test.go
│   │       ├── session_fixtures_test.go
│   │       ├── session_gate.go
│   │       ├── session_gate_accepted_test.go
│   │       ├── session_gate_workflow_test.go
│   │       ├── session_lifecycle.go
│   │       ├── session_lifecycle_test.go
│   │       ├── session_rollback.go
│   │       ├── session_start.go
│   │       ├── session_start_test.go
│   │       └── testdata/
│   ├── domain/
│   │   ├── checkpoint/
│   │   │   └── types.go
│   │   ├── graph/
│   │   │   ├── autowire.go
│   │   │   ├── checker.go
│   │   │   ├── checker_test.go
│   │   │   ├── clone.go
│   │   │   ├── merge.go
│   │   │   ├── merge_status_test.go
│   │   │   ├── mutates_test.go
│   │   │   ├── ops.go
│   │   │   ├── order.go
│   │   │   ├── preflight.go
│   │   │   ├── preflight_test.go
│   │   │   ├── render.go
│   │   │   ├── render_test.go
│   │   │   ├── show.go
│   │   │   └── types.go
│   │   ├── protocol/
│   │   │   ├── protocol.go
│   │   │   ├── spec_parser.go
│   │   │   └── spec_parser_test.go
│   │   └── session/
│   │       ├── diff.go
│   │       └── types.go
│   ├── infra/
│   │   ├── cache/
│   │   │   └── doc.go
│   │   ├── fs/
│   │   │   └── doc.go
│   │   ├── git/
│   │   │   └── doc.go
│   │   ├── logging/
│   │   │   └── doc.go
│   │   └── persistence/
│   │       ├── checkpoint_store.go
│   │       ├── graph_store.go
│   │       ├── session_focus.go
│   │       └── session_store.go
│   ├── llm/
│   │   ├── memory/
│   │   │   └── transcript.go
│   │   ├── prompt/
│   │   │   └── doc.go
│   │   ├── provider/
│   │   │   └── providers.go
│   │   ├── toolcall/
│   │   │   └── tool.go
│   │   └── transport/
│   │       ├── client.go
│   │       └── message.go
│   ├── router/
│   │   ├── chains/
│   │   │   ├── chain.go
│   │   │   ├── chain_test.go
│   │   │   ├── payloads.go
│   │   │   └── types.go
│   │   ├── feedback.go
│   │   ├── feedback_test.go
│   │   ├── llm.go
│   │   ├── router.go
│   │   ├── router_test.go
│   │   └── types.go
│   ├── runtime/
│   │   ├── executor/
│   │   │   └── doc.go
│   │   ├── install/
│   │   │   ├── install.go
│   │   │   └── link.go
│   │   ├── playwright/
│   │   │   ├── playwright.go
│   │   │   └── playwright_harness.go
│   │   ├── preflight/
│   │   │   ├── preflight.go
│   │   │   ├── preflight_test.go
│   │   │   └── registry.go
│   │   └── sandbox/
│   │       └── doc.go
│   ├── shared/
│   │   ├── concurrency/
│   │   │   └── doc.go
│   │   ├── errors/
│   │   │   └── doc.go
│   │   ├── maps/
│   │   │   └── doc.go
│   │   └── slices/
│   │       └── doc.go
│   ├── tools/
│   │   ├── fs/
│   │   │   ├── bash.go
│   │   │   ├── builtins.go
│   │   │   ├── edit.go
│   │   │   ├── glob.go
│   │   │   ├── grep.go
│   │   │   ├── list.go
│   │   │   ├── markdown.go
│   │   │   ├── markdown_test.go
│   │   │   ├── read.go
│   │   │   ├── write.go
│   │   │   └── write_autotypecalc_test.go
│   │   ├── git/
│   │   │   ├── builtins.go
│   │   │   └── git_status.go
│   │   ├── graph/
│   │   │   ├── builtins.go
│   │   │   ├── graph.go
│   │   │   ├── graph_temp_focus_test.go
│   │   │   └── html_impl_fragment_test.go
│   │   ├── runtime/
│   │   │   ├── builtins.go
│   │   │   └── runtime_smoke.go
│   │   ├── session/
│   │   │   ├── build.go
│   │   │   ├── build_test.go
│   │   │   ├── builtins.go
│   │   │   ├── dismiss_test.go
│   │   │   └── session.go
│   │   ├── subagent.go
│   │   └── tool.go
│   └── typecalc/
│       ├── core/
│       │   ├── bundle.go
│       │   ├── coverage.go
│       │   ├── coverage_test.go
│       │   ├── cycles.go
│       │   ├── env.go
│       │   ├── evidence.go
│       │   ├── permission.go
│       │   ├── permission_test.go
│       │   ├── request.go
│       │   ├── request_test.go
│       │   ├── state.go
│       │   ├── status_machine.go
│       │   ├── status_machine_test.go
│       │   ├── sum.go
│       │   ├── sum_test.go
│       │   ├── types.go
│       │   └── types_test.go
│       ├── feedback/
│       │   ├── feedback.go
│       │   └── feedback_test.go
│       ├── harness/
│       │   ├── harness.go
│       │   └── harness_test.go
│       ├── lang/
│       │   ├── compile.go
│       │   ├── compile_test.go
│       │   ├── format.go
│       │   ├── format_test.go
│       │   ├── fs.go
│       │   ├── test.go
│       │   └── test_loop_test.go
│       ├── probe/
│       │   ├── probe.go
│       │   ├── probe_llm_test.go
│       │   └── probe_test.go
│       ├── review/
│       │   ├── e2e_review_test.go
│       │   ├── html_branch_replay_test.go
│       │   ├── review.go
│       │   ├── review_test.go
│       │   ├── runtime_check.go
│       │   ├── runtime_check_sparse_test.go
│       │   ├── runtime_check_test.go
│       │   ├── static_check.go
│       │   └── static_check_test.go
│       ├── rule/
│       │   ├── defaults.go
│       │   ├── router.go
│       │   ├── router_test.go
│       │   └── rule.go
│       ├── runtime/
│       │   └── doc.go
│       └── synthesize/
│           ├── describe.go
│           ├── helpers.go
│           ├── synthesize.go
│           ├── synthesize_test.go
│           └── testhelper_test.go
├── README.md
└── tests/
    └── terraria/
        ├── index.html
        ├── K/
        │   ├── checkpoint.json
        │   ├── defs/
        │   │   ├── CraftItem.js
        │   │   ├── enemy_list.js
        │   │   ├── frame_rendered.js
        │   │   ├── InitWorld.js
        │   │   ├── input_state.js
        │   │   ├── particle_list.js
        │   │   ├── player_state.js
        │   │   ├── RenderFrame.js
        │   │   ├── SpawnEnemies.js
        │   │   ├── time_state.js
        │   │   ├── UpdateGame.js
        │   │   ├── UpdateInput.js
        │   │   └── world_data.js
        │   ├── frags/
        │   │   ├── CraftItem.js
        │   │   ├── InitWorld.js
        │   │   ├── RenderFrame.js
        │   │   ├── SpawnEnemies.js
        │   │   ├── UpdateGame.js
        │   │   └── UpdateInput.js
        │   ├── graph.json
        │   └── sessions/
        │       └── s_terraria_root.json
        └── SPEC.md
```

## Limitations

Honest list of what does NOT work yet:

- **No "import existing code → graph" tool.** kcpos shines on greenfield; for retrofit you build the graph by hand.
- **LLM stream timeout retry is incomplete.** v93-01 in the 2026-05-13 batch died twice on `read stream: context deadline exceeded`; transcript is preserved so `kcpos chat --resume latest` continues, but mid-write loss happens. TODO for v9.4.
- **`bash` tool has 30 s timeout.** Builds longer than that need to be split.
- **`read_file` does not chunk large files.** Use `markdown_outline` + `markdown_section` for long markdown.
- **No git integration beyond `git_status`.** commit/branch/diff go through `bash`.
- **Single-process, no locking.** Two kcpos invocations in the same project will race on K/.
- **Sessions saved per turn but not atomically.** SIGKILL during Save can leave a half-written JSON.
- **System prompt is intentionally short.** Complex tasks may need the user to spell out the workflow. See `internal/agent/system.md`.

## License

Not yet declared. Treat as personal experimentation until otherwise specified.
