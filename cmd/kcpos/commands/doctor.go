package commands

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/creator915/Koncept_OS/internal/runtime/preflight"
)

// runDoctor is the `kcpos doctor` subcommand. Lists every tool kcpos
// knows about, whether it's detected, its version, and the install hint
// for any missing ones. Optionally installs them when `--install` is
// passed.
//
// Exit codes:
//
//	0 — all installable tools present (uninstallable missing ones don't fail)
//	1 — at least one tool kcpos can install is missing
//	2 — usage error
func RunDoctor(args []string) int {
	install := false
	autoYes := false
	for _, a := range args {
		switch a {
		case "--install":
			install = true
		case "--yes", "-y":
			autoYes = true
		case "-h", "--help":
			fmt.Print(doctorUsage)
			return 0
		default:
			fmt.Fprintf(os.Stderr, "kcpos doctor: unknown flag %q\n", a)
			fmt.Fprint(os.Stderr, doctorUsage)
			return 2
		}
	}

	results := preflight.DetectAll()
	printDoctorTable(os.Stdout, results)

	missingInstallable := []preflight.Tool{}
	for _, r := range results {
		if r.Found {
			continue
		}
		// Only count tools that have an install recipe — Go / Python /
		// system packages without an installer get a hint but not a
		// non-zero exit (user has to install them by hand).
		if isInstallable(r.Tool) {
			missingInstallable = append(missingInstallable, r.Tool)
		}
	}

	if len(missingInstallable) == 0 {
		fmt.Println()
		fmt.Println("All installable tools present.")
		return 0
	}

	if !install {
		fmt.Println()
		fmt.Printf("%d installable tool(s) missing. Re-run with --install to fix:\n",
			len(missingInstallable))
		for _, t := range missingInstallable {
			fmt.Printf("  kcpos doctor --install      # would install: %s\n", t)
		}
		return 1
	}

	// --install path: run installs in registry order, surfacing each
	// step to stdout. Interactive prompt unless --yes was passed.
	for _, t := range missingInstallable {
		hint := preflight.Hint(t)
		fmt.Printf("\n→ Installing %s\n  %s\n", t, hint)
		opts := preflight.InstallOptions{
			Mode:    preflight.ModeInteractive,
			Stdout:  os.Stdout,
			Stderr:  os.Stderr,
			Confirm: stdinConfirm,
		}
		if autoYes {
			opts.Mode = preflight.ModeAutoConfirm
		}
		if err := preflight.Install(t, opts); err != nil {
			fmt.Fprintf(os.Stderr, "install %s failed: %v\n", t, err)
			return 1
		}
		r := preflight.Detect(t)
		fmt.Printf("  ✓ %s installed (version %s)\n", t, r.Version)
	}

	fmt.Println()
	fmt.Println("Re-checking environment:")
	printDoctorTable(os.Stdout, preflight.DetectAll())
	return 0
}

const doctorUsage = `kcpos doctor — detect and (optionally) install kcpos's external toolchain

Usage:
  kcpos doctor                     list detected tools with versions
  kcpos doctor --install           install any installable tools that are missing
  kcpos doctor --install --yes     skip the interactive y/N prompt

Tools detected:
  node, npm, npx, tsc, go, python3, py_compile, playwright, chromium

Exit codes:
  0  all installable tools present
  1  at least one installable tool is missing (run --install to fix)
  2  usage error
`

// printDoctorTable renders the detect results as an aligned table.
// Columns: tool | found | version | path (truncated) | hint (when missing)
func printDoctorTable(w io.Writer, results []preflight.Result) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TOOL\tFOUND\tVERSION\tDETAILS")
	for _, r := range results {
		mark := "✗"
		if r.Found {
			mark = "✓"
		}
		details := ""
		switch {
		case r.Found && r.Path != "":
			details = truncatePath(r.Path)
		case !r.Found:
			// Prefer the dynamic Err message (contains broken-symlink
			// detection + checked-roots context) over the static Hint
			// when probe ran. Falls back to Hint for tools that don't
			// run a probe (e.g. node-module tools with no candidates).
			if r.Err != nil {
				details = r.Err.Error()
			} else {
				details = preflight.Hint(r.Tool)
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Tool, mark, fallback(r.Version, "—"), details)
	}
	tw.Flush()
}

func fallback(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// truncatePath shortens long absolute paths for table display.
func truncatePath(p string) string {
	const max = 70
	if len(p) <= max {
		return p
	}
	return "…" + p[len(p)-(max-1):]
}

// isInstallable mirrors registry.installer != nil without exporting
// the registry. Hard-codes the install-capable subset.
func isInstallable(t preflight.Tool) bool {
	switch t {
	case preflight.Node, preflight.NPM, preflight.NPX,
		preflight.Playwright, preflight.Chromium:
		return true
	}
	return false
}

// stdinConfirm asks the user via stdin. Used by ModeInteractive.
func stdinConfirm(t preflight.Tool, hint string) bool {
	fmt.Printf("  Install %s now? [y/N]: ", t)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}
