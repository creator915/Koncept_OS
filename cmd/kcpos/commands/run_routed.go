package commands

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/creator915/Koncept_OS/internal/app/agent"
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"github.com/creator915/Koncept_OS/internal/llm/memory"
	"github.com/creator915/Koncept_OS/internal/llm/provider"
	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/snapshot"
	"github.com/creator915/Koncept_OS/internal/tools"
)

// RunRouted handles `kcpos run-routed [flags] "<task>"`. This is the
// Phase-2.C outer-Router entry point: kcpos drives the run by
// dispatching TypedValues through registered Handlers (see
// internal/router/outerflow.go), with LLM sub-loops scoped INSIDE each
// Handler instead of orchestrating the whole run.
//
// Difference vs `kcpos chat`:
//   - chat: classic LLM tool-loop, LLM picks any tool every turn.
//   - run-routed: outer Router picks the Handler by current TypedValue
//     type; each Handler may run a focused LLM sub-loop for its
//     narrow job. Determinism + feedback arcs replace open dispatch.
func RunRouted(args []string) int {
	fs := flag.NewFlagSet("kcpos run-routed", flag.ExitOnError)
	rootID := fs.String("session", "s_root", "root session id (will be created if absent)")
	specPath := fs.String("spec", "SPEC.md", "path to the project SPEC.md (or equivalent)")
	maxSteps := fs.Int("max-steps", 200, "Router transition cap (forward + feedback combined)")
	perHandlerIter := fs.Int("handler-max-iter", 60, "LLM iteration cap inside each creative Handler (2026-05-20 raised 30→60 after batch #3 confirmed 30 truncated mid-fix on TestError feedback loops)")
	tracePath := fs.String("trace", "", "if set, write the routed-run trace JSON to this path")
	maxAttempts := fs.Int("max-attempts", 1, "Phase 7 auto-retry budget: how many times to rollback+retry on terminal Outer.Obstacle (1 = single attempt, no retry; ≥2 enables snapshot-driven retry with lesson injection)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `kcpos run-routed — outer-Router (Phase-2.C) driver

Usage:
  kcpos run-routed "task description"
  kcpos run-routed --session s_demo --spec docs/SPEC.md "build a pong game"
  kcpos run-routed --trace tests/routed-trace.json "task"

Flags:`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	task := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if task == "" {
		fmt.Fprintln(os.Stderr, "error: task description required (after flags)")
		fs.Usage()
		return 2
	}
	if err := session.ValidateID(*rootID); err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid --session %q: %v\n", *rootID, err)
		return 2
	}

	// Ensure the root session exists on disk. We auto-create it
	// because the outer Router infers state from the session file —
	// without one it would just sit at Outer.Task forever.
	if !persistence.ExistsSession(persistence.SessionDefaultDir, *rootID) {
		s := session.New(*rootID, "", task, session.Input{})
		s.Status = session.StatusActive
		if err := persistence.SaveSession(persistence.SessionDefaultDir, s); err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot create root session %q: %v\n", *rootID, err)
			return 1
		}
		_ = persistence.SetFocus(persistence.SessionDefaultDir, *rootID)
	}

	cfg, err := provider.ProviderFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	client := transport.NewClient(cfg)

	cwd, _ := os.Getwd()
	tr := memory.New(cwd)

	deps := &agent.OuterDeps{
		Client:            client,
		ToolSet:           tools.Builtins(),
		RootSessionID:     *rootID,
		SpecPath:          *specPath,
		TaskDescription:   task,
		PerHandlerMaxIter: *perHandlerIter,
		MaxRouterSteps:    *maxSteps,
		Transcript:        tr,
	}

	fmt.Fprintf(os.Stderr, "%s[routed: provider=%s model=%s session=%s spec=%s transcript=%s]\n",
		agent.SessionStartBanner(), providerName(), cfg.Model, *rootID, *specPath, tr.ID)

	// In-progress trace persistor — saves trace.json after every
	// Router transition so a SIGKILL (e.g. 1800s `timeout` wrapper)
	// still leaves the most-recent state on disk.
	var persist agent.TracePersistor
	if *tracePath != "" {
		_ = os.MkdirAll(filepath.Dir(*tracePath), 0o755)
		persist = func(snap agent.RoutedRunResult) {
			b, err := json.MarshalIndent(snap, "", "  ")
			if err == nil {
				_ = os.WriteFile(*tracePath, b, 0o644)
			}
		}
	}

	ctx := context.Background()
	// Phase 7 — attach a workdir-scoped Snapshotter to ctx so every
	// state-mutating tool call captures its side_effects, every
	// Outer.* transition emits an event, and (with --max-attempts ≥2)
	// failed branches get archived + lessons synthesized for the
	// next attempt's system prompt.
	snap := snapshot.NewSnapshotter(cwd)
	ctx = snapshot.WithSnapshotter(ctx, snap)
	result, runErr := agent.RunRoutedTurnWithRetry(ctx, deps, persist, snap, *maxAttempts)
	// Final transcript save after Router returns; OnProgress already
	// saves per-LLM-turn but this catches any tail.
	_ = tr.Save()
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "router error:", runErr)
		return 1
	}

	// Final trace.json — the persist callback also wrote this on each
	// transition, but the closing snapshot has the definitive
	// TerminalType + TerminalPayload.
	if *tracePath != "" {
		b, _ := json.MarshalIndent(result, "", "  ")
		_ = os.WriteFile(*tracePath, b, 0o644)
	}

	fmt.Fprintf(os.Stderr, "[routed terminal=%s steps=%d]\n", result.TerminalType, result.Steps)
	if result.TerminalType == "Outer.Finished" {
		fmt.Println("FINISHED")
		return 0
	}
	fmt.Println("OBSTACLE")
	if len(result.TerminalPayload) > 0 {
		fmt.Println(string(result.TerminalPayload))
	}
	return 1
}
