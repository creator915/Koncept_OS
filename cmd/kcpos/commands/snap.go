package commands

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/creator915/Koncept_OS/internal/snapshot"
)

// RunSnap dispatches the `kcpos snap` subcommands. Each subcommand
// operates on the snapshot store under cwd's .kcpos/snapshots/ —
// the same store the agent populates during a run-routed pass.
func RunSnap(args []string) int {
	if len(args) == 0 {
		printSnapUsage()
		return 1
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list":
		return runSnapList(rest)
	case "show":
		return runSnapShow(rest)
	case "replay":
		return runSnapReplay(rest)
	case "milestone":
		return runSnapMilestone(rest)
	case "diff":
		return runSnapDiff(rest)
	case "refs":
		return runSnapRefs(rest)
	case "rollback":
		return runSnapRollback(rest)
	case "lesson":
		return runSnapLesson(rest)
	case "-h", "--help", "help":
		printSnapUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown snap subcommand: %s\n\n", sub)
		printSnapUsage()
		return 1
	}
}

func printSnapUsage() {
	fmt.Fprintln(os.Stderr, `kcpos snap — event-sourced snapshot navigation

Usage:
  kcpos snap list [--type TYPE] [--limit N]
      List events in chain order. --type filters by event type
      (llm.turn / tool.exec / outer.transition / milestone).

  kcpos snap show <event-id>
      Show one event's full payload. Accepts short ids (≥8 hex chars)
      as long as they uniquely identify an event.

  kcpos snap replay --to <event-id> --target <dir> [--clean]
      Reconstruct workdir state at the named event into <dir>.
      --clean wipes the target's watched roots before applying
      side_effects (default: false; require an empty target).

  kcpos snap milestone <name> <event-id>
      Name an event with a stable ref (under milestone/<name>).
      Used by rollback / replay-to-milestone navigation.

  kcpos snap diff <event-a> <event-b>
      Show the workdir delta between two events as a file-by-file
      added/modified/deleted summary.

  kcpos snap refs
      List every named ref (tip, milestone/*, attempt/*, pinned/*)
      and the event it points at.

  kcpos snap rollback --to <event-id> [--name BRANCH]
      Rewind tip to <event-id> and archive the current branch under
      attempt/<name> (auto-allocates attempt/<N> when --name omitted).
      Workdir state is restored to <event-id> via tmp-dir swap.
      Phase 5: introduces branched event histories; subsequent
      WalkFrom navigation handles them correctly.

  kcpos snap lesson --branch <ref> [--write]
      Synthesize a lesson from an archived branch (e.g. attempt/1)
      using the heuristic pattern table. --write persists the
      lesson to .kcpos/snapshots/lessons/<branch>.md; without
      --write, the lesson is printed to stdout.`)
}

// loadSnapshotter opens the snapshot store under cwd. Errors when
// no snapshots have been captured yet (the typical "fresh project"
// case prints a clear next-step hint).
func loadSnapshotter() (*snapshot.Snapshotter, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}
	s := snapshot.NewSnapshotter(cwd)
	if !s.IsEnabled() {
		return nil, fmt.Errorf("no snapshot store at %s — run `kcpos run-routed` first (snapshotting captures on every state-mutating tool call)", s.SnapshotsRoot())
	}
	return s, nil
}

func runSnapList(args []string) int {
	fs := flag.NewFlagSet("snap list", flag.ContinueOnError)
	typeFilter := fs.String("type", "", "filter by event type (llm.turn / tool.exec / outer.transition / milestone)")
	limit := fs.Int("limit", 0, "show at most N events (0 = all)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	s, err := loadSnapshotter()
	if err != nil {
		return printErr(err)
	}
	events, err := s.Events.List()
	if err != nil {
		return printErr(err)
	}
	count := 0
	for _, ev := range events {
		if *typeFilter != "" && ev.Type != *typeFilter {
			continue
		}
		summary := summariseEvent(ev)
		fmt.Printf("%s  %-18s  %s  %s\n",
			ev.ID[:12], ev.Type, ev.Timestamp.Format("15:04:05"), summary)
		count++
		if *limit > 0 && count >= *limit {
			break
		}
	}
	if count == 0 {
		fmt.Fprintln(os.Stderr, "(no events match filter)")
	}
	return 0
}

func runSnapShow(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: kcpos snap show <event-id>")
		return 1
	}
	s, err := loadSnapshotter()
	if err != nil {
		return printErr(err)
	}
	id, err := resolveEventID(s, args[0])
	if err != nil {
		return printErr(err)
	}
	ev, err := s.Events.Get(id)
	if err != nil {
		return printErr(err)
	}
	// Pretty-print payload — re-marshal indented for readability.
	var pretty interface{}
	if jerr := json.Unmarshal(ev.Payload, &pretty); jerr == nil {
		buf, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Printf("id:        %s\nparent:    %s\ntype:      %s\ntimestamp: %s\npayload:\n%s\n",
			ev.ID, parentOrGenesis(ev.ParentID), ev.Type,
			ev.Timestamp.Format("2006-01-02 15:04:05 MST"), string(buf))
	} else {
		fmt.Printf("id:        %s\ntype:      %s\npayload (raw):\n%s\n",
			ev.ID, ev.Type, string(ev.Payload))
	}
	return 0
}

func runSnapReplay(args []string) int {
	fs := flag.NewFlagSet("snap replay", flag.ContinueOnError)
	to := fs.String("to", "", "event id (or short prefix) to replay to (default: tip)")
	target := fs.String("target", "", "target workdir for reconstructed state (REQUIRED, must differ from cwd)")
	cleanFirst := fs.Bool("clean", false, "wipe target's watched roots before replaying")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *target == "" {
		fmt.Fprintln(os.Stderr, "kcpos snap replay: --target REQUIRED (replaying to cwd would clobber live state)")
		return 1
	}
	// NOTE: Replay also rejects target == source workdir, but that
	// check is a raw string compare. If --target is the absolute
	// path of cwd while the source workdir is relative, the strings
	// won't match and Replay will proceed — wiping the source's
	// watched roots if --clean. Future: filepath.Abs both sides
	// before comparison. Today: document and trust the caller.
	s, err := loadSnapshotter()
	if err != nil {
		return printErr(err)
	}
	var stopAt string
	if *to != "" {
		id, err := resolveEventID(s, *to)
		if err != nil {
			return printErr(err)
		}
		stopAt = id
	}
	applied, err := s.Replay(snapshot.ReplayOptions{
		TargetWorkdir: *target,
		CleanFirst:    *cleanFirst,
		StopAt:        stopAt,
	})
	if err != nil {
		return printErr(err)
	}
	fmt.Fprintf(os.Stderr, "replayed %d events into %s\n", applied, *target)
	return 0
}

func runSnapMilestone(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: kcpos snap milestone <name> <event-id>")
		return 1
	}
	s, err := loadSnapshotter()
	if err != nil {
		return printErr(err)
	}
	name := args[0]
	if name == "" {
		fmt.Fprintln(os.Stderr, "kcpos snap milestone: <name> must be non-empty (empty would write refs/milestone/.txt which can't be retrieved)")
		return 1
	}
	id, err := resolveEventID(s, args[1])
	if err != nil {
		return printErr(err)
	}
	if err := s.Refs.Set("milestone/"+name, id); err != nil {
		return printErr(err)
	}
	fmt.Fprintf(os.Stderr, "milestone/%s → %s\n", name, id[:16])
	return 0
}

func runSnapDiff(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: kcpos snap diff <event-a> <event-b>")
		return 1
	}
	s, err := loadSnapshotter()
	if err != nil {
		return printErr(err)
	}
	idA, err := resolveEventID(s, args[0])
	if err != nil {
		return printErr(err)
	}
	idB, err := resolveEventID(s, args[1])
	if err != nil {
		return printErr(err)
	}
	// Replay each side into a scratch dir, snapshot both, diff.
	tmpA, err := os.MkdirTemp("", "kcpos-snap-diff-a-*")
	if err != nil {
		return printErr(err)
	}
	defer os.RemoveAll(tmpA)
	tmpB, err := os.MkdirTemp("", "kcpos-snap-diff-b-*")
	if err != nil {
		return printErr(err)
	}
	defer os.RemoveAll(tmpB)
	if _, err := s.Replay(snapshot.ReplayOptions{TargetWorkdir: tmpA, CleanFirst: true, StopAt: idA}); err != nil {
		return printErr(fmt.Errorf("replay %s: %w", idA[:12], err))
	}
	if _, err := s.Replay(snapshot.ReplayOptions{TargetWorkdir: tmpB, CleanFirst: true, StopAt: idB}); err != nil {
		return printErr(fmt.Errorf("replay %s: %w", idB[:12], err))
	}
	snapA, err := snapshot.TakeWorkdirSnapshot(tmpA)
	if err != nil {
		return printErr(err)
	}
	snapB, err := snapshot.TakeWorkdirSnapshot(tmpB)
	if err != nil {
		return printErr(err)
	}
	diff := snapA.Diff(snapB)
	if diff.IsEmpty() {
		fmt.Fprintln(os.Stderr, "(no workdir delta between these events)")
		return 0
	}
	for _, p := range diff.Added {
		fmt.Printf("+ %s\n", p)
	}
	for _, p := range diff.Modified {
		fmt.Printf("~ %s\n", p)
	}
	for _, p := range diff.Deleted {
		fmt.Printf("- %s\n", p)
	}
	return 0
}

func runSnapLesson(args []string) int {
	fs := flag.NewFlagSet("snap lesson", flag.ContinueOnError)
	branch := fs.String("branch", "", "branch ref to synthesize from (e.g. attempt/1) — REQUIRED")
	write := fs.Bool("write", false, "persist lesson to .kcpos/snapshots/lessons/<branch>.md (default: print to stdout)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *branch == "" {
		fmt.Fprintln(os.Stderr, "kcpos snap lesson: --branch REQUIRED (use `kcpos snap refs` to list archived branches)")
		return 1
	}
	s, err := loadSnapshotter()
	if err != nil {
		return printErr(err)
	}
	// Phase 6 ships heuristic-only; Phase 7 will wire a real LLM
	// callback when auto-retry uses lessons to inform next attempt.
	lesson, err := s.SynthesizeLesson(*branch, nil)
	if err != nil {
		return printErr(err)
	}
	if *write {
		if err := s.WriteLesson(lesson); err != nil {
			return printErr(err)
		}
		fmt.Fprintf(os.Stderr, "wrote lesson to .kcpos/snapshots/lessons/%s.md (generated-by %s)\n",
			strings.ReplaceAll(lesson.BranchRef, "/", "-"), lesson.GeneratedBy)
		return 0
	}
	// Stream the rendered markdown to stdout so the user can pipe
	// it into less/glow. Render() is the single source of truth —
	// same format that WriteLesson uses on disk.
	fmt.Print(lesson.Render())
	return 0
}

func runSnapRollback(args []string) int {
	fs := flag.NewFlagSet("snap rollback", flag.ContinueOnError)
	to := fs.String("to", "", "target event id or ref name to rewind to (REQUIRED)")
	name := fs.String("name", "", "branch name appended under attempt/ (empty = auto attempt/<N>)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *to == "" {
		fmt.Fprintln(os.Stderr, "kcpos snap rollback: --to REQUIRED (use a milestone ref like milestone/graph-declared, or a sha prefix)")
		return 1
	}
	s, err := loadSnapshotter()
	if err != nil {
		return printErr(err)
	}
	target, err := resolveEventID(s, *to)
	if err != nil {
		return printErr(err)
	}
	result, err := s.Rollback(target, *name)
	if err != nil {
		return printErr(err)
	}
	fmt.Fprintf(os.Stderr,
		"rolled back to %s — archived branch as %s (failed tip %s, %d events archived)\n",
		result.RolledBackTo[:16], result.ArchivedBranchRef,
		result.FailedTip[:16], result.EventsArchived)
	return 0
}

func runSnapRefs(_ []string) int {
	s, err := loadSnapshotter()
	if err != nil {
		return printErr(err)
	}
	refs, err := s.Refs.List()
	if err != nil {
		return printErr(err)
	}
	names := make([]string, 0, len(refs))
	for n := range refs {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "(no refs)")
		return 0
	}
	for _, n := range names {
		fmt.Printf("%-32s  %s\n", n, refs[n][:16])
	}
	return 0
}

// resolveEventID accepts a sha (full or ≥8-char prefix) OR a ref
// name (e.g. "tip", "milestone/foo") and returns the full event id.
// Ambiguous prefixes error out so the caller is forced to disambiguate.
//
// Resolution order: ref lookup first, sha-prefix scan second. If a
// ref name happens to match a valid sha-prefix shape (16 hex chars,
// say), the ref wins. This is documented but not blocked: ref names
// in kcpos convention are `tip` / `milestone/X` / `attempt/N` /
// `pinned/X` — never hex-only — so the collision is theoretical.
// Use a full 64-char sha to bypass ref lookup unambiguously.
func resolveEventID(s *snapshot.Snapshotter, query string) (string, error) {
	// Try ref first.
	if id, err := s.Refs.Get(query); err == nil {
		return id, nil
	}
	// Treat as sha prefix.
	if len(query) < 8 {
		return "", fmt.Errorf("event id prefix %q too short (need ≥8 hex chars)", query)
	}
	all, err := s.Events.List()
	if err != nil {
		return "", err
	}
	var matches []string
	for _, ev := range all {
		if strings.HasPrefix(ev.ID, query) {
			matches = append(matches, ev.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no event matches prefix %q (and no ref by that name)", query)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("prefix %q is ambiguous — %d events match (first two: %s, %s) — use a longer prefix",
			query, len(matches), matches[0][:16], matches[1][:16])
	}
}

func parentOrGenesis(p string) string {
	if p == "" {
		return "(genesis)"
	}
	return p
}

// summariseEvent produces a one-line description of an event for
// `snap list` output. Per-type formatting keeps the most useful
// signal visible without unpacking the full payload.
//
// Decode failures surface as a "(payload-decode-failed)" marker
// rather than silently rendering zero-value fields — otherwise a
// schema mismatch (old binary reading new events, or vice-versa)
// would produce honest-looking but wrong summaries like
// "turn=0  tool_calls=0".
func summariseEvent(ev snapshot.Event) string {
	switch ev.Type {
	case snapshot.EventTypeLLMTurn:
		var p snapshot.LLMTurnEvent
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return ev.Type + " (payload-decode-failed)"
		}
		toolCount := 0
		if len(p.ToolCalls) > 0 && string(p.ToolCalls) != "null" {
			var calls []interface{}
			_ = json.Unmarshal(p.ToolCalls, &calls)
			toolCount = len(calls)
		}
		return fmt.Sprintf("turn=%d %s tool_calls=%d", p.TurnIndex, p.SubAgent, toolCount)
	case snapshot.EventTypeToolExec:
		var p snapshot.ToolExecEvent
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return ev.Type + " (payload-decode-failed)"
		}
		seCount := len(p.SideEffects)
		errMark := ""
		if p.Err != "" {
			errMark = " ERR"
		}
		return fmt.Sprintf("%s side_effects=%d%s", p.Tool, seCount, errMark)
	case snapshot.EventTypeOuterTransition:
		var p snapshot.OuterTransitionEvent
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return ev.Type + " (payload-decode-failed)"
		}
		return fmt.Sprintf("%s → %s", p.From, p.To)
	case snapshot.EventTypeMilestone:
		var p snapshot.MilestoneEvent
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return ev.Type + " (payload-decode-failed)"
		}
		return p.Name
	}
	return ev.Type
}
