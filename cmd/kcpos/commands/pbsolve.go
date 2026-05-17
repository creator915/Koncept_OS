package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/creator915/Koncept_OS/internal/legacy/pbkcpos"
)

// RunPBSolve is `kcpos pb-solve <instanceID> <lang> <workRoot>` — runs
// the ProgramBench reconstruction orchestrator for ONE task (recover
// black box → reconstruct → verify → export submission.tar.gz). It
// does NOT run `programbench eval`; that is a separate step on the
// produced submission. Prints a JSON Result.
func RunPBSolve(args []string) int {
	if len(args) < 3 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stderr, "usage: kcpos pb-solve <instanceID> <language> <workRoot>")
		return 2
	}
	r := pbkcpos.Reconstruct(context.Background(), pbkcpos.Config{
		InstanceID: args[0],
		Language:   args[1],
		WorkRoot:   args[2],
	})
	b, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(b))
	if r.Err != "" {
		return 1
	}
	return 0
}
