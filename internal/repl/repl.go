package repl

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/chzyer/readline"
	"github.com/creator915/Koncept_OS/internal/agent"
	"github.com/creator915/Koncept_OS/internal/llm"
	"github.com/creator915/Koncept_OS/internal/transcript"
)

const (
	reset  = "\x1b[0m"
	bold   = "\x1b[1m"
	dim    = "\x1b[2m"
	red    = "\x1b[31m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	blue   = "\x1b[34m"
)

// Run starts the interactive REPL. Each input line is fed to the agent;
// output is streamed by the agent itself. History persists to .kcpos/history.
func Run(ctx context.Context, client *llm.Client, tr *transcript.Transcript) error {
	histPath := filepath.Join(filepath.Dir(tr.Dir), "history")
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          bold + green + "❯ " + reset,
		HistoryFile:     histPath,
		HistoryLimit:    1000,
		InterruptPrompt: "^C",
		EOFPrompt:       "",
	})
	if err != nil {
		return fmt.Errorf("readline init: %w", err)
	}
	defer rl.Close()

	printBanner(tr)

	for {
		line, err := rl.Readline()
		if err == readline.ErrInterrupt {
			if line == "" {
				break
			}
			continue
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "%sread error: %v%s\n", red, err, reset)
			break
		}
		prompt := strings.TrimSpace(line)
		if prompt == "" {
			continue
		}

		handled, exit := handleSlashCommand(prompt)
		if exit {
			break
		}
		if handled {
			continue
		}

		fmt.Fprintln(os.Stderr)
		if err := agent.RunTurn(ctx, client, &tr.Messages, prompt); err != nil {
			fmt.Fprintf(os.Stderr, "%serror:%s %v\n", red, reset, err)
		}
		if err := tr.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "%swarning: failed to save transcript: %v%s\n", yellow, err, reset)
		}
		printSeparator()
	}
	fmt.Fprintf(os.Stderr, "%s[transcript saved: %s]%s\n", dim, tr.Path(), reset)
	return nil
}

func printBanner(tr *transcript.Transcript) {
	state := "new"
	if len(tr.Messages) > 0 {
		state = fmt.Sprintf("resumed · %d messages", len(tr.Messages))
	}
	fmt.Fprintf(os.Stderr, "%s%skcpos%s · transcript %s · %s\n", bold, blue, reset, tr.ID, state)
	fmt.Fprintf(os.Stderr, "%stype a task or /help · Ctrl+D to quit · ↑↓ for history%s\n\n", dim, reset)
}

func printSeparator() {
	fmt.Fprintf(os.Stderr, "\n%s───%s\n\n", dim, reset)
}

// handleSlashCommand returns (handled, exit).
//   - handled=true means the input was a slash command (don't pass to agent)
//   - exit=true means the REPL should leave its main loop
func handleSlashCommand(input string) (handled, exit bool) {
	if !strings.HasPrefix(input, "/") {
		return false, false
	}
	parts := strings.Fields(input)
	switch parts[0] {
	case "/exit", "/quit":
		return true, true
	case "/help":
		printHelp()
		return true, false
	default:
		fmt.Fprintf(os.Stderr, "%sunknown command: %s · /help for list%s\n", yellow, parts[0], reset)
		return true, false
	}
}

func printHelp() {
	fmt.Fprintln(os.Stderr, "  /help         show this help")
	fmt.Fprintln(os.Stderr, "  /exit, /quit  leave the REPL")
	fmt.Fprintln(os.Stderr, "  Ctrl+D        leave the REPL (same as /exit)")
	fmt.Fprintln(os.Stderr, "  Ctrl+C        cancel current input line")
	fmt.Fprintln(os.Stderr, "  ↑ / ↓         walk through history")
	fmt.Fprintln(os.Stderr, "  Ctrl+R        reverse-search history")
}
