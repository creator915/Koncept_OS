// Package pbkcpos is the ProgramBench reconstruction orchestrator: the
// kcpos-side "agent" that, given only a black-box reference binary +
// docs inside a no-internet cleanroom container, produces a submission
// codebase (source + compile.sh) for `programbench eval` to grade.
//
// Honest framing (committed to the user): PB tests from-scratch
// reconstruction, NOT the brownfield 屎山 design. What plugs in here
// from our recent work is the characterization SPINE — recover an
// untrusted black box's behavior by probing it, lock golden I/O, then
// construct toward that recovered contract and verify by re-probing.
// Here the "black box" is a compiled CLI binary instead of a .py file,
// so the probe is a CLI invocation probe (new, small) reusing the
// recover→construct→verify loop concept, not characterize.RealHarness
// (which renders a Python test harness — wrong shape for a binary).
//
// Expected outcome (stated up front): PB frontier ≈ 3% solved; this
// config ≈ 0 solved. The real signal is per-task behavioral partial
// pass-rate, captured by programbench eval downstream.
package pbkcpos

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/creator915/Koncept_OS/internal/llm/provider"
	"github.com/creator915/Koncept_OS/internal/llm/transport"
)

// Config drives one task reconstruction.
type Config struct {
	InstanceID string // e.g. abishekvashok__cmatrix.5c082c6
	Language   string // c | go | rs | cpp ... (from task.yaml)
	WorkRoot   string // run dir; submission lands at <WorkRoot>/<InstanceID>/submission.tar.gz
	Probes     int    // # CLI probes to characterize the reference (default 14)
	Iterations int    // reconstruct↔verify rounds (default 4)
	DockerOrg  string // default "programbench"
}

func imageRef(org, instanceID, tag string) string {
	if org == "" {
		org = "programbench"
	}
	return fmt.Sprintf("%s/%s:%s", org, strings.Replace(instanceID, "__", "_1776_", 1), tag)
}

// Result is the per-task orchestration outcome (NOT the PB score —
// that comes from `programbench eval` on the produced submission).
type Result struct {
	InstanceID     string `json:"instanceId"`
	SubmissionPath string `json:"submissionPath"`
	Probed         int    `json:"probed"`         // golden CLI behaviors captured
	Iterations     int    `json:"iterations"`     // reconstruct rounds used
	SelfMatch      int    `json:"selfMatch"`      // probes the final build matched vs reference
	Built          bool   `json:"built"`          // final submission compiled in-container
	Err            string `json:"err,omitempty"`
}

// dockerExec runs a command inside container, returns combined output.
func dockerExec(ctx context.Context, cid, sh string, timeout time.Duration) (string, int) {
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(c, "docker", "exec", "-w", "/workspace", cid, "bash", "-lc", sh)
	out, err := cmd.CombinedOutput()
	rc := 0
	if err != nil {
		rc = 1
		if ee, ok := err.(*exec.ExitError); ok {
			rc = ee.ExitCode()
		}
	}
	return string(out), rc
}

func sh(ctx context.Context, args ...string) (string, error) {
	c, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(c, args[0], args[1:]...).CombinedOutput()
	return string(out), err
}

// llm is the single LLM seam (DeepSeek via provider-from-env, same as
// synthesize/pbrun).
func llm(ctx context.Context, system, user string) (string, error) {
	cfg, err := provider.ProviderFromEnv()
	if err != nil {
		return "", err
	}
	cfg.Thinking = false
	cl := transport.NewClient(cfg)
	m, err := cl.Chat(ctx, []transport.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}, nil, transport.StreamHandler{})
	if err != nil {
		return "", err
	}
	return m.Content, nil
}

func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i > 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}

// probe is one CLI invocation + the reference's observed behavior.
type probe struct {
	Argv   []string `json:"argv"`
	Stdin  string   `json:"stdin,omitempty"`
	Stdout string   `json:"stdout"`
	Stderr string   `json:"stderr"`
	Exit   int      `json:"exit"`
}

// Reconstruct runs the full recover→construct→verify loop for one PB
// task and writes submission.tar.gz. It NEVER reads upstream source —
// only the in-container docs + black-box reference binary.
func Reconstruct(ctx context.Context, cfg Config) Result {
	res := Result{InstanceID: cfg.InstanceID}
	if cfg.Probes == 0 {
		cfg.Probes = 14
	}
	if cfg.Iterations == 0 {
		cfg.Iterations = 4
	}
	// Progress logging to stderr (the launcher tees it to the run log).
	// Without this a multi-hour batch runs blind — observability was the
	// meta-defect that forced forensic state reconstruction on the first
	// pilot. Format: [pbkcpos <id> +<elapsed>] <phase>.
	t0 := time.Now()
	step := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, "[pbkcpos %s +%ds] %s\n",
			cfg.InstanceID, int(time.Since(t0).Seconds()), fmt.Sprintf(format, a...))
	}
	step("start: probes=%d iterations=%d", cfg.Probes, cfg.Iterations)
	cleanroom := imageRef(cfg.DockerOrg, cfg.InstanceID, "task_cleanroom")
	cid := "pbk-" + strings.Replace(cfg.InstanceID, "__", "-", 1)
	cid = strings.NewReplacer(".", "-", "_", "-").Replace(cid)

	_, _ = sh(ctx, "docker", "rm", "-f", cid)
	if out, err := sh(ctx, "docker", "run", "-d", "--platform", "linux/amd64",
		"--name", cid, "-w", "/workspace", cleanroom, "sleep", "4h"); err != nil {
		res.Err = "start cleanroom: " + err.Error() + " :: " + out
		return res
	}
	defer sh(context.Background(), "docker", "rm", "-f", cid)
	step("cleanroom container up")

	// --- recover: docs + reference --help/--version ---
	step("reading docs + reference --help/--version")
	docs, _ := dockerExec(ctx, cid,
		"for f in README* readme* *.1 *.md; do [ -f \"$f\" ] && echo \"=== $f ===\" && cat \"$f\"; done 2>/dev/null | head -c 12000; "+
			"echo '=== ./executable --help ==='; (./executable --help 2>&1 | head -c 4000); "+
			"echo '=== ./executable --version ==='; (./executable --version 2>&1 | head -c 800)",
		60*time.Second)

	// --- characterize the black box: LLM proposes CLI invocations ---
	step("LLM: synthesizing %d CLI probes (slow call)", cfg.Probes)
	probesJSON, err := llm(ctx,
		"You reverse-engineer a black-box CLI by choosing diverse invocations. Output ONLY JSON.",
		fmt.Sprintf("Docs + observed help for a CLI program (language: %s):\n\n%s\n\n"+
			"Propose %d diverse invocations to characterize its behavior (flags, help, version, "+
			"bad input, edge args, stdin). Output JSON: {\"probes\":[{\"argv\":[...],\"stdin\":\"\"}, ...]}",
			cfg.Language, clip(docs, 9000), cfg.Probes))
	if err != nil {
		res.Err = "probe synth: " + err.Error()
		return res
	}
	var pl struct {
		Probes []probe `json:"probes"`
	}
	_ = json.Unmarshal([]byte(stripFences(probesJSON)), &pl)
	if len(pl.Probes) == 0 {
		res.Err = "probe synth produced no invocations"
		return res
	}

	// Run probes against the REFERENCE executable → golden behavior.
	step("running %d probes against reference black box", len(pl.Probes))
	golden := runProbes(ctx, cid, "./executable", pl.Probes)
	res.Probed = len(golden)
	step("golden behavior captured: %d probes", len(golden))

	// --- construct ↔ verify loop ---
	scratch := filepath.Join(cfg.WorkRoot, cfg.InstanceID, "src")
	_ = os.RemoveAll(scratch)
	_ = os.MkdirAll(scratch, 0o755)
	var lastDiff string
	for it := 1; it <= cfg.Iterations; it++ {
		res.Iterations = it
		step("reconstruct round %d/%d: LLM generating source (slow call)", it, cfg.Iterations)
		files, compileSh, lerr := askReconstruct(ctx, cfg.Language, docs, golden, lastDiff)
		if lerr != nil {
			res.Err = fmt.Sprintf("reconstruct round %d: %v", it, lerr)
			return res
		}
		step("round %d: wrote %d file(s), building in :task container (emulated)", it, len(files))
		if err := writeTree(scratch, files, compileSh); err != nil {
			res.Err = "write tree: " + err.Error()
			return res
		}
		built, selfMatch, diff := buildAndVerify(ctx, cfg, scratch, golden)
		res.Built, res.SelfMatch, lastDiff = built, selfMatch, diff
		step("round %d: built=%v selfMatch=%d/%d", it, built, selfMatch, len(golden))
		if built && selfMatch == len(golden) {
			step("round %d: perfect self-match, stopping early", it)
			break // perfect self-consistency; stop early
		}
	}

	// --- export submission.tar.gz ---
	step("exporting submission.tar.gz")
	subDir := filepath.Join(cfg.WorkRoot, cfg.InstanceID)
	subTar := filepath.Join(subDir, "submission.tar.gz")
	if out, err := sh(ctx, "tar", "czf", subTar, "-C", scratch, "."); err != nil {
		res.Err = "tar submission: " + err.Error() + " :: " + out
		return res
	}
	res.SubmissionPath = subTar
	return res
}

func runProbes(ctx context.Context, cid, bin string, ps []probe) []probe {
	out := make([]probe, 0, len(ps))
	for _, p := range ps {
		args := strings.Join(quoteAll(p.Argv), " ")
		cmd := bin + " " + args
		if p.Stdin != "" {
			cmd = "printf %s " + shQuote(p.Stdin) + " | " + cmd
		}
		// capture stdout/stderr/exit separately
		full := "OUT=$(" + cmd + " 2>/tmp/e); EC=$?; echo \"<<<EXIT:$EC>>>\"; echo \"<<<OUT>>>\"; printf '%s' \"$OUT\" | head -c 4000; echo; echo \"<<<ERR>>>\"; head -c 1500 /tmp/e"
		raw, _ := dockerExec(ctx, cid, full, 30*time.Second)
		g := p
		g.Exit = parseInt(between(raw, "<<<EXIT:", ">>>"))
		g.Stdout = afterMarker(raw, "<<<OUT>>>", "<<<ERR>>>")
		g.Stderr = strings.TrimSpace(afterMarker(raw, "<<<ERR>>>", ""))
		out = append(out, g)
	}
	return out
}

func askReconstruct(ctx context.Context, lang, docs string, golden []probe, lastDiff string) (map[string]string, string, error) {
	gj, _ := json.Marshal(golden)
	fb := ""
	if lastDiff != "" {
		fb = "\n\nYour previous attempt diverged from the reference here — fix these:\n" + clip(lastDiff, 6000)
	}
	reply, err := llm(ctx,
		"You reconstruct a program from its documented + observed behavior. Output ONLY strict JSON.",
		fmt.Sprintf("Language: %s. Reconstruct a program whose behavior matches the reference.\n\n"+
			"DOCS:\n%s\n\nOBSERVED reference behavior (invocation→output, authoritative):\n%s%s\n\n"+
			"Output JSON: {\"files\":[{\"path\":\"relative/path\",\"content\":\"...\"}],"+
			"\"compileSh\":\"#!/bin/bash\\nset -e\\n... build and produce ./executable in CWD ...\"}\n"+
			"compile.sh MUST end with an executable named exactly ./executable in the working dir.",
			lang, clip(docs, 7000), clip(string(gj), 9000), fb))
	if err != nil {
		return nil, "", err
	}
	var env struct {
		Files []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"files"`
		CompileSh string `json:"compileSh"`
	}
	if e := json.Unmarshal([]byte(stripFences(reply)), &env); e != nil {
		return nil, "", fmt.Errorf("bad reconstruct JSON: %w", e)
	}
	if len(env.Files) == 0 || strings.TrimSpace(env.CompileSh) == "" {
		return nil, "", fmt.Errorf("reconstruct reply missing files or compileSh")
	}
	fm := map[string]string{}
	for _, f := range env.Files {
		if strings.Contains(f.Path, "..") || strings.HasPrefix(f.Path, "/") {
			continue
		}
		fm[f.Path] = f.Content
	}
	return fm, env.CompileSh, nil
}

func writeTree(root string, files map[string]string, compileSh string) error {
	for p, c := range files {
		fp := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(fp, []byte(c), 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(root, "compile.sh"), []byte(compileSh), 0o755)
}

// buildAndVerify builds the submission in a :task container and re-runs
// the golden probes against the freshly built ./executable, returning
// (built, #matching, humanDiff). This is the verify half of the loop.
func buildAndVerify(ctx context.Context, cfg Config, scratch string, golden []probe) (bool, int, string) {
	taskImg := imageRef(cfg.DockerOrg, cfg.InstanceID, "task")
	cid := "pbkb-" + strings.NewReplacer(".", "-", "_", "-", "/", "-").Replace(cfg.InstanceID)
	_, _ = sh(ctx, "docker", "rm", "-f", cid)
	if _, err := sh(ctx, "docker", "run", "-d", "--platform", "linux/amd64",
		"--name", cid, "-w", "/workspace", taskImg, "sleep", "1h"); err != nil {
		return false, 0, "build container start failed: " + err.Error()
	}
	defer sh(context.Background(), "docker", "rm", "-f", cid)
	dockerExec(ctx, cid, "rm -rf /workspace/* /workspace/.[!.]* 2>/dev/null || true", 60*time.Second)
	if _, err := sh(ctx, "docker", "cp", scratch+"/.", cid+":/workspace/"); err != nil {
		return false, 0, "copy submission in: " + err.Error()
	}
	bo, brc := dockerExec(ctx, cid, "chmod +x ./compile.sh && ./compile.sh", 25*time.Minute)
	if brc != 0 {
		return false, 0, "compile failed:\n" + clip(bo, 3000)
	}
	if o, rc := dockerExec(ctx, cid, "test -x ./executable && echo OK", 20*time.Second); rc != 0 || !strings.Contains(o, "OK") {
		return false, 0, "compile.sh did not produce ./executable"
	}
	got := runProbes(ctx, cid, "./executable", golden)
	match, diffs := 0, []string{}
	for i := range golden {
		if i < len(got) && got[i].Stdout == golden[i].Stdout && got[i].Exit == golden[i].Exit {
			match++
		} else if i < len(got) {
			diffs = append(diffs, fmt.Sprintf("argv=%v\n want exit=%d out=%q\n got  exit=%d out=%q",
				golden[i].Argv, golden[i].Exit, clip(golden[i].Stdout, 300), got[i].Exit, clip(got[i].Stdout, 300)))
		}
	}
	return true, match, strings.Join(diffs, "\n---\n")
}

// --- tiny helpers ---

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…[clipped]"
}
func quoteAll(a []string) []string {
	o := make([]string, len(a))
	for i, x := range a {
		o[i] = shQuote(x)
	}
	return o
}
func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	j := strings.Index(s, b)
	if j < 0 {
		return s
	}
	return s[:j]
}
func afterMarker(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	if b == "" {
		return s
	}
	j := strings.Index(s, b)
	if j < 0 {
		return s
	}
	return s[:j]
}
func parseInt(s string) int {
	n := 0
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}
