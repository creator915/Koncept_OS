package commands

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/creator915/Koncept_OS/internal/app/agent"
	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/llm/provider"
	"github.com/creator915/Koncept_OS/internal/app/repl"
	"github.com/creator915/Koncept_OS/internal/llm/memory"
	core "github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// runChat handles `kcpos chat [flags] [prompt...]`. The prompt may also be
// piped on stdin. Empty prompt → REPL mode.
func RunChat(args []string) int {
	fs := flag.NewFlagSet("kcpos chat", flag.ExitOnError)
	resume := fs.String("resume", "", "resume a chat transcript: <id> or 'latest'")
	contract := fs.String("contract", "", "capability contract preset (e.g. 'blackbox'); fail-closed — an unknown name aborts rather than running unrestricted. Use for any harnessed/untrusted run.")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `kcpos chat — interactive AI agent

Usage:
  kcpos chat                              REPL with fresh transcript
  kcpos chat "task"                       one-shot
  kcpos chat --resume latest              REPL, continue most recent transcript
  kcpos chat --resume latest "task"       one-shot with prior context

Flags:`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		// stdin pipe support
		if stat, _ := os.Stdin.Stat(); (stat.Mode()&os.ModeCharDevice) == 0 {
			b, _ := io.ReadAll(os.Stdin)
			prompt = strings.TrimSpace(string(b))
		}
	}
	return runChatWithPrompt(*resume, prompt, "", *contract)
}

// runChatWithPrompt is the shared entry point for chat-mode dispatch. focusID
// is non-empty only when the caller (e.g. `kcpos session resume`) wants the
// REPL to start with a KonceptOS session focused.
func runChatWithPrompt(resumeID, prompt, focusID, contract string) int {
	cfg, err := provider.ProviderFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	var tr *memory.Transcript
	if resumeID != "" {
		tr, err = memory.Load(cwd, resumeID)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
	} else {
		tr = memory.New(cwd)
	}

	client := transport.NewClient(cfg)
	fmt.Fprintf(os.Stderr, "%s[provider: %s, model: %s]\n", agent.SessionStartBanner(), providerName(), cfg.Model)
	if focusID != "" {
		fmt.Fprintf(os.Stderr, "%s[KonceptOS focus: %s]\n", agent.Stamp(), focusID)
	}
	ctx := context.Background()

	// Capability contract (forensic §7.2): fail-closed. An unknown name
	// aborts — we never silently fall back to the unrestricted (nil
	// Caps) gate, since that silent fallback is exactly what let the
	// 2026-05-17 PB-kcpos runs cheat through `bash`.
	var runOpts agent.RunOptions
	if contract != "" {
		caps, ok := core.PresetByName(contract)
		if !ok {
			fmt.Fprintf(os.Stderr, "error: unknown --contract %q (fail-closed; refusing to run with the capability gate disabled)\n", contract)
			return 1
		}
		if prompt == "" {
			fmt.Fprintln(os.Stderr, "error: --contract requires a one-shot prompt; REPL contract scoping is not yet wired (fail-closed, see forensic §7.3)")
			return 1
		}
		runOpts.Caps = caps
		fmt.Fprintf(os.Stderr, "%s[contract: %s — %d capabilities, deny-by-default; bash/exec excluded]\n", agent.Stamp(), contract, len(caps))
	}

	if prompt != "" {
		// one-shot. Persist the transcript after every completed turn so
		// a timeout/SIGTERM kill mid-run still leaves a JSON log on disk
		// (pre-fix: save happened only AFTER the whole loop returned).
		runOpts.OnProgress = func() { _ = tr.Save() }
		if err := agent.RunTurnOpts(ctx, client, &tr.Messages, prompt, runOpts); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			_ = tr.Save()
			return 1
		}
		if err := tr.Save(); err != nil {
			fmt.Fprintln(os.Stderr, "warning: failed to save transcript:", err)
		} else {
			fmt.Fprintf(os.Stderr, "[transcript saved: %s]\n", tr.Path())
		}
		return 0
	}
	// REPL
	if err := repl.Run(ctx, client, tr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func providerName() string {
	if v := os.Getenv("KCPOS_PROVIDER"); v != "" {
		return v
	}
	return "deepseek"
}
