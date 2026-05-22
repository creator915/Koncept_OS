// Package snapshot implements kcpos's event-sourced state capture
// system. See docs/snapshot.md for the user-facing reference; this
// doc is the package-level Go overview.
//
// # Architecture
//
// Three storage layers, all under <workdir>/.kcpos/snapshots/:
//
//   - BlobStore — content-addressed blob pool. Files (sources, bundle
//     JSON, etc.) stored once per unique content via sha256, looked
//     up by sha.
//   - EventLog — append-only sha-chained event log. Each event has a
//     parent id; the chain id is sha256(parent || canonical(payload)).
//     Tampering any historical event invalidates every downstream id.
//   - RefStore — mutable named pointers (like git refs). Common ones:
//     tip, milestone/<phase>-<session>, attempt/<N>, pinned/<name>.
//
// The Snapshotter facade owns all three plus a mutex for serialised
// Append (tip read+write must be atomic to avoid orphan events under
// concurrent tool batches).
//
// # Capture
//
// Three hooks emit events when a Snapshotter is in flight (attached
// via snapshot.WithSnapshotter on the ctx):
//
//   - llm.turn — RunTurnOpts captures each assistant turn (subAgent
//     identifier, reasoning, content, tool calls). SLIM payload — no
//     full message history (transcripts/ already holds that, and
//     embedding it would grow events quadratically).
//   - tool.exec — runOneToolCall captures every invocation, including
//     side_effects (FileWrite/FileDelete inferred from workdir diff
//     before/after the call). Read-only tools produce empty
//     side_effects.
//   - outer.transition — RunRoutedTurnWithPersist (agent loop) emits
//     a transition event for every Outer.* state change.
//
// # Replay
//
// Reconstructs workdir state by walking from genesis to a chosen tip
// and re-applying every side_effect in chain order. Does NOT re-call
// the LLM or external tools.
//
// What "recorded outputs are the source of truth" means: when a tool
// like `probe` or `run_local` produced an output in the original run,
// that output is captured as part of the tool.exec event's `result`
// field. Anything DOWNSTREAM that consumed that result (the LLM's
// next turn, a subsequent tool call) is itself replayed from event
// records — so the downstream behavior is reproducible without
// re-invoking probe. The observation itself is NOT a side-effect
// (workdir was never changed by `probe`); only file.write /
// file.delete events mutate the workdir on replay. The replay
// contract is "reapplying recorded side_effects to a fresh workdir
// lands you on a byte-identical state" — observations are reproduced
// implicitly via their position in the event chain, not by writing
// them to the workdir.
//
// Determinism is in the side-effect replay, not in re-running
// causation.
//
// Replay uses WalkFrom (ancestry walk from a specific tip) so branched
// histories — produced by Rollback — are unambiguous.
//
// # Rollback (Phase 5)
//
// Rewinds tip to a target event, archives the current chain under
// attempt/<N>, restores workdir state to the target. Branched events
// remain reachable via the archive ref (failed-tip walks ancestry to
// genesis through the archived branch).
//
// # Lessons (Phase 6)
//
// Heuristic pattern table maps terminal Obstacle reasons to canned
// diagnostics + retry advice. Lessons saved as markdown with YAML
// frontmatter under .kcpos/snapshots/lessons/<branch>.md. The
// Snapshotter's Render/parseLessonMarkdown/ReadLessons round-trip
// guarantees Phase 7 can read what Phase 6 wrote.
//
// # Auto-retry (Phase 7)
//
// agent.RunRoutedTurnWithRetry wraps RunRoutedTurnWithPersist in a
// retry loop bounded by DefaultMaxAttempts (3). On terminal
// Obstacle: pickRollbackMilestone selects the most-work-invested
// milestone, Rollback archives + restores, SynthesizeLesson + Write
// persists the lesson, next attempt's H_architect injects the lesson
// preamble into its sub-agent system prompt.
//
// # Entry points
//
// User-facing: kcpos snap (CLI subcommands in cmd/kcpos/commands/snap.go).
//
// Programmatic:
//   - snapshot.NewSnapshotter(workdir)
//   - snapshot.WithSnapshotter / snapshot.FromContext (ctx threading)
//   - Snapshotter.Append / .Capture / .AppendFileWrite / .SetMilestone
//   - Snapshotter.Replay (ReplayOptions{TargetWorkdir, CleanFirst, StopAt})
//   - Snapshotter.Rollback (target, branchName) → RollbackResult
//   - Snapshotter.SynthesizeLesson (branchRef, LessonSynthesizer) → *Lesson
//   - Snapshotter.WriteLesson / .ReadLessons
package snapshot
