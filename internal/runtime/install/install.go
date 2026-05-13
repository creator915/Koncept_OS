package install

import (
	"context"
	"fmt"
	"strings"

	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
	"github.com/creator915/Koncept_OS/internal/runtime/preflight"
)

func InstallTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: false, // npm/playwright installs are not safe to parallelize against the same cache
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name: "runtime_install",
				Description: "Install runtime_smoke's prerequisites (playwright + headless Chromium). Installs to the centralized kcpos cache at ~/.kcpos/cache/playwright/ so the ~200MB chromium binary is fetched ONCE per machine, then reused across every project. Idempotent: re-running when already installed is a no-op unless `force=true`.\n\nPer the v9.0.7 install policy (D1), this tool defaults to autonomous install — it does NOT prompt for confirmation. Operators who want gated installs should run `kcpos doctor --install` instead (which prompts via stdin) or set KCPOS_AUTO_INSTALL=0 (planned).\n\nReturns the post-install detection table: which tools are now available + their versions. Errors with the install hint when a recipe failed (e.g. npm missing — chain to your system's package manager first).",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"force": map[string]interface{}{"type": "boolean", "description": "When true, reinstall even if playwright + chromium are already detected. Default false (skip if both present)."},
					},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			force, _ := args["force"].(bool)
			return runRuntimeInstall(ctx, force)
		},
	}
}

func runRuntimeInstall(ctx context.Context, force bool) (string, error) {
	// Probe before installing. When both already present and !force,
	// short-circuit to keep the agent loop fast.
	preflight.ClearCache()
	playRes := preflight.Detect(preflight.Playwright)
	chromeRes := preflight.Detect(preflight.Chromium)
	if !force && playRes.Found && chromeRes.Found {
		return formatInstallReport("already installed; nothing to do", playRes, chromeRes), nil
	}

	var b strings.Builder
	fmt.Fprintln(&b, "runtime_install: provisioning kcpos cache (~/.kcpos/cache/playwright/)")

	opts := preflight.InstallOptions{
		Mode:   preflight.ModeAutoConfirm,
		Stdout: &b,
		Stderr: &b,
	}

	// 1. playwright via npm (skips chromium download — see installPlaywright)
	if !playRes.Found || force {
		fmt.Fprintln(&b, "step 1/2: npm install playwright")
		if err := preflight.Install(preflight.Playwright, opts); err != nil {
			return b.String(), fmt.Errorf("install playwright: %w", err)
		}
	}

	// 2. chromium binary
	if !chromeRes.Found || force {
		fmt.Fprintln(&b, "step 2/2: npx playwright install chromium")
		if err := preflight.Install(preflight.Chromium, opts); err != nil {
			return b.String(), fmt.Errorf("install chromium: %w", err)
		}
	}

	playRes = preflight.Detect(preflight.Playwright)
	chromeRes = preflight.Detect(preflight.Chromium)
	return formatInstallReport(b.String()+"runtime_install: complete", playRes, chromeRes), nil
}

func formatInstallReport(header string, play, chrome preflight.Result) string {
	var b strings.Builder
	fmt.Fprintln(&b, header)
	fmt.Fprintf(&b, "  playwright: found=%v version=%s\n", play.Found, fallback(play.Version, "—"))
	fmt.Fprintf(&b, "  chromium:   found=%v version=%s\n", chrome.Found, fallback(chrome.Version, "—"))
	if play.Path != "" {
		fmt.Fprintf(&b, "  cache:      %s\n", play.Path)
	}
	return b.String()
}

func fallback(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
