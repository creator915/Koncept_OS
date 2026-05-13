package preflight

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
)

// spec is a registry entry for one tool. Closed type — outside packages
// can only see Tool ids and the Result/Hint/Install API surface.
type spec struct {
	tool       Tool
	detectCmd  []string
	detectEnv  []string // KEY=VAL entries appended to subprocess Environ
	versionRe  *regexp.Regexp
	installer  installerFn
	hint       string
	nodeModule string // non-empty: probe cache dir for node_modules/<name>
}

type installerFn func(opts InstallOptions) error

// Version regexes — capture group 1 must be the version.
var (
	reVPrefixed = regexp.MustCompile(`v(\d+\.\d+\.\d+(?:[\-+][\w.]+)?)`)              // "v18.20.4" / "v1.0.0-beta"
	rePlain     = regexp.MustCompile(`(\d+\.\d+\.\d+(?:[\-+][\w.]+)?)`)               // "18.20.4"
	reGo        = regexp.MustCompile(`go version go(\d+\.\d+(?:\.\d+)?(?:[\-+][\w.]+)?)`) // "go version go1.22.1 darwin/arm64"
	rePy        = regexp.MustCompile(`Python (\d+\.\d+(?:\.\d+)?)`)
)

// registry holds the canonical list of known tools. Order is preserved
// for `kcpos doctor` output stability.
var registry = []spec{
	{
		tool:      Node,
		detectCmd: []string{"node", "--version"},
		versionRe: reVPrefixed,
		installer: installNode,
		hint:      "node missing — install Node.js 18+ via your package manager (`brew install node` on macOS, `apt-get install nodejs npm` on Debian/Ubuntu)",
	},
	{
		tool:      NPM,
		detectCmd: []string{"npm", "--version"},
		versionRe: rePlain,
		installer: installNPM,
		hint:      "npm missing — usually bundled with Node.js; install Node.js (see `node` hint)",
	},
	{
		tool:      NPX,
		detectCmd: []string{"npx", "--version"},
		versionRe: rePlain,
		installer: installNPX,
		hint:      "npx missing — bundled with npm 5.2+; reinstall Node.js to get a current npm",
	},
	{
		tool:      TSC,
		// npx tsc respects local node_modules first; when no local tsc,
		// npx will fetch it (which is what we want for kcpos's TS path).
		detectCmd: []string{"npx", "--no-install", "tsc", "--version"},
		versionRe: regexp.MustCompile(`Version\s+(\d+\.\d+\.\d+)`),
		installer: nil, // npx auto-fetches on first use
		hint:      "tsc missing — kcpos uses `npx tsc`; npm/npx must be available, and the first TS compile will download tsc transparently",
	},
	{
		tool:      Go,
		detectCmd: []string{"go", "version"},
		versionRe: reGo,
		installer: nil, // user-installed, no automated path
		hint:      "go missing — install Go 1.21+ from https://go.dev/dl/",
	},
	{
		tool:      Python3,
		detectCmd: []string{"python3", "--version"},
		versionRe: rePy,
		installer: nil,
		hint:      "python3 missing — install Python 3.10+ via your package manager (`brew install python` on macOS)",
	},
	{
		tool:      PyCompile,
		detectCmd: []string{"python3", "-c", "import py_compile; print('ok')"},
		versionRe: nil, // py_compile ships with python3; presence == version-of-python
		installer: nil,
		hint:      "py_compile missing — comes with python3 stdlib; install python3 (see `python3` hint)",
	},
	{
		tool:       Playwright,
		nodeModule: "playwright",
		// Fallback when not in cache: try the system npx playwright.
		detectCmd: []string{"npx", "--no-install", "playwright", "--version"},
		versionRe: regexp.MustCompile(`Version\s+(\d+\.\d+\.\d+)`),
		installer: installPlaywright,
		hint:      "playwright not in kcpos cache (~/.kcpos/cache/playwright/) or ./node_modules. Recommended flow:\n  1. Discover via `bash`/`glob`/`find` — examples: `find ~ -maxdepth 5 -type d -name playwright 2>/dev/null`; `npm config get prefix` (look in <prefix>/lib/node_modules/playwright); `ls ~/.npm/_npx/*/node_modules/playwright`.\n  2. Once found, call `runtime_link path=<absolute path to node_modules parent dir>` — this creates a persistent symlink in the kcpos cache. No env var needed.\n  3. If nothing found, call `runtime_install` (downloads ~200MB chromium into kcpos cache).\nPower-user override: set KCPOS_PLAYWRIGHT_NODE_PATH for one-off / CI scenarios; persistent binding via runtime_link is the normal path.",
	},
	{
		tool: Chromium,
		// Verify chromium binary inside playwright's browser cache.
		detectCmd: []string{"node", "-e", chromiumProbeJS},
		detectEnv: nil, // set dynamically in probe (PLAYWRIGHT_BROWSERS_PATH)
		versionRe: regexp.MustCompile(`chromium ok ([^\s]+)`),
		installer: installChromium,
		hint:      "chromium missing — bundled with playwright; runtime_install will fetch it",
	},
}

// chromiumProbeJS asks playwright to point at its chromium binary; if
// the binary exists, prints `chromium ok <executablePath>`. Failure paths
// throw, which the detect regex won't match — Found stays false.
const chromiumProbeJS = `
try {
  const { chromium } = require('playwright');
  const path = chromium.executablePath();
  require('fs').accessSync(path);
  console.log('chromium ok ' + path);
} catch (e) {
  console.error('chromium probe failed: ' + e.message);
  process.exit(1);
}`

// lookup finds a registry entry by Tool id. Returns (spec, true) on hit.
func lookup(t Tool) (spec, bool) {
	for _, s := range registry {
		if s.tool == t {
			return s, true
		}
	}
	return spec{}, false
}

// allSpecs returns the registry in declaration order. Used by DetectAll.
func allSpecs() []spec {
	out := make([]spec, len(registry))
	copy(out, registry)
	return out
}

// --- installer implementations ---

// installNode / installNPM / installNPX share the same recipe: try
// `brew install node` on macOS, `apt-get` on debian-likes, else error
// with the install hint. Honors PATH so containerized installs work.
func installNode(opts InstallOptions) error {
	return installSystemPackage(opts, "node")
}

func installNPM(opts InstallOptions) error {
	// npm comes bundled with node — installing node satisfies both.
	return installSystemPackage(opts, "node")
}

func installNPX(opts InstallOptions) error {
	return installSystemPackage(opts, "node")
}

// installSystemPackage runs the platform-appropriate package manager.
// Stays narrow: only the package managers most kcpos contributors use.
// Anything more exotic should hit the Hint path and let the user install
// by hand.
func installSystemPackage(opts InstallOptions, pkg string) error {
	switch runtime.GOOS {
	case "darwin":
		if _, err := lookPath("brew"); err == nil {
			return runCmd(opts, "", nil, "brew", "install", pkg)
		}
		return fmt.Errorf("brew not found — install Homebrew first or `brew install %s` manually", pkg)
	case "linux":
		if _, err := lookPath("apt-get"); err == nil {
			// Try without sudo first; let the caller deal with privilege escalation.
			return runCmd(opts, "", nil, "apt-get", "install", "-y", pkg)
		}
		if _, err := lookPath("dnf"); err == nil {
			return runCmd(opts, "", nil, "dnf", "install", "-y", pkg)
		}
		return fmt.Errorf("no supported package manager (apt-get / dnf) — install %s manually", pkg)
	}
	return fmt.Errorf("automated install not supported on %s — install %s manually", runtime.GOOS, pkg)
}

// installPlaywright sets up the centralized kcpos playwright cache:
// 1. Create ~/.kcpos/cache/playwright/{,browsers/}
// 2. Write a minimal package.json if missing
// 3. `npm install playwright` with cwd=cacheDir
//
// The browsers/ subdirectory is filled by installChromium.
func installPlaywright(opts InstallOptions) error {
	cacheDir := opts.CacheDir
	if cacheDir == "" {
		cacheDir = defaultCacheDir()
	}
	playwrightDir := filepath.Join(cacheDir, "playwright")
	if err := os.MkdirAll(playwrightDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", playwrightDir, err)
	}
	pkgPath := filepath.Join(playwrightDir, "package.json")
	if !fileExists(pkgPath) {
		const pkgJSON = `{
  "name": "kcpos-playwright-cache",
  "private": true,
  "description": "kcpos centralized playwright install — DO NOT MODIFY by hand",
  "dependencies": {
    "playwright": "^1.40.0"
  }
}
`
		if err := os.WriteFile(pkgPath, []byte(pkgJSON), 0o644); err != nil {
			return fmt.Errorf("write package.json: %w", err)
		}
	}
	// npm install in the cache dir. We DON'T pin a version — the
	// `^1.40.0` in package.json lets npm pick the latest compatible.
	browsersDir := filepath.Join(playwrightDir, "browsers")
	env := []string{
		"PLAYWRIGHT_BROWSERS_PATH=" + browsersDir,
		// Skip the post-install chromium download here — installChromium
		// does it explicitly so failures surface in the right place.
		"PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1",
	}
	if err := runCmd(opts, playwrightDir, env, "npm", "install"); err != nil {
		return fmt.Errorf("npm install playwright: %w", err)
	}
	return nil
}

// installChromium runs `npx playwright install chromium` against the
// cached playwright. Pulls the ~200MB binary into CacheDir/playwright/browsers/.
func installChromium(opts InstallOptions) error {
	cacheDir := opts.CacheDir
	if cacheDir == "" {
		cacheDir = defaultCacheDir()
	}
	playwrightDir := filepath.Join(cacheDir, "playwright")
	browsersDir := filepath.Join(playwrightDir, "browsers")
	// Require playwright to be installed first; the caller chains
	// installPlaywright → installChromium.
	modPath := filepath.Join(playwrightDir, "node_modules", "playwright", "package.json")
	if !fileExists(modPath) {
		return fmt.Errorf("playwright not installed at %s — run preflight.Install(Playwright) first", playwrightDir)
	}
	env := []string{"PLAYWRIGHT_BROWSERS_PATH=" + browsersDir}
	if err := runCmd(opts, playwrightDir, env, "npx", "playwright", "install", "chromium"); err != nil {
		return fmt.Errorf("npx playwright install chromium: %w", err)
	}
	return nil
}

// --- small filesystem helpers ---

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// candidateNodeRoots returns a SHORT, deterministic ordered list of
// node_modules directories preflight will probe when looking up a
// node-module tool like Playwright. Three locations only — the bare
// minimum that don't depend on guessing the user's package-manager
// conventions:
//
//  1. KCPOS_PLAYWRIGHT_NODE_PATH env override (the escape hatch — set
//     this to any node_modules dir to point kcpos at an existing install)
//  2. ~/.kcpos/cache/playwright/node_modules  (kcpos own cache, created
//     by runtime_install)
//  3. ./node_modules                          (project-local; matches
//     Node's own resolver behavior)
//
// Anything beyond this is the AGENT'S job to discover. The agent has
// `bash`, `glob`, and `find` tools — much more flexible than a hardcoded
// path list that won't survive pnpm/yarn-PnP/monorepo/custom-prefix
// setups. When preflight returns Found=false, the Result carries
// CheckedRoots so the agent can avoid re-probing the same paths.
func candidateNodeRoots() []string {
	var roots []string
	if env := os.Getenv("KCPOS_PLAYWRIGHT_NODE_PATH"); env != "" {
		roots = append(roots, env)
	}
	if cacheDir := defaultCacheDir(); cacheDir != "" {
		roots = append(roots, filepath.Join(cacheDir, "playwright", "node_modules"))
	}
	if cwd, err := os.Getwd(); err == nil {
		roots = append(roots, filepath.Join(cwd, "node_modules"))
	}
	return roots
}

func defaultCacheDir() string {
	if env := os.Getenv("KCPOS_CACHE_DIR"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".kcpos", "cache")
}

func readPackageJSONVersion(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var doc struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	return doc.Version
}

// lookPath is a wrappable exec.LookPath for testability. Tests can
// override via the var; production calls the stdlib directly.
var lookPath = exec.LookPath
