package install

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
	"github.com/creator915/Koncept_OS/internal/runtime/preflight"
)

func LinkTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: false, // mutates a single well-known symlink
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name: "runtime_link",
				Description: "Persistently bind an EXISTING playwright install to the kcpos cache, avoiding the 200MB chromium re-download from runtime_install. Creates a symlink at ~/.kcpos/cache/playwright/node_modules pointing at the node_modules directory you specify.\n\nIntended workflow when runtime_smoke says \"playwright missing\":\n  1. Use `bash`/`glob`/`find` to discover playwright on this machine.\n     Common locations: any project's node_modules (`find ~ -maxdepth 5 -type d -name playwright`), npm global prefix (`npm config get prefix`), npx cache (`ls ~/.npm/_npx/*/node_modules/playwright`).\n  2. Call `runtime_link path=<absolute path to that node_modules dir>` to bind it.\n  3. Re-call runtime_smoke — preflight's cheap probe now finds it.\n\nThe binding persists across kcpos invocations. Idempotent: re-linking overwrites the previous symlink. Validates that <path>/playwright/package.json exists before binding.\n\nNo env var required after binding — preflight's kcpos-cache probe walks the symlink transparently.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Absolute path to a node_modules directory containing playwright/package.json. NOT the playwright package itself — its PARENT node_modules.",
						},
					},
					"required": []string{"path"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			path, _ := args["path"].(string)
			return runRuntimeLink(path)
		},
	}
}

func runRuntimeLink(rawPath string) (string, error) {
	if rawPath == "" {
		return "", fmt.Errorf("runtime_link: path required (absolute path to a node_modules directory)")
	}
	abs, err := filepath.Abs(rawPath)
	if err != nil {
		return "", fmt.Errorf("runtime_link: resolve abs path: %w", err)
	}
	// User might pass the playwright dir itself rather than its parent
	// node_modules — give a friendly correction.
	if strings.HasSuffix(abs, string(os.PathSeparator)+"playwright") {
		return "", fmt.Errorf("runtime_link: path=%q points at the playwright package itself; pass the PARENT node_modules directory instead (e.g. %s)", abs, filepath.Dir(abs))
	}
	pkgJSON := filepath.Join(abs, "playwright", "package.json")
	if _, err := os.Stat(pkgJSON); err != nil {
		return "", fmt.Errorf("runtime_link: %s does not contain playwright/package.json — verify the path actually has a playwright install (looked for: %s)", abs, pkgJSON)
	}

	cacheDir := preflight.CacheDir()
	if cacheDir == "" {
		return "", fmt.Errorf("runtime_link: cannot resolve kcpos cache dir (KCPOS_CACHE_DIR or HOME must be set)")
	}
	playwrightDir := filepath.Join(cacheDir, "playwright")
	if err := os.MkdirAll(playwrightDir, 0o755); err != nil {
		return "", fmt.Errorf("runtime_link: mkdir %s: %w", playwrightDir, err)
	}
	linkPath := filepath.Join(playwrightDir, "node_modules")

	// Replace any existing symlink/dir. We intentionally only remove
	// symlinks or empty dirs — refuse to clobber a real install (e.g.
	// from runtime_install) without explicit instructions.
	info, statErr := os.Lstat(linkPath)
	if statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(linkPath); err != nil {
				return "", fmt.Errorf("runtime_link: remove existing symlink %s: %w", linkPath, err)
			}
		} else if info.IsDir() {
			// Real directory — refuse to delete it.
			return "", fmt.Errorf("runtime_link: %s is a real directory (likely from runtime_install). To replace it, delete it manually first, then re-run runtime_link", linkPath)
		}
	}

	if err := os.Symlink(abs, linkPath); err != nil {
		return "", fmt.Errorf("runtime_link: symlink %s → %s: %w", linkPath, abs, err)
	}

	// Verify preflight now finds it.
	preflight.ClearCache()
	r := preflight.Detect(preflight.Playwright)
	if !r.Found {
		return "", fmt.Errorf("runtime_link: symlink created but preflight still can't detect playwright. This suggests the linked node_modules doesn't have a valid playwright/package.json. Linked: %s → %s", linkPath, abs)
	}

	cr := preflight.Detect(preflight.Chromium)

	var b strings.Builder
	fmt.Fprintf(&b, "runtime_link: bound %s → %s\n", linkPath, abs)
	fmt.Fprintf(&b, "  playwright: %s\n", fallback(r.Version, "unknown"))
	if cr.Found {
		fmt.Fprintf(&b, "  chromium:   %s\n", fallback(cr.Path, "found"))
	} else {
		fmt.Fprintf(&b, "  chromium:   ✗ %s\n", preflight.Hint(preflight.Chromium))
	}
	fmt.Fprintln(&b, "binding persists across kcpos invocations — runtime_smoke will now find it automatically.")
	return b.String(), nil
}
