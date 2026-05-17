package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/creator915/Koncept_OS/internal/legacy/pbrun"
)

// RunPB is `kcpos pb-run <fixtureDir>` — one PB-class brownfield task
// end to end (characterize → LLM modify → lock re-check → hidden
// oracle). Prints a JSON scorecard. Exit 0 always (the score is the
// payload; a "MISS" is data, not a CLI error) unless setup failed.
func RunPB(args []string) int {
	if len(args) != 1 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stderr, "usage: kcpos pb-run <fixtureDir>")
		return 2
	}
	r := pbrun.Run(context.Background(), args[0])
	b, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(b))
	if r.Err != "" {
		return 1
	}
	return 0
}
