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
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/creator915/Koncept_OS/internal/infra/persistence"
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
var reLongFlag = regexp.MustCompile(`--([a-z][a-z0-9-]+)\b`)

// generateBattery builds the deterministic, non-model-controlled
// stimulus battery: fixed Feathers §6.6 8-class structural stimuli plus
// short + long flags lifted from the task's OWN provided docs (usage
// surface, not model-authored). Sorted/stable ⇒ reproducible.
//
// 2026-05-21 expansion: long-flag extraction and structural arg/stdin
// edge cases. PB-30 batch #8 tty-clock locked 53/53 internally but PB
// grader scored 13/100 across 281 tests — the gap was the battery
// missing long-flag forms (--version vs -v, --color N), large stdin
// payloads, and quoted/special-character arg shapes. Each addition
// stays determinism-preserving (lifted from on-disk docs, fixed
// constants — never randomized, never agent-controlled). Future work:
// fuzz-with-seed dimension when docs are sparse.
func generateBattery() []batteryItem {
	items := []batteryItem{
		// Structural stimuli (Feathers §6.6 8-class + extensions):
		{"no-args", nil, ""},
		{"help-h", []string{"-h"}, ""},
		{"help-long", []string{"--help"}, ""},
		{"version", []string{"--version"}, ""},
		{"version-short", []string{"-V"}, ""},
		{"bad-flag", []string{"-Z"}, ""},
		{"bad-long-flag", []string{"--this-is-not-a-real-flag"}, ""},
		{"dashdash", []string{"--"}, ""},
		// stdin shapes:
		{"stdin-newline", nil, "\n"},
		{"stdin-spaces", nil, "   \t  \n"},
		{"stdin-lines", nil, "a\nb\nc\n"},
		{"stdin-special", nil, "!@#$%^&*()_+{}|:<>?\n"},
		{"stdin-numeric", nil, "0\n-1\n2147483648\n"},
		{"stdin-empty-line-mix", nil, "x\n\n\ny\n"},
		{"stdin-utf8", nil, "héllo\nwörld\n日本語\n"},
		{"stdin-large", nil, strings.Repeat("line\n", 200)},
		{"stdin-binary-safe", nil, "a\x00b\nc\n"},
		// argv shapes:
		{"arg-plain", []string{"hello"}, ""},
		{"arg-empty", []string{""}, ""},
		{"arg-special", []string{"a b\tc"}, ""},
		{"arg-quoted-spaces", []string{"hello world"}, ""},
		{"arg-utf8", []string{"héllo"}, ""},
		{"arg-numeric", []string{"42"}, ""},
		{"arg-negative-num", []string{"-1"}, ""},
		{"arg-multi", []string{"a", "b", "c"}, ""},
	}

	shortFlags := map[string]bool{}
	longFlags := map[string]bool{}
	for _, g := range []string{"README*", "*.md", "*.1", "*.6", "*.7", "*.8", "FAQ", "*.txt", "man/*", "doc/*", "docs/*"} {
		matches, _ := filepath.Glob(g)
		for _, f := range matches {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			body := string(b)
			for _, m := range reShortFlag.FindAllStringSubmatch(body, -1) {
				if len(m) == 3 {
					shortFlags[m[2]] = true
				}
			}
			for _, m := range reLongFlag.FindAllStringSubmatch(body, -1) {
				if len(m) == 2 && len(m[1]) >= 2 {
					longFlags[m[1]] = true
				}
			}
		}
	}
	// Skip docs-extracted forms that are already in the fixed battery
	// above, so the same flag doesn't appear twice (different name but
	// equivalent argv).
	for _, builtin := range []string{"help", "version"} {
		delete(longFlags, builtin)
	}
	for _, builtin := range []string{"h", "V"} {
		delete(shortFlags, builtin)
	}

	shortKeys := make([]string, 0, len(shortFlags))
	for k := range shortFlags {
		shortKeys = append(shortKeys, k)
	}
	sort.Strings(shortKeys)
	for _, k := range shortKeys {
		items = append(items, batteryItem{"sflag-" + k, []string{"-" + k}, ""})
		items = append(items, batteryItem{"sflag-" + k + "-stdin", []string{"-" + k}, "a\nb\n"})
	}

	longKeys := make([]string, 0, len(longFlags))
	for k := range longFlags {
		longKeys = append(longKeys, k)
	}
	sort.Strings(longKeys)
	for _, k := range longKeys {
		items = append(items, batteryItem{"lflag-" + k, []string{"--" + k}, ""})
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
	// 2026-05-21 — defensive vacuous-pass check. Pre-fix the oracle had
	// a structural hole: probe and run_local both routed to the same
	// /workspace/executable inside the KCPOS_AMD64_CONTAINER, and the
	// compile tool docker-cp'd the agent's build OVER that path. So
	// "probe vs run_local" was actually "agent's binary vs agent's
	// binary" (or "reference vs reference" if the agent never compiled).
	// Either way, every battery item byte-matched → fake 100% lock.
	//
	// PB-30 batch #8 figlet exposed this: agent never called compile,
	// container's /workspace/executable was still the original PB
	// reference, the oracle reported 65/65 match, gate passed, ★FIN
	// shipped — but the agent had produced no deliverable.
	//
	// Fix: at oracle entry, docker-exec sha256sum on both sides and
	// require the agent's binary to (a) exist and (b) hash-differ from
	// the preserved reference at /workspace/executable.ref. The harness
	// (tests/.../setup_real_pb.sh) is responsible for staging the .ref
	// copy and pointing probe at it; this check is the kcpos-side guard
	// that catches harness misconfig + agent-skipped-compile alike.
	//
	// Non-container reconstruction tasks (no KCPOS_AMD64_CONTAINER) are
	// not yet checked — that path needs a different hash channel and is
	// deferred.
	if reason := vacuousOracleCheck(ctx); reason != "" {
		return false, reason, nil
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
	// 2026-05-21 — fill CodeHash so the gate's [method-use-rule] can
	// detect post-characterize impl edits. Pre-fix this field was zero,
	// causing gate to report "locked hash (none), current X" and fail
	// every PB-30 object even when the impl had not changed since the
	// oracle ran. CodeHash is the hash of the impl source (matches what
	// the gate computes via HashSource(implFileBody) when comparing).
	codeHash := ""
	if g, gerr := persistence.LoadGraph(persistence.GraphDefaultPath); gerr == nil && g != nil {
		if obj, ok := g.Objects[objectID]; ok && obj.Impl != nil && *obj.Impl != "" {
			implPath := *obj.Impl
			if !filepath.IsAbs(implPath) {
				if cwd, cerr := os.Getwd(); cerr == nil {
					implPath = filepath.Join(cwd, implPath)
				}
			}
			if body, rerr := os.ReadFile(implPath); rerr == nil {
				codeHash = core.HashSource(string(body))
			}
		}
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
		CodeHash:   codeHash,
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

// vacuousOracleCheck guards against the pre-2026-05-21 hole where probe
// and run_local both routed to /workspace/executable, so the battery
// compared the same binary against itself. Returns "" when safe to
// proceed, or a diagnostic reason string when the oracle would be
// vacuous (and must NOT run).
//
// Two failure modes detected:
//   - agent never compiled: /workspace/executable still hashes to the
//     preserved reference (or no .ref exists yet — harness misconfig).
//   - agent compiled but reference was overwritten in place: probe's
//     channel resolves to the same path as run_local's.
//
// The check is container-mode only (KCPOS_AMD64_CONTAINER set). Non-PB
// reconstruction tasks bypass this — they use a different oracle channel
// and need a separate guard, deferred.
func vacuousOracleCheck(ctx context.Context) string {
	c := os.Getenv("KCPOS_AMD64_CONTAINER")
	if c == "" {
		return ""
	}
	refHash, refErr := dockerExecHashSum(ctx, c, "/workspace/executable.ref")
	if refErr != nil {
		return fmt.Sprintf(
			"vacuous-oracle-guard: /workspace/executable.ref is missing in container %q (%v). "+
				"The harness must preserve the reference at .ref before the agent runs — without that, probe and run_local both target /workspace/executable and any equivalence verdict would be vacuous. "+
				"Fix the harness (setup script) to: docker exec <c> cp /workspace/executable /workspace/executable.ref AND point ./probe at /workspace/executable.ref.",
			c, refErr)
	}
	agentHash, agentErr := dockerExecHashSum(ctx, c, "/workspace/executable")
	if agentErr != nil {
		return fmt.Sprintf(
			"vacuous-oracle-guard: /workspace/executable not found in container %q (%v) — agent has not produced a deliverable. "+
				"Run `compile` to build /workspace/executable, then retry confirm_object. "+
				"(Pre-fix the oracle would have run anyway and compared the reference against itself, reporting a fake 100%% match.)",
			c, agentErr)
	}
	if refHash == agentHash {
		return fmt.Sprintf(
			"vacuous-oracle-guard: /workspace/executable hash (%s) IS IDENTICAL to the preserved reference at /workspace/executable.ref. "+
				"The agent's deliverable IS the reference, not a rebuild — equivalence against itself is vacuous. "+
				"Confirm the agent actually wrote source + compile.sh and ran `compile` to produce a distinct binary.",
			refHash[:16])
	}
	return ""
}

// dockerExecHashSum returns the sha256 of `path` inside `container`.
// Uses container-side sha256sum so we don't have to docker-cp out and
// stat on host — keeps the operation in the same trust domain probe
// and run_local use.
func dockerExecHashSum(ctx context.Context, container, path string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", "exec", "-i", container, "sha256sum", path).Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) < 1 {
		return "", fmt.Errorf("docker exec sha256sum: empty output")
	}
	return fields[0], nil
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
