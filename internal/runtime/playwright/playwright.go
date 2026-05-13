// Package runtimetools registers agent tools that drive real-browser
// runtime smoke tests for HTML deliverables (runtime_smoke) and trigger
// the playwright/chromium install (runtime_install).
//
// These tools sit alongside typecalc_* but cover a verification dimension
// the vm.Script harness cannot: actually booting the deliverable in a
// browser and reporting whether it loads, errors, or renders. The
// 2026-05-12 Terraria batch retro motivated this — 4/5 instances shipped
// black-screen HTML while passing kcpos's gate, because typecalc_test
// couldn't see browser-level failures.
//
// Tools registered:
//   - runtime_smoke   — boot a deliverable in headless Chromium, capture diagnostics
//   - runtime_install — install playwright + chromium under ~/.kcpos/cache/playwright/
package playwright

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/creator915/Koncept_OS/internal/runtime/preflight"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// RunPlaywrightSmoke is the Go-side driver for runtime_smoke. Writes
// the harness JS to a temp file, spawns `node <harness>`, parses the
// single JSON line printed to stdout.
//
// Returns the parsed section + a non-nil error iff the subprocess
// could not be started OR its output could not be parsed. A returned
// section with OK=false is a NORMAL outcome (the deliverable is broken)
// and not an error.
func RunPlaywrightSmoke(ctx context.Context, implPath string, opts SmokeOptions) (*core.RuntimeSmokeSection, error) {
	// Resolve to absolute file:// URL.
	abs, err := filepath.Abs(implPath)
	if err != nil {
		return nil, fmt.Errorf("resolve impl path %q: %w", implPath, err)
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("impl missing at %s: %w", abs, err)
	}
	url := "file://" + abs

	// Pre-flight: both playwright and chromium must resolve. Capture
	// the actual locations so the node subprocess loads from whichever
	// directory had them (kcpos cache, project node_modules pointed
	// at via KCPOS_PLAYWRIGHT_NODE_PATH, etc.) — not a single hardcoded
	// path.
	playRes := preflight.Detect(preflight.Playwright)
	if !playRes.Found {
		return nil, fmt.Errorf("runtime_smoke prerequisite missing: playwright not loadable. %s", preflight.Hint(preflight.Playwright))
	}
	chromeRes := preflight.Detect(preflight.Chromium)
	if !chromeRes.Found {
		return nil, fmt.Errorf("runtime_smoke prerequisite missing: chromium not loadable (playwright at %s but its chromium isn't accessible). %s", playRes.ResolvedRoot, preflight.Hint(preflight.Chromium))
	}

	// Materialize the harness JS.
	timeoutMs := opts.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}
	viewW := opts.ViewportW
	if viewW <= 0 {
		viewW = 800
	}
	viewH := opts.ViewportH
	if viewH <= 0 {
		viewH = 600
	}
	screenshot := ""
	if opts.ScreenshotPath != "" {
		screenshot = opts.ScreenshotPath
	}

	script := playwrightHarnessJS
	script = strings.ReplaceAll(script, "__IMPL_URL__", jsString(url))
	script = strings.ReplaceAll(script, "__TIMEOUT_MS__", strconv.Itoa(timeoutMs))
	script = strings.ReplaceAll(script, "__VIEWPORT_W__", strconv.Itoa(viewW))
	script = strings.ReplaceAll(script, "__VIEWPORT_H__", strconv.Itoa(viewH))
	script = strings.ReplaceAll(script, "__SCREENSHOT__", jsString(screenshot))

	tmp, err := os.CreateTemp("", "kcpos-smoke-*.js")
	if err != nil {
		return nil, fmt.Errorf("create temp harness: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(script); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("write temp harness: %w", err)
	}
	tmp.Close()

	// Spawn node with NODE_PATH pointing at the kcpos playwright cache.
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs+10000)*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "node", tmpPath)
	env := os.Environ()
	// NODE_PATH points at the playwright we actually resolved (could be
	// kcpos cache, user project, env override — whatever Detect found).
	if playRes.ResolvedRoot != "" {
		env = append(env, "NODE_PATH="+playRes.ResolvedRoot)
	}
	// PLAYWRIGHT_BROWSERS_PATH only set when the chromium probe could
	// tell us a specific browsers dir (e.g. it found chromium in kcpos
	// cache). If empty, we let playwright use its default resolution
	// (~/Library/Caches/ms-playwright on macOS) which is what the user
	// likely already has populated.
	if chromeRes.ExtraPath != "" {
		env = append(env, "PLAYWRIGHT_BROWSERS_PATH="+chromeRes.ExtraPath)
	}
	cmd.Env = env

	out, runErr := cmd.CombinedOutput()
	// The harness always emits a single JSON line to stdout; even
	// crashes are caught in the .catch handler. So we parse stdout
	// even when the process exits non-zero.
	if len(out) == 0 {
		return nil, fmt.Errorf("runtime_smoke: empty output from node harness (run error: %v)", runErr)
	}
	// Locate the LAST line that looks like a JSON object; tolerates
	// playwright spam on stderr (combined output mixes streams) and
	// helper warnings before the result line.
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var jsonLine string
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "{") && strings.HasSuffix(l, "}") {
			jsonLine = l
			break
		}
	}
	if jsonLine == "" {
		return nil, fmt.Errorf("runtime_smoke: no JSON result in harness output:\n%s", truncate(string(out), 2000))
	}

	var raw smokeRawResult
	if err := json.Unmarshal([]byte(jsonLine), &raw); err != nil {
		return nil, fmt.Errorf("runtime_smoke: parse JSON: %w (line: %s)", err, truncate(jsonLine, 800))
	}

	section := &core.RuntimeSmokeSection{
		OK:                raw.OK,
		LoadFired:         raw.LoadFired,
		LoadDurationMs:    raw.LoadDurationMs,
		PageErrors:        convertErrors(raw.PageErrors),
		ConsoleErrors:     convertErrors(raw.ConsoleErrors),
		RequestFailures:   convertRequests(raw.RequestFailures),
		Canvas:            convertCanvas(raw.Canvas),
		ScreenshotPath:    screenshot,
		PlaywrightVersion: raw.PlaywrightVersion,
		Timestamp:         time.Now().UTC(),
	}
	return section, nil
}

// SmokeOptions carries per-invocation knobs from the agent tool layer.
type SmokeOptions struct {
	TimeoutMs      int
	ViewportW      int
	ViewportH      int
	ScreenshotPath string
}

// smokeRawResult mirrors the JSON the Node harness emits.
type smokeRawResult struct {
	OK                bool                 `json:"ok"`
	LoadFired         bool                 `json:"loadFired"`
	LoadDurationMs    int                  `json:"loadDurationMs"`
	PageErrors        []smokeRawError      `json:"pageErrors"`
	ConsoleErrors     []smokeRawError      `json:"consoleErrors"`
	RequestFailures   []smokeRawRequest    `json:"requestFailures"`
	Canvas            *smokeRawCanvas      `json:"canvas"`
	PlaywrightVersion string               `json:"playwrightVersion"`
}

type smokeRawError struct {
	Message  string `json:"message"`
	Stack    string `json:"stack"`
	Source   string `json:"source"`
	Location string `json:"location"`
}

type smokeRawRequest struct {
	URL     string `json:"url"`
	Failure string `json:"failure"`
}

type smokeRawCanvas struct {
	Found          bool `json:"found"`
	Width          int  `json:"width"`
	Height         int  `json:"height"`
	NonBlackPixels int  `json:"nonBlackPixels"`
	OK             bool `json:"ok"`
}

func convertErrors(es []smokeRawError) []core.RuntimeSmokeError {
	if len(es) == 0 {
		return nil
	}
	out := make([]core.RuntimeSmokeError, len(es))
	for i, e := range es {
		out[i] = core.RuntimeSmokeError{
			Message:  e.Message,
			Stack:    e.Stack,
			Source:   e.Source,
			Location: e.Location,
		}
	}
	return out
}

func convertRequests(rs []smokeRawRequest) []core.RuntimeSmokeRequest {
	if len(rs) == 0 {
		return nil
	}
	out := make([]core.RuntimeSmokeRequest, len(rs))
	for i, r := range rs {
		out[i] = core.RuntimeSmokeRequest{URL: r.URL, Failure: r.Failure}
	}
	return out
}

func convertCanvas(c *smokeRawCanvas) *core.RuntimeSmokeCanvas {
	if c == nil {
		return nil
	}
	return &core.RuntimeSmokeCanvas{
		Found:          c.Found,
		Width:          c.Width,
		Height:         c.Height,
		NonBlackPixels: c.NonBlackPixels,
		OK:             c.OK,
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// jsString returns the value as a JSON-encoded string literal — safe to
// substitute into the JS source.
func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
