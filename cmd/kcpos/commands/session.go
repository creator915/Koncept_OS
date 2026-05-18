package commands

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/creator915/Koncept_OS/internal/app/workflow"
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
)

func RunSession(args []string) int {
	if len(args) == 0 {
		printSessionUsage()
		return 1
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list":
		return runSessionList(rest)
	case "show":
		return runSessionShow(rest)
	case "create":
		return runSessionCreate(rest)
	case "start":
		return runSessionStart(rest)
	case "status":
		return runSessionStatus(rest)
	case "focus":
		return runSessionFocus(rest)
	case "delete":
		return runSessionDelete(rest)
	case "resume":
		return runSessionResume(rest)
	case "-h", "--help", "help":
		printSessionUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown session subcommand: %s\n\n", sub)
		printSessionUsage()
		return 1
	}
}

func printSessionUsage() {
	fmt.Fprintln(os.Stderr, `kcpos session — KonceptOS work-session lifecycle (K/sessions/)

Usage:
  kcpos session list [--status waiting|active|finished]
  kcpos session show <id>
  kcpos session create --id ID --task "..." [--parent ID]
  kcpos session start  --id ID --task "..." [--parent ID]   # create+active+focus atomic
  kcpos session status --id ID --to active|finished
  kcpos session focus [--id ID | --clear]
  kcpos session delete --id ID                       # rolls back graphDiff
  kcpos session resume <id>                          # focus + start REPL`)
}

func runSessionList(args []string) int {
	fs := flag.NewFlagSet("kcpos session list", flag.ExitOnError)
	status := fs.String("status", "", "filter by status")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ids, err := persistence.ListSessions(persistence.SessionDefaultDir)
	if err != nil {
		return printErr(err)
	}
	if len(ids) == 0 {
		fmt.Println("no sessions")
		return 0
	}
	for _, id := range ids {
		s, err := persistence.LoadSession(persistence.SessionDefaultDir, id)
		if err != nil {
			fmt.Printf("  %s · [load error: %v]\n", id, err)
			continue
		}
		if *status != "" && string(s.Status) != *status {
			continue
		}
		parent := s.Parent
		if parent == "" {
			parent = "<root>"
		}
		fmt.Printf("  %s · %s · parent=%s · children=%d · %s\n",
			s.ID, s.Status, parent, len(s.Children), truncateText(s.Task, 60))
	}
	return 0
}

func runSessionShow(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: kcpos session show <id>")
		return 1
	}
	id, err := session.NormalizeID(args[0])
	if err != nil {
		return printErr(err)
	}
	s, err := persistence.LoadSession(persistence.SessionDefaultDir, id)
	if err != nil {
		return printErr(err)
	}
	fmt.Printf("session %s\n", s.ID)
	fmt.Printf("  status: %s\n", s.Status)
	fmt.Printf("  task: %s\n", s.Task)
	if s.Parent == "" {
		fmt.Println("  parent: <root>")
	} else {
		fmt.Printf("  parent: %s\n", s.Parent)
	}
	if len(s.Children) > 0 {
		sorted := append([]string(nil), s.Children...)
		sort.Strings(sorted)
		fmt.Printf("  children: %s\n", strings.Join(sorted, ", "))
	}
	if len(s.Input.Signatures) > 0 {
		fmt.Printf("  input.signatures: %s\n", strings.Join(s.Input.Signatures, ", "))
	}
	if len(s.Input.Context) > 0 {
		fmt.Printf("  input.context: %s\n", strings.Join(s.Input.Context, ", "))
	}
	added := len(s.Output.GraphDiff.Added.Attributes) + len(s.Output.GraphDiff.Added.Objects)
	mod := len(s.Output.GraphDiff.Modified.Attributes) + len(s.Output.GraphDiff.Modified.Objects)
	rem := len(s.Output.GraphDiff.Removed.Attributes) + len(s.Output.GraphDiff.Removed.Objects)
	if added+mod+rem > 0 {
		fmt.Printf("  graphDiff: added=%d modified=%d removed=%d\n", added, mod, rem)
	}
	return 0
}

func runSessionCreate(args []string) int {
	fs := flag.NewFlagSet("kcpos session create", flag.ExitOnError)
	id := fs.String("id", "", "session id (s_ prefix auto-prepended)")
	parent := fs.String("parent", "", "parent session id (empty = root)")
	task := fs.String("task", "", "task description")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *id == "" || *task == "" {
		fmt.Fprintln(os.Stderr, "--id and --task required")
		return 1
	}
	normID, err := session.NormalizeID(*id)
	if err != nil {
		return printErr(err)
	}
	s, err := workflow.Create(persistence.SessionDefaultDir, normID, *parent, *task, session.Input{})
	if err != nil {
		return printErr(err)
	}
	fmt.Printf("created %s · status=%s\n", s.ID, s.Status)
	return 0
}

func runSessionStart(args []string) int {
	fs := flag.NewFlagSet("kcpos session start", flag.ExitOnError)
	id := fs.String("id", "", "session id")
	parent := fs.String("parent", "", "parent session id (empty = root)")
	task := fs.String("task", "", "task description")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *id == "" || *task == "" {
		fmt.Fprintln(os.Stderr, "--id and --task required")
		return 1
	}
	normID, err := session.NormalizeID(*id)
	if err != nil {
		return printErr(err)
	}
	s, err := workflow.Start(persistence.SessionDefaultDir, normID, *parent, *task, session.Input{})
	if err != nil {
		return printErr(err)
	}
	fmt.Printf("started %s · status=%s · focused\n", s.ID, s.Status)
	return 0
}

func runSessionStatus(args []string) int {
	fs := flag.NewFlagSet("kcpos session status", flag.ExitOnError)
	id := fs.String("id", "", "session id")
	to := fs.String("to", "", "target status: active | finished")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *id == "" || *to == "" {
		fmt.Fprintln(os.Stderr, "--id and --to required")
		return 1
	}
	normID, err := session.NormalizeID(*id)
	if err != nil {
		return printErr(err)
	}
	s, err := workflow.SetStatus(persistence.SessionDefaultDir, normID, session.Status(*to))
	if err != nil {
		return printErr(err)
	}
	fmt.Printf("%s status → %s\n", s.ID, s.Status)
	return 0
}

func runSessionFocus(args []string) int {
	fs := flag.NewFlagSet("kcpos session focus", flag.ExitOnError)
	id := fs.String("id", "", "session id to focus")
	clear := fs.Bool("clear", false, "clear focus")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *clear {
		if err := persistence.SetFocus(persistence.SessionDefaultDir, ""); err != nil {
			return printErr(err)
		}
		fmt.Println("focus cleared")
		return 0
	}
	if *id == "" {
		// Show current focus
		cur, err := persistence.GetFocus(persistence.SessionDefaultDir)
		if err != nil {
			return printErr(err)
		}
		if cur == "" {
			fmt.Println("(no focus)")
		} else {
			fmt.Printf("focus: %s\n", cur)
		}
		return 0
	}
	normID, err := session.NormalizeID(*id)
	if err != nil {
		return printErr(err)
	}
	s, err := persistence.LoadSession(persistence.SessionDefaultDir, normID)
	if err != nil {
		return printErr(err)
	}
	if s.Status != session.StatusActive {
		fmt.Fprintf(os.Stderr, "error: %s status is %s, must be active to focus\n", normID, s.Status)
		return 1
	}
	if err := persistence.SetFocus(persistence.SessionDefaultDir, normID); err != nil {
		return printErr(err)
	}
	fmt.Printf("focus → %s\n", normID)
	return 0
}

func runSessionDelete(args []string) int {
	fs := flag.NewFlagSet("kcpos session delete", flag.ExitOnError)
	id := fs.String("id", "", "session id to delete + roll back")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *id == "" {
		fmt.Fprintln(os.Stderr, "--id required")
		return 1
	}
	normID, err := session.NormalizeID(*id)
	if err != nil {
		return printErr(err)
	}
	cwd, _ := os.Getwd()
	graphPath := cwd + "/K/graph.json"
	deleted, err := workflow.Rollback(persistence.SessionDefaultDir, graphPath, normID)
	if err != nil {
		return printErr(err)
	}
	if len(deleted) == 0 {
		fmt.Printf("no session %s found\n", normID)
		return 0
	}
	fmt.Printf("rolled back %d session(s): %s\n", len(deleted), strings.Join(deleted, ", "))
	return 0
}

// runSessionResume loads a session, ensures it is active, focuses it, then
// starts a chat REPL — convenient for picking up where you left off.
func runSessionResume(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: kcpos session resume <id>")
		return 1
	}
	id, err := session.NormalizeID(args[0])
	if err != nil {
		return printErr(err)
	}
	s, err := persistence.LoadSession(persistence.SessionDefaultDir, id)
	if err != nil {
		return printErr(err)
	}
	switch s.Status {
	case session.StatusWaiting:
		// Auto-promote to active so focus is allowed
		if _, err := workflow.SetStatus(persistence.SessionDefaultDir, id, session.StatusActive); err != nil {
			return printErr(err)
		}
		fmt.Fprintf(os.Stderr, "[promoted %s to active]\n", id)
	case session.StatusFinished:
		fmt.Fprintf(os.Stderr, "error: session %s is already finished — cannot resume; create a fresh session instead\n", id)
		return 1
	}
	if err := persistence.SetFocus(persistence.SessionDefaultDir, id); err != nil {
		return printErr(err)
	}
	return runChatWithPrompt("", "", id, "")
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
