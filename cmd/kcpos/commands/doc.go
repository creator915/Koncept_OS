package commands

import (
	"fmt"
	"os"

	"github.com/creator915/Koncept_OS/internal/app/agent"
	"github.com/creator915/Koncept_OS/internal/domain/protocol"
)

// RunDoc prints documentation generated from kcpos's internal state.
// `kcpos doc protocol` is the v9.0 successor to the old CLAUDE.md —
// the runtime protocol as a single source of truth.
func RunDoc(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: kcpos doc <protocol|system>")
		return 2
	}
	switch args[0] {
	case "protocol":
		fmt.Print(protocol.Describe())
		return 0
	case "system":
		fmt.Print(agent.SystemPrompt)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown doc topic %q (try: protocol, system)\n", args[0])
		return 2
	}
}
