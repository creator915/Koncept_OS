package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/creator915/Koncept_OS/internal/agent"
	"github.com/creator915/Koncept_OS/internal/llm"
	"github.com/creator915/Koncept_OS/internal/repl"
	"github.com/creator915/Koncept_OS/internal/transcript"
)

// runChat handles `kcpos chat [flags] [prompt...]`. The prompt may also be
// piped on stdin. Empty prompt → REPL mode.
func runChat(args []string) int {
	fs := flag.NewFlagSet("kcpos chat", flag.ExitOnError)
	resume := fs.String("resume", "", "resume a chat transcript: <id> or 'latest'")
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
	return runChatWithPrompt(*resume, prompt, "")
}

// runChatWithPrompt is the shared entry point for chat-mode dispatch. focusID
// is non-empty only when the caller (e.g. `kcpos session resume`) wants the
// REPL to start with a KonceptOS session focused.
func runChatWithPrompt(resumeID, prompt, focusID string) int {
	cfg, err := llm.ProviderFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	var tr *transcript.Transcript
	if resumeID != "" {
		tr, err = transcript.Load(cwd, resumeID)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
	} else {
		tr = transcript.New(cwd)
	}

	client := llm.NewClient(cfg)
	fmt.Fprintf(os.Stderr, "%s[provider: %s, model: %s]\n", agent.SessionStartBanner(), providerName(), cfg.Model)
	if focusID != "" {
		fmt.Fprintf(os.Stderr, "%s[KonceptOS focus: %s]\n", agent.Stamp(), focusID)
	}
	ctx := context.Background()

	if prompt != "" {
		// one-shot
		if err := agent.RunTurn(ctx, client, &tr.Messages, prompt); err != nil {
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
