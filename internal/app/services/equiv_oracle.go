package services

// equiv_oracle.go — correction ② (process-justice / 流程正义).
//
// The verification chain confers status=confirmed. For a SPEC task the
// existing compile→synth→test→review path tests the declared contract.
// For a spec-LESS reconstruction task (the PB / legacy-blackbox case)
// there is NO contract — only a reference oracle `./probe`. The design
// (屎山代码维护Agent设计文档 §2.4 OracleSource=Reference/Characterization,
// §6.6 Characterization Test) says verification there is a behavioral
// equivalence tester: run the deliverable and the reference over a
// stimulus battery the AGENT DOES NOT CONTROL, lock現状, and persist it
// as the bundle's Characterization section (§2.4b / §6.6 — the design's
// own home for this; we do NOT invent an ad-hoc evidence kind).
//
// Deterministic: the battery is gate-generated (Feathers §6.6 8-class +
// short flags lifted from the task's OWN provided docs) — never
// model-supplied (the 命门: a model-chosen battery is theater).
// Fail-closed: any divergence / missing executable / tool error ⇒ NOT
// equivalent, and the caller MUST refuse to confer confirmed.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/creator915/Koncept_OS/internal/tools/fs"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// reconstructionMode reports whether this run is a spec-less
// reconstruction: a reference oracle `./probe` exists in the workdir.
// (The PB / blackbox harness writes ./probe; SPEC runs do not.)
func reconstructionMode() bool {
	st, err := os.Stat("probe")
	return err == nil && !st.IsDir()
}

type batteryItem struct {
	name  string
	argv  []string
	stdin string
}

var reShortFlag = regexp.MustCompile(`(^|[\s\[(|])-([A-Za-z])\b`)

// generateBattery builds the deterministic, non-model-controlled
// stimulus battery: fixed Feathers §6.6 8-class structural stimuli plus
// single-letter flags lifted from the task's OWN provided docs (usage
// surface, not model-authored). Sorted/stable ⇒ reproducible.
func generateBattery() []batteryItem {
	items := []batteryItem{
		{"no-args", nil, ""},
		{"help-h", []string{"-h"}, ""},
		{"help-long", []string{"--help"}, ""},
		{"version", []string{"--version"}, ""},
		{"bad-flag", []string{"-Z"}, ""},
		{"dashdash", []string{"--"}, ""},
		{"stdin-newline", nil, "\n"},
		{"stdin-spaces", nil, "   \t  \n"},
		{"stdin-lines", nil, "a\nb\nc\n"},
		{"stdin-special", nil, "!@#$%^&*()_+{}|:<>?\n"},
		{"stdin-numeric", nil, "0\n-1\n2147483648\n"},
		{"stdin-empty-line-mix", nil, "x\n\n\ny\n"},
		{"arg-plain", []string{"hello"}, ""},
		{"arg-empty", []string{""}, ""},
		{"arg-special", []string{"a b\tc"}, ""},
	}
	flags := map[string]bool{}
	for _, g := range []string{"README*", "*.md", "*.1", "*.6", "FAQ", "*.txt"} {
		matches, _ := filepath.Glob(g)
		for _, f := range matches {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			for _, m := range reShortFlag.FindAllStringSubmatch(string(b), -1) {
				if len(m) == 3 {
					flags[m[2]] = true
				}
			}
		}
	}
	keys := make([]string, 0, len(flags))
	for k := range flags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		items = append(items, batteryItem{"flag-" + k, []string{"-" + k}, ""})
		items = append(items, batteryItem{"flag-" + k + "-stdin", []string{"-" + k}, "a\nb\n"})
	}
	return items
}

type equivResult struct {
	Item     string `json:"item"`
	Argv     []string `json:"argv"`
	Match    bool   `json:"match"`
	ProbeOut string `json:"probeOut,omitempty"`
	ExecOut  string `json:"execOut,omitempty"`
	Note     string `json:"note,omitempty"`
}

// runEquivalenceOracle runs the deliverable (./executable via the
// platform-routed run_local tool) and the reference (./probe) over the
// generated battery, requires byte-equal combined output on every item,
// persists the verdict as the bundle's Characterization section
// (design §2.4b/§6.6), and reports pass/fail. Fail-closed.
func runEquivalenceOracle(ctx context.Context, objectID string) (passed bool, summary string, err error) {
	probeT, ok1 := fs.Tools()["probe"]
	runT, ok2 := fs.Tools()["run_local"]
	if !ok1 || !ok2 {
		return false, "", fmt.Errorf("equiv-oracle: probe/run_local tools unavailable")
	}
	battery := generateBattery()
	results := make([]equivResult, 0, len(battery))
	matched, mismatched := 0, 0
	var unlocked []string
	for _, it := range battery {
		args := map[string]interface{}{"args": toIfaceSlice(it.argv)}
		if it.stdin != "" {
			args["stdin"] = it.stdin
		}
		refOut, refErr := probeT.Run(ctx, args)
		if refErr != nil {
			// Reference-side infrastructural failure is a harness
			// problem, not a verdict — surface it (do NOT silently pass).
			return false, "", fmt.Errorf("equiv-oracle: probe failed on %s: %v", it.name, refErr)
		}
		gotOut, gotErr := runT.Run(ctx, args)
		res := equivResult{Item: it.name, Argv: it.argv}
		if gotErr != nil {
			res.Match = false
			res.Note = "deliverable did not run: " + gotErr.Error()
			mismatched++
			unlocked = append(unlocked, it.name)
		} else if normalizeOut(refOut) == normalizeOut(gotOut) {
			res.Match = true
			matched++
		} else {
			res.Match = false
			res.ProbeOut = clip(refOut, 200)
			res.ExecOut = clip(gotOut, 200)
			mismatched++
			unlocked = append(unlocked, it.name)
		}
		results = append(results, res)
	}
	passed = mismatched == 0 && matched > 0

	lang := ""
	if b, hasB := core.ReadBundle(objectID); hasB && b.Compile != nil {
		lang = b.Compile.Lang
	}
	detail, _ := json.Marshal(map[string]interface{}{
		"oracle":    "behavioral-equivalence vs ./probe",
		"battery":   len(battery),
		"matched":   matched,
		"mismatched": mismatched,
		"results":   results,
	})
	sec := &core.CharacterizationSection{
		SuiteID:    "equiv-" + objectID,
		Lang:       lang,
		OracleProperty: fmt.Sprintf(
			"deliverable is behaviorally equivalent to the reference ./probe over a %d-item gate-generated (non-model-controlled) battery", len(battery)),
		Cases:         nil, // CLI-argv battery ≠ TestCase (function-call) shape; verdict + lossless replay live in Detail (design Part 10.3)
		LockedCount:   matched,
		UnlockedCount: mismatched,
		Unlocked:      unlocked,
		ConfidenceReport: []string{
			fmt.Sprintf("battery = %d items (Feathers §6.6 8-class + docs-derived flags, deterministic)", len(battery)),
			fmt.Sprintf("matched = %d/%d", matched, len(battery)),
			"input set = gate-generated, NOT model-controlled",
		},
		ConditionalOn: []string{
			"reference ./probe is the authoritative oracle for this task",
			"battery exercises the observable surface; unobserved inputs are honestly unlocked",
		},
		Detail:    detail,
		Timestamp: time.Now().UTC(),
	}
	if werr := core.WriteCharacterization(objectID, sec); werr != nil {
		return passed, "", fmt.Errorf("equiv-oracle: persist characterization: %w", werr)
	}

	summary = fmt.Sprintf("behavioral-equivalence: %d/%d battery items matched ./probe (non-model-controlled)",
		matched, len(battery))
	if !passed {
		var ds []string
		for _, r := range results {
			if !r.Match {
				ds = append(ds, fmt.Sprintf("[%s] argv=%v %s", r.Item, r.Argv, r.Note))
			}
		}
		summary += "\n--- divergences (first 10) ---\n" + strings.Join(capSlice(ds, 10), "\n")
	}
	return passed, summary, nil
}

// hasEquivEvidence reports whether a PASSING behavioral-equivalence
// characterization was recorded for objectID. Used by the MarkConfirmed
// chokepoint to refuse confirm without it in reconstruction mode.
func hasEquivEvidence(objectID string) bool {
	b, ok := core.ReadBundle(objectID)
	if !ok || b.Characterization == nil {
		return false
	}
	c := b.Characterization
	return strings.HasPrefix(c.SuiteID, "equiv-") && c.UnlockedCount == 0 && c.LockedCount > 0
}

// normalizeOut trims trailing per-line whitespace so a lone trailing
// newline is not a spurious divergence. It deliberately does NOT
// normalize program-name differences: a reconstruction that prints a
// different argv[0] IS behaviorally different and must fail
// (fail-closed / under-confirm is the safe direction).
func normalizeOut(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t\r")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func toIfaceSlice(ss []string) []interface{} {
	out := make([]interface{}, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func clip(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", "\\n")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func capSlice(ss []string, n int) []string {
	if len(ss) <= n {
		return ss
	}
	return ss[:n]
}
