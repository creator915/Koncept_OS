package commands

import (
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"github.com/creator915/Koncept_OS/internal/app/workflow"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/creator915/Koncept_OS/internal/domain/checkpoint"
)

func RunCheckpoint(args []string) int {
	if len(args) == 0 {
		printCheckpointUsage()
		return 1
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "show":
		return runCheckpointShow(rest)
	case "add":
		return runCheckpointAdd(rest)
	case "freeze":
		return runCheckpointFreeze(rest)
	case "fill":
		return runCheckpointFill(rest)
	case "waive":
		return runCheckpointWaive(rest)
	case "-h", "--help", "help":
		printCheckpointUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown checkpoint subcommand: %s\n\n", sub)
		printCheckpointUsage()
		return 1
	}
}

func printCheckpointUsage() {
	fmt.Fprintln(os.Stderr, `kcpos checkpoint — verification ledger (K/checkpoint.json)

Usage:
  kcpos checkpoint show [--id CHK-XXX]
  kcpos checkpoint add --id CHK-XXX --severity must|should|waiver --description "..."
                       [--category CAT] [--reason "..."]   # reason required when severity=waiver
  kcpos checkpoint freeze
  kcpos checkpoint fill --id CHK-XXX --proof "src/x.go:42 Sym"
  kcpos checkpoint waive --id CHK-XXX --reason "..."`)
}

func runCheckpointShow(args []string) int {
	fs := flag.NewFlagSet("kcpos checkpoint show", flag.ExitOnError)
	id := fs.String("id", "", "specific item id (optional)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	c, err := persistence.LoadCheckpointOrInit(persistence.CheckpointDefaultPath)
	if err != nil {
		return printErr(err)
	}
	c.RecomputeSummary()
	if *id != "" {
		idx := c.FindItem(*id)
		if idx < 0 {
			return printErr(fmt.Errorf("item %s not found", *id))
		}
		printCheckpointItem(&c.Items[idx])
		return 0
	}
	printCheckpointFull(c)
	if c.Summary.FinalVerdict == checkpoint.VerdictFail {
		return 1
	}
	return 0
}

func runCheckpointAdd(args []string) int {
	fs := flag.NewFlagSet("kcpos checkpoint add", flag.ExitOnError)
	id := fs.String("id", "", "CHK-<token>")
	desc := fs.String("description", "", "what & how to verify")
	cat := fs.String("category", "", "optional grouping")
	sev := fs.String("severity", "", "must|should|waiver")
	reason := fs.String("reason", "", "waiver reason (required when severity=waiver)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := workflow.AddItem(persistence.CheckpointDefaultPath, *id, *desc, *cat, checkpoint.Severity(*sev), *reason); err != nil {
		return printErr(err)
	}
	fmt.Printf("added %s [%s]\n", *id, *sev)
	return 0
}

func runCheckpointFreeze(args []string) int {
	if err := workflow.Freeze(persistence.CheckpointDefaultPath); err != nil {
		return printErr(err)
	}
	c, _ := persistence.LoadCheckpoint(persistence.CheckpointDefaultPath)
	fmt.Printf("frozen at %s · %d items\n", c.FrozenAt.Format("2006-01-02 15:04:05"), len(c.Items))
	return 0
}

func runCheckpointFill(args []string) int {
	fs := flag.NewFlagSet("kcpos checkpoint fill", flag.ExitOnError)
	id := fs.String("id", "", "CHK-XXX")
	proof := fs.String("proof", "", "code proof: file:line + symbol")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := workflow.Fill(persistence.CheckpointDefaultPath, *id, *proof); err != nil {
		return printErr(err)
	}
	fmt.Printf("%s filled\n", *id)
	return 0
}

func runCheckpointWaive(args []string) int {
	fs := flag.NewFlagSet("kcpos checkpoint waive", flag.ExitOnError)
	id := fs.String("id", "", "CHK-XXX")
	reason := fs.String("reason", "", "why this is being excused")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := workflow.Waive(persistence.CheckpointDefaultPath, *id, *reason); err != nil {
		return printErr(err)
	}
	fmt.Printf("%s waived\n", *id)
	return 0
}

func printCheckpointFull(c *checkpoint.Checkpoint) {
	frozen := "no (mutable)"
	if c.Frozen {
		frozen = "yes · " + c.FrozenAt.Format("2006-01-02 15:04:05")
	}
	fmt.Printf("checkpoint: %s\n", c.Summary.FinalVerdict)
	fmt.Printf("  frozen: %s\n", frozen)
	fmt.Printf("  totalItems=%d · passed=%d · waived=%d · failed=%d\n",
		c.Summary.TotalItems, c.Summary.Passed, c.Summary.Waived, c.Summary.Failed)
	if len(c.Items) == 0 {
		fmt.Println("  (no items)")
		return
	}
	idx := make([]int, len(c.Items))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return c.Items[idx[a]].ID < c.Items[idx[b]].ID })
	fmt.Println("  items:")
	for _, i := range idx {
		it := &c.Items[i]
		state := "(unfilled)"
		switch {
		case it.Severity == checkpoint.SeverityWaiver:
			state = "WAIVED"
		case it.CodeProof != "":
			state = "FILLED"
		case it.Severity == checkpoint.SeverityMust:
			state = "MISSING"
		}
		fmt.Printf("    %s · [%s] · %s · %s\n", it.ID, it.Severity, state, truncateText(it.Description, 50))
	}
}

func printCheckpointItem(it *checkpoint.Item) {
	fmt.Printf("%s · [%s]\n", it.ID, it.Severity)
	fmt.Printf("  description: %s\n", it.Description)
	if it.Category != "" {
		fmt.Printf("  category: %s\n", it.Category)
	}
	if it.CodeProof != "" {
		fmt.Printf("  codeProof: %s\n", it.CodeProof)
	}
	if it.WaiverReason != "" {
		fmt.Printf("  waiverReason: %s\n", it.WaiverReason)
	}
	if !it.VerifiedAt.IsZero() {
		fmt.Printf("  verifiedAt: %s\n", it.VerifiedAt.Format("2006-01-02 15:04:05"))
	}
}
