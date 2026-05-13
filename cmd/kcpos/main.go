// kcpos — KonceptOS coding agent CLI.
//
// Two operating modes:
//   1. AI agent (kcpos chat / kcpos with no args): start the LLM-driven REPL or
//      run a one-shot prompt. The agent has tools to manipulate the project's
//      hypergraph (graph_*), work-sessions (session_*), and verification
//      checkpoint (checkpoint_*).
//   2. Direct CLI (kcpos graph / session / checkpoint): manipulate the same
//      on-disk artifacts without going through the LLM. Useful for human
//      inspection, scripting, and CI.
//
// Both modes share the same internal/* packages — there is no logic
// duplication between the agent tools and the CLI subcommands.
package main

import (
	"fmt"
	"os"

	"github.com/creator915/Koncept_OS/cmd/kcpos/commands"
)

const usage = `kcpos — KonceptOS coding agent CLI

Usage:
  kcpos                            interactive AI agent (REPL)
  kcpos "task"                     one-shot AI agent (legacy form)
  kcpos chat [flags] [task]        explicit chat subcommand
  kcpos graph <verb> ...           direct CLI on K/graph.json
  kcpos session <verb> ...         work-session lifecycle (K/sessions/)
  kcpos checkpoint <verb> ...      verification ledger (K/checkpoint.json)
  kcpos doc protocol               print the runtime protocol (markdown)
  kcpos doc system                 print the full LLM system prompt
  kcpos doctor [--install] [-y]    detect / install external toolchain
  kcpos help                       this help
  kcpos <sub> --help               help for a subcommand

Environment:
  KCPOS_PROVIDER          deepseek (default) | moonshot | qwen | openai
  DEEPSEEK_API_KEY        required for chat with default provider
  KCPOS_MODEL             override default model for the chosen provider
`

func main() {
	if len(os.Args) < 2 {
		// Default: REPL chat
		os.Exit(commands.RunChat(nil))
	}

	sub := os.Args[1]
	rest := os.Args[2:]

	switch sub {
	case "chat":
		os.Exit(commands.RunChat(rest))
	case "graph":
		os.Exit(commands.RunGraph(rest))
	case "session":
		os.Exit(commands.RunSession(rest))
	case "checkpoint":
		os.Exit(commands.RunCheckpoint(rest))
	case "doc":
		os.Exit(commands.RunDoc(rest))
	case "doctor":
		os.Exit(commands.RunDoctor(rest))
	case "help", "-h", "--help":
		fmt.Print(usage)
		os.Exit(0)
	default:
		// Backward-compatible legacy form: `kcpos "task"` or `kcpos task words`.
		// If the first arg doesn't look like a subcommand name, treat the
		// whole arg list as a chat prompt.
		os.Exit(commands.RunChat(os.Args[1:]))
	}
}
