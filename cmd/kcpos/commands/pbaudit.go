package commands

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/creator915/Koncept_OS/internal/legacy/pbaudit"
)

// RunPBAudit is `kcpos pb-audit <transcript.json>` — the deterministic
// post-run cheat detector (forensic 手段5). Exit 0 = clean, exit 3 =
// TAINTED (the batch controller must VOID the score), exit 2 = usage /
// read error. Prints a JSON Report.
func RunPBAudit(args []string) int {
	if len(args) != 1 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stderr, "usage: kcpos pb-audit <transcript.json>\n"+
			"  exit 0 = clean, 3 = TAINTED (score VOID), 2 = error")
		return 2
	}
	raw, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	rep, err := pbaudit.Audit(raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	b, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(b))
	if rep.Tainted {
		fmt.Fprintf(os.Stderr, "TAINTED: %d finding(s) — this run's PB score is VOID\n", len(rep.Findings))
		return 3
	}
	fmt.Fprintln(os.Stderr, "clean: no enumerated cheat vector detected (detection, not proof of non-cheating)")
	return 0
}
