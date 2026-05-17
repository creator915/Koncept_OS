// Package pbrun is the end-to-end brownfield closed loop for PB-class
// tasks (屎山代码维护Agent设计文档 v1.0): recover behavior → let an LLM
// apply the required change → re-check the recovered lock → verify
// against a HIDDEN oracle the agent path never touches.
//
// Integrity (committed to the user before building this): the kcpos
// modify path is fed ONLY the legacy code + TASK.md. The fixture's
// _oracle/ directory (preservation + acceptance tests) is read by the
// RUNNER for scoring and is NEVER placed in any LLM prompt. The lock is
// recovered purely by observing the legacy code. So a PASS is not
// self-graded theater.
//
// What this honestly tests: kcpos's Method-Use-Rule gate DETECTION
// accuracy — when the LLM's change breaks locked behavior, does the
// lock re-check catch it; when it doesn't, does it correctly allow.
// That is the design's actual thesis, more meaningful than a solve
// rate (PB best model = 3%; low solve is expected, gate accuracy is
// the point).
package pbrun

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/creator915/Koncept_OS/internal/legacy/characterize"
	"github.com/creator915/Koncept_OS/internal/llm/provider"
	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// Meta is the agent-visible fixture config (tests/pb/<name>/meta.json).
type Meta struct {
	Symbol     string   `json:"symbol"`
	Lang       string   `json:"lang"`
	TargetFile string   `json:"targetFile"` // path within legacy/ the change must land in
	Produces   []string `json:"produces"`
	Describe   string   `json:"describe"`
	Signature  string   `json:"signature"`
}

// Result is the honest per-task scorecard.
type Result struct {
	Fixture string `json:"fixture"`

	Locked        int      `json:"locked"`        // characterization cases locked from the ORIGINAL
	Unlocked      int      `json:"unlocked"`
	LLMModified   bool     `json:"llmModified"`   // the LLM returned a changed target file
	ArtifactDrift bool     `json:"artifactDrift"` // hash changed (a real edit happened)

	// kcpos's own verdict (Method Use Rule, lock re-check on modified code).
	GateSawBreakage bool     `json:"gateSawBreakage"`
	GateBrokenCases []string `json:"gateBrokenCases,omitempty"`

	// Independent HIDDEN oracle ground truth (agent never saw these).
	PreserveOraclePass bool   `json:"preserveOraclePass"` // original behavior still holds
	AcceptOraclePass   bool   `json:"acceptOraclePass"`   // the new requirement was met
	OracleLog          string `json:"oracleLog,omitempty"`

	// The thesis metric: does kcpos's gate verdict match the hidden
	// preservation ground truth?
	GateCorrect bool   `json:"gateCorrect"`
	GateOutcome string `json:"gateOutcome"` // "true-catch" | "true-allow" | "MISS" | "false-alarm"

	Err string `json:"err,omitempty"`
}

// Run executes one PB-class fixture end to end.
func Run(ctx context.Context, fixtureDir string) Result {
	res := Result{Fixture: filepath.Base(fixtureDir)}

	meta, err := readMeta(fixtureDir)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	legacyTarget := filepath.Join(fixtureDir, "legacy", meta.TargetFile)
	origBody, err := os.ReadFile(legacyTarget)
	if err != nil {
		res.Err = "read legacy target: " + err.Error()
		return res
	}
	portObs := defaultPortObs(meta.Produces)

	// 1. CHARACTERIZE the ORIGINAL (agent never saw _oracle/).
	cr, err := characterize.Characterize(ctx, characterize.CharRequest{
		ObjectID:        meta.Symbol,
		ImplSymbol:      meta.Symbol,
		Lang:            meta.Lang,
		ArtifactPath:    legacyTarget,
		CodeHash:        core.HashSource(string(origBody)),
		Signature:       meta.Signature,
		Description:     meta.Describe,
		Produces:        meta.Produces,
		PortObservation: portObs,
		Environment:     map[string]string{"lang": meta.Lang},
		Stability:       3,
		IntroducedBy:    "pbrun",
	}, characterize.DefaultSynthesize(), characterize.RealHarness())
	if err != nil {
		res.Err = "characterize: " + err.Error()
		return res
	}
	res.Locked = len(cr.Lock.Cases)
	res.Unlocked = len(cr.Lock.Unlocked)
	if res.Locked == 0 {
		res.Err = "characterization locked zero behavior — no preservation net to test"
		return res
	}

	// 2. MODIFY via LLM. Prompt = TASK.md + legacy code ONLY.
	modified, err := llmModify(ctx, fixtureDir, meta, legacyTarget)
	if err != nil {
		res.Err = "llm modify: " + err.Error()
		return res
	}
	res.LLMModified = strings.TrimSpace(modified) != "" && modified != string(origBody)
	res.ArtifactDrift = core.HashSource(modified) != core.HashSource(string(origBody))

	// Stage the modified target in a scratch tree (siblings copied so
	// imports still resolve).
	scratch, cleanup, err := stageModified(fixtureDir, meta, modified)
	if err != nil {
		res.Err = "stage modified: " + err.Error()
		return res
	}
	defer cleanup()
	modTarget := filepath.Join(scratch, meta.TargetFile)

	// 3. kcpos GATE: re-check the recovered lock against the MODIFIED
	// code (Method Use Rule). Divergence on any locked case = kcpos
	// says "behavior preservation violated".
	broken, derr := recheckLock(ctx, cr, meta, modTarget, portObs)
	if derr != nil {
		res.Err = "lock recheck: " + derr.Error()
		return res
	}
	res.GateBrokenCases = broken
	res.GateSawBreakage = len(broken) > 0

	// 4. HIDDEN ORACLE ground truth (runner-only; never prompted).
	pPass, aPass, olog := runHiddenOracle(fixtureDir, meta, scratch)
	res.PreserveOraclePass = pPass
	res.AcceptOraclePass = aPass
	res.OracleLog = olog

	// 5. Thesis metric: did kcpos's gate verdict match the hidden
	// preservation truth? Ground truth "broken" = preserve oracle FAIL.
	groundBroken := !pPass
	switch {
	case groundBroken && res.GateSawBreakage:
		res.GateCorrect, res.GateOutcome = true, "true-catch"
	case !groundBroken && !res.GateSawBreakage:
		res.GateCorrect, res.GateOutcome = true, "true-allow"
	case groundBroken && !res.GateSawBreakage:
		res.GateCorrect, res.GateOutcome = false, "MISS (broke behavior, gate did not catch)"
	default:
		res.GateCorrect, res.GateOutcome = false, "false-alarm (gate flagged but behavior intact)"
	}
	return res
}

func readMeta(dir string) (Meta, error) {
	var m Meta
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, fmt.Errorf("meta.json: %w", err)
	}
	if m.Symbol == "" || m.TargetFile == "" || m.Lang == "" {
		return m, fmt.Errorf("meta.json missing symbol/targetFile/lang")
	}
	if len(m.Produces) == 0 {
		m.Produces = []string{"result"}
	}
	return m, nil
}

func defaultPortObs(produces []string) map[string]string {
	po := map[string]string{}
	if len(produces) == 1 {
		po[produces[0]] = "return"
	} else {
		for _, p := range produces {
			po[p] = "return." + p
		}
	}
	return po
}

// llmModify feeds the LLM ONLY TASK.md + every legacy/ file. _oracle/
// is never read here. Returns the proposed new content of TargetFile.
func llmModify(ctx context.Context, dir string, meta Meta, target string) (string, error) {
	taskMD, err := os.ReadFile(filepath.Join(dir, "TASK.md"))
	if err != nil {
		return "", fmt.Errorf("read TASK.md: %w", err)
	}
	legacyDir := filepath.Join(dir, "legacy")
	var b strings.Builder
	b.WriteString("You are maintaining an UNTESTED legacy codebase. Implement the requested change.\n")
	b.WriteString("Preserve all existing behavior that the task does not explicitly ask you to change.\n\n")
	b.WriteString("=== TASK ===\n")
	b.Write(taskMD)
	b.WriteString("\n\n=== LEGACY SOURCE (the only spec that exists) ===\n")
	_ = filepath.Walk(legacyDir, func(p string, info os.FileInfo, e error) error {
		if e != nil || info.IsDir() {
			return nil
		}
		c, _ := os.ReadFile(p)
		rel, _ := filepath.Rel(legacyDir, p)
		fmt.Fprintf(&b, "\n--- FILE: %s ---\n%s\n", rel, string(c))
		return nil
	})
	rel, _ := filepath.Rel(legacyDir, target)
	fmt.Fprintf(&b, "\nReturn ONLY the complete new content of %s, no fences, no prose.\n", rel)

	cfg, err := provider.ProviderFromEnv()
	if err != nil {
		return "", err
	}
	cfg.Thinking = false
	client := transport.NewClient(cfg)
	msg, err := client.Chat(ctx, []transport.Message{
		{Role: "system", Content: "You are a careful legacy-code maintainer. Output only code."},
		{Role: "user", Content: b.String()},
	}, nil, transport.StreamHandler{})
	if err != nil {
		return "", err
	}
	return stripFences(msg.Content), nil
}

func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i > 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s) + "\n"
}

// stageModified copies legacy/ into a temp dir and overwrites the
// target with the LLM's version (siblings preserved for imports).
func stageModified(dir string, meta Meta, modified string) (string, func(), error) {
	scratch, err := os.MkdirTemp("", "pbrun-")
	if err != nil {
		return "", func() {}, err
	}
	legacyDir := filepath.Join(dir, "legacy")
	err = filepath.Walk(legacyDir, func(p string, info os.FileInfo, e error) error {
		if e != nil || info.IsDir() {
			return e
		}
		rel, _ := filepath.Rel(legacyDir, p)
		dst := filepath.Join(scratch, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		c, _ := os.ReadFile(p)
		return os.WriteFile(dst, c, 0o644)
	})
	if err != nil {
		os.RemoveAll(scratch)
		return "", func() {}, err
	}
	if err := os.WriteFile(filepath.Join(scratch, meta.TargetFile), []byte(modified), 0o644); err != nil {
		os.RemoveAll(scratch)
		return "", func() {}, err
	}
	return scratch, func() { os.RemoveAll(scratch) }, nil
}

// recheckLock re-runs the recovered golden cases against the MODIFIED
// target and returns the names of cases whose observed output no longer
// matches the lock (= Method Use Rule preservation violations).
func recheckLock(ctx context.Context, cr *characterize.CharResult, meta Meta, modTarget string, portObs map[string]string) ([]string, error) {
	probes := make([]characterize.CharProbe, 0, len(cr.Lock.Cases))
	for _, c := range cr.Lock.Cases {
		probes = append(probes, characterize.CharProbe{Name: c.Name, Setup: c.Setup, Call: c.Call})
	}
	trace, err := characterize.RealHarness()(ctx, characterize.HarnessRequest{
		ObjectID:        meta.Symbol,
		ImplSymbol:      meta.Symbol,
		Lang:            meta.Lang,
		ArtifactPath:    modTarget,
		Produces:        meta.Produces,
		PortObservation: portObs,
		Probes:          probes,
	})
	if err != nil {
		return nil, err
	}
	var calls []core.RuntimeCall
	if trace != nil {
		calls = trace.Calls
	}
	var broken []string
	for i, c := range cr.Lock.Cases {
		want, _ := json.Marshal(c.Expect)
		if i >= len(calls) {
			broken = append(broken, c.Name+" (no observation post-change)")
			continue
		}
		got := observedExpectKey(calls[i].Outputs, meta.Produces)
		if string(want) != got {
			broken = append(broken, fmt.Sprintf("%s (was %s, now %s)", c.Name, string(want), got))
		}
	}
	return broken, nil
}

// observedExpectKey transcribes one observed call the same way the
// engine locks it, so the comparison is apples-to-apples.
func observedExpectKey(obs map[string]json.RawMessage, produces []string) string {
	want := map[string]bool{}
	for _, p := range produces {
		want[p] = true
	}
	type exp struct {
		Port   string          `json:"port"`
		Equals json.RawMessage `json:"equals,omitempty"`
	}
	var out []exp
	// stable order
	for _, p := range produces {
		if v, ok := obs[p]; ok && len(v) > 0 {
			out = append(out, exp{Port: p, Equals: append(json.RawMessage(nil), v...)})
		}
		_ = want
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// runHiddenOracle executes _oracle/preserve_test.py and
// _oracle/accept_test.py against the modified scratch tree. These
// files are READ HERE ONLY — never in any LLM prompt.
func runHiddenOracle(dir string, meta Meta, scratch string) (preserve, accept bool, log string) {
	oracleDir := filepath.Join(dir, "_oracle")
	run := func(testFile string) (bool, string) {
		src := filepath.Join(oracleDir, testFile)
		if _, err := os.Stat(src); err != nil {
			return false, testFile + ": missing"
		}
		dst := filepath.Join(scratch, testFile)
		c, _ := os.ReadFile(src)
		_ = os.WriteFile(dst, c, 0o644)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "python3", "-m", "pytest", "-q", dst)
		cmd.Dir = scratch
		out, err := cmd.CombinedOutput()
		_ = os.Remove(dst)
		return err == nil, testFile + ":\n" + string(out)
	}
	p, pl := run("preserve_test.py")
	a, al := run("accept_test.py")
	return p, a, pl + "\n" + al
}
