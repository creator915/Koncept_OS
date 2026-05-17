package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/creator915/Koncept_OS/internal/legacy/characterize"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// RunCharacterize is the `kcpos characterize` subcommand — the
// brownfield characterization front stage as a standalone entry (屎山
// 代码维护Agent设计文档 v1.0). It takes an UNTRUSTED legacy artifact,
// synthesizes input probes, runs them against the artifact, transcribes
// the observed behavior into a golden lock, and persists the
// Finite/Reproducible evidence + conditional-confidence Oracle into the
// object's bundle. It judges nothing — char tests have no moral
// authority (Feathers 6.6); it RECORDS what the code currently does so
// later modification can be checked for behavior preservation.
//
// This is also the document's prescribed "first real agent task": its
// output (and what it fails to characterize) drives the v1.1 iteration.
//
// Exit codes:
//
//	0 — a lock was written (even a partial one — honest partial > fake complete)
//	1 — engine error (no probes, harness unavailable, unreadable artifact)
//	2 — usage error
func RunCharacterize(args []string) int {
	var (
		file       string
		symbol     string
		lang       string
		producesCS string
		describe   string
		signature  string
		stability  = 3
	)
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			fmt.Print(characterizeUsage)
			return 0
		case a == "--symbol":
			i++
			if i >= len(args) {
				return charUsageErr("--symbol needs a value")
			}
			symbol = args[i]
		case a == "--lang":
			i++
			if i >= len(args) {
				return charUsageErr("--lang needs a value")
			}
			lang = args[i]
		case a == "--produces":
			i++
			if i >= len(args) {
				return charUsageErr("--produces needs a comma-separated value")
			}
			producesCS = args[i]
		case a == "--describe":
			i++
			if i >= len(args) {
				return charUsageErr("--describe needs a value")
			}
			describe = args[i]
		case a == "--signature":
			i++
			if i >= len(args) {
				return charUsageErr("--signature needs a value")
			}
			signature = args[i]
		case a == "--stability":
			i++
			if i >= len(args) {
				return charUsageErr("--stability needs an integer")
			}
			v, err := strconv.Atoi(args[i])
			if err != nil || v < 1 {
				return charUsageErr("--stability must be a positive integer")
			}
			stability = v
		case strings.HasPrefix(a, "-"):
			return charUsageErr("unknown flag " + a)
		default:
			if file != "" {
				return charUsageErr("more than one artifact path given")
			}
			file = a
		}
		i++
	}
	if file == "" {
		return charUsageErr("missing legacy artifact path")
	}

	absFile, err := filepath.Abs(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "characterize: resolve path: %v\n", err)
		return 1
	}
	body, err := os.ReadFile(absFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "characterize: read artifact: %v\n", err)
		return 1
	}
	if lang == "" {
		lang = langFromExt(absFile)
		if lang == "" {
			return charUsageErr("could not infer --lang from extension; pass --lang explicitly (python|javascript|typescript|html)")
		}
	}
	base := strings.TrimSuffix(filepath.Base(absFile), filepath.Ext(absFile))
	objectID := symbol
	if objectID == "" {
		objectID = sanitizeID(base)
	}
	if symbol == "" {
		symbol = objectID
	}
	var produces []string
	for _, p := range strings.Split(producesCS, ",") {
		if s := strings.TrimSpace(p); s != "" {
			produces = append(produces, s)
		}
	}
	if len(produces) == 0 {
		// The harness needs at least one output port to read. Default to
		// a single "result" port — honest about the assumption (it goes
		// into the run's outcomes), and overridable via --produces.
		produces = []string{"result"}
	}
	if describe == "" {
		describe = "Legacy artifact; behavior unknown and untrusted. Characterize current behavior by observation."
	}

	// Default extractor: a single produced port reads the call's return
	// value; multiple ports read return.<port>. Without this the harness
	// defaults to "global" and a return-valued legacy function locks
	// nothing. Overridable later via a richer flag if needed.
	portObs := map[string]string{}
	if len(produces) == 1 {
		portObs[produces[0]] = "return"
	} else {
		for _, p := range produces {
			portObs[p] = "return." + p
		}
	}

	codeHash := core.HashSource(string(body))
	env := map[string]string{
		"lang": lang,
		"os":   runtime.GOOS,
		"arch": runtime.GOARCH,
	}

	fmt.Printf("characterize: %s\n", absFile)
	fmt.Printf("  symbol=%s  objectId=%s  lang=%s  produces=%v\n", symbol, objectID, lang, produces)
	fmt.Printf("  artifact hash=%s  stability=%d×\n", shortHash12(codeHash), stability)
	fmt.Println("  → synthesizing probes (no trusted expectations) …")

	res, err := characterize.Characterize(context.Background(), characterize.CharRequest{
		ObjectID:     objectID,
		ImplSymbol:   symbol,
		Lang:         lang,
		ArtifactPath: absFile,
		CodeHash:     codeHash,
		Signature:    signature,
		Description:     describe,
		Produces:        produces,
		PortObservation: portObs,
		Environment:     env,
		Stability:       stability,
		IntroducedBy:    "kcpos characterize",
	}, characterize.DefaultSynthesize(), characterize.RealHarness())
	if err != nil {
		fmt.Fprintf(os.Stderr, "characterize: %v\n", err)
		return 1
	}

	if err := characterize.Persist(objectID, res); err != nil {
		fmt.Fprintf(os.Stderr, "characterize: persist lock: %v\n", err)
		return 1
	}

	// Honest report (设计文档 Part 10.2): covered / uncovered /
	// per-dimension confidence / assumptions — never a single score.
	fmt.Println()
	fmt.Printf("LOCKED behavior  : %d case(s)\n", len(res.Lock.Cases))
	fmt.Printf("UNLOCKED (未覆盖) : %d probe(s)\n", len(res.Lock.Unlocked))
	for _, u := range res.Lock.Unlocked {
		fmt.Printf("  - %s : %s\n", u, res.Finite.Outcomes[u])
	}
	fmt.Println("confidence (per-dimension, NOT a single score):")
	for _, line := range res.Oracle.Confidence.Report() {
		fmt.Printf("  - %s\n", line)
	}
	fmt.Println("conditional on assumptions:")
	for _, a := range res.Assumptions {
		fmt.Printf("  - [%s] %s\n", a.Layer, a.Statement)
	}
	fmt.Printf("oracle: %s\n", res.Oracle.Property)
	fmt.Printf("evidence: Finite=%s  Reproducible=%s\n", res.Finite.SuiteID, res.Reproducible.SuiteID)
	fmt.Printf("\nlock persisted → %s (characterization section)\n", core.BundlePath(objectID))
	if len(res.Lock.Cases) == 0 {
		fmt.Println("\nNOTE: lock characterizes ZERO behavior — it is NOT a behavior-preservation net.")
		fmt.Println("The gate's [method-use-rule] will refuse to ship this object until it locks real behavior.")
	}
	return 0
}

func charUsageErr(msg string) int {
	fmt.Fprintf(os.Stderr, "kcpos characterize: %s\n\n", msg)
	fmt.Fprint(os.Stderr, characterizeUsage)
	return 2
}

func langFromExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".py":
		return "python"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".html", ".htm":
		return "html"
	}
	return ""
}

func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" {
		return "legacy_object"
	}
	return out
}

func shortHash12(h string) string {
	if len(h) >= 12 {
		return h[:12]
	}
	if h == "" {
		return "(none)"
	}
	return h
}

const characterizeUsage = `kcpos characterize — recover an untrusted legacy artifact's CURRENT behavior

Brownfield characterization front stage (屎山代码维护Agent设计文档 v1.0).
Synthesizes input probes, runs them against the artifact, and locks the
OBSERVED behavior as a golden regression oracle. It judges nothing — it
records what the code does today so a later change can be checked for
behavior preservation (Feathers Method Use Rule).

Usage:
  kcpos characterize <file> [flags]

Flags:
  --symbol NAME        function/symbol under test (default: file base name)
  --lang LANG          python|javascript|typescript|html (default: from extension)
  --produces a,b       observable output port names (default: result)
  --describe TEXT      recovered behavior description fed to the prober
  --signature TEXT     recovered signature fed to the prober
  --stability N        observe each probe N times; ports unstable across
                       runs are NOT locked (nondeterminism guard, default 3)
  -h, --help           this help

Exit codes:
  0  a lock was written (a partial lock is honest, not a failure)
  1  engine error (no probes / no harness / unreadable artifact)
  2  usage error
`
