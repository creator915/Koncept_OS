// Package preflight detects and (optionally) installs the external
// toolchain kcpos drives — Node, Go, Python, Playwright, Chromium, etc.
//
// Why a dedicated package: pre-v9.1 the entire detection surface was a
// one-line `exec.LookPath` wrapper at internal/typecalc/lang/compile.go.
// When a tool was missing (e.g. no `node` for HTML deliverables), kcpos
// silently no-oped — verification "passed" because the runner never ran.
// 4/5 instances of the 2026-05-12 Terraria batch shipped broken HTML
// while passing the gate, partly for this reason.
//
// This package fixes the gap with three concerns:
//
//  1. Detect(tool) — version-probe a known tool. Result includes the
//     resolved path, version string, and the exact probe command run.
//     Used by `kcpos doctor` and by `runtime_smoke` to decide whether
//     to invoke or to direct the agent to call `runtime_install`.
//
//  2. Install(tool, opts) — run the canonical install recipe. Confirmation
//     modes: AutoConfirm (default for agent path), Interactive (CLI),
//     Blocked (refuse). Some tools have no installer (Go, system Python);
//     those return ErrNotInstallable so the caller can surface a hint.
//
//  3. Registry — the table of known tools, their detection commands,
//     version regex, install recipes, and human-readable hints. Lives
//     in registry.go.
//
// The package has zero external Go deps; everything is os/exec + stdlib.
package preflight

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Tool identifies a known tool in the registry.
type Tool string

const (
	Node       Tool = "node"
	NPM        Tool = "npm"
	NPX        Tool = "npx"
	TSC        Tool = "tsc"
	Go         Tool = "go"
	Python3    Tool = "python3"
	PyCompile  Tool = "py_compile"
	Playwright Tool = "playwright"
	Chromium   Tool = "chromium"
)

// Result is the outcome of Detect for a tool.
type Result struct {
	Tool     Tool
	Path     string // absolute filesystem path, or module path for node-module tools
	Version  string // parsed version; "unknown" when probe ok but parse failed
	Found    bool
	Probe    string // exact command, joined by space — audit trail
	ProbeOut string // first 4KB of combined output
	Err      error  // non-nil iff !Found

	// ResolvedRoot — for node-module tools (Playwright), the parent
	// node_modules directory that contains the module. Used by callers
	// (runtime_smoke) to set NODE_PATH dynamically so chromium / the
	// playwright runtime is loaded from whichever location actually had it
	// (kcpos cache, project-local, default user cache, etc.). Empty for
	// binary tools and when not found.
	ResolvedRoot string

	// ExtraPath — for tools with associated data caches (Chromium's
	// browser binaries), the directory the caller should set
	// PLAYWRIGHT_BROWSERS_PATH to. Empty means "let the tool use its own
	// default" (e.g. ~/Library/Caches/ms-playwright on macOS).
	ExtraPath string

	// CheckedRoots — for node-module tools, the candidate directories
	// preflight probed before deciding. Populated whether Found is true
	// (single entry, the hit) or false (all candidates tried). Lets the
	// agent skip re-probing the same paths and direct its own `glob`/
	// `find` toward locations preflight didn't try.
	CheckedRoots []string
}

// InstallMode controls user confirmation for Install.
type InstallMode int

const (
	// ModeAutoConfirm runs the install with no prompt. Default for the
	// runtime_install agent tool per the D1 decision (Terraria v9.0.6
	// retro): agents should self-heal toolchain gaps. CI may also use
	// this via KCPOS_AUTO_INSTALL=1.
	ModeAutoConfirm InstallMode = iota

	// ModeInteractive prompts the user via Confirm function. Used by
	// `kcpos doctor --install`. Caller supplies the Confirm callback.
	ModeInteractive

	// ModeBlocked refuses to install. Returns ErrUserDeclined with the
	// install hint so the caller can surface it. Used when an env var
	// override or operator policy disables auto-install.
	ModeBlocked
)

// InstallOptions configures an Install call.
type InstallOptions struct {
	Mode InstallMode

	// CacheDir is where node-module installs land. Defaults to
	// ~/.kcpos/cache when empty. Centralized to avoid 200MB+
	// node_modules in every test project (D-cache decision).
	CacheDir string

	// Confirm is called by ModeInteractive to gate the install. It must
	// return true for the install to proceed. nil with ModeInteractive
	// is treated as ModeBlocked.
	Confirm func(t Tool, hint string) bool

	// Stdout/Stderr capture install progress. nil routes to io.Discard
	// (avoids spam in the tool result) — set explicitly for CLI use.
	Stdout io.Writer
	Stderr io.Writer

	// Timeout caps each install subprocess. Zero defaults to 15 min
	// (chromium download under slow network).
	Timeout time.Duration
}

// Sentinel errors callers can match against.
var (
	ErrNotInstallable = errors.New("preflight: tool has no install recipe")
	ErrUserDeclined   = errors.New("preflight: user declined install")
	ErrInstallFailed  = errors.New("preflight: install command failed")
	ErrUnknownTool    = errors.New("preflight: unknown tool")
)

// detection cache — repeated Detect calls in a single CLI/agent turn
// should not respawn subprocesses. Cache is process-scoped; a fresh
// `kcpos` invocation always re-probes.
var (
	detectCache   = map[Tool]Result{}
	detectCacheMu sync.RWMutex
)

// ClearCache invalidates the detection cache. Called by Install on
// success so the next Detect re-probes the freshly installed binary.
func ClearCache() {
	detectCacheMu.Lock()
	defer detectCacheMu.Unlock()
	detectCache = map[Tool]Result{}
}

// Detect probes ONE tool and returns the result. Cached for the
// process lifetime — call ClearCache to force a re-probe.
func Detect(t Tool) Result {
	detectCacheMu.RLock()
	if r, ok := detectCache[t]; ok {
		detectCacheMu.RUnlock()
		return r
	}
	detectCacheMu.RUnlock()

	r := probe(t)

	detectCacheMu.Lock()
	detectCache[t] = r
	detectCacheMu.Unlock()
	return r
}

// DetectAll probes every registered tool in declaration order. Used by
// `kcpos doctor`.
func DetectAll() []Result {
	specs := allSpecs()
	out := make([]Result, 0, len(specs))
	for _, s := range specs {
		out = append(out, Detect(s.tool))
	}
	return out
}

// probe is the uncached worker — runs the detection command and
// parses the version regex from its output.
func probe(t Tool) Result {
	r := Result{Tool: t}
	spec, ok := lookup(t)
	if !ok {
		r.Err = fmt.Errorf("%w: %s", ErrUnknownTool, t)
		return r
	}
	// Node-module tools (Playwright) — walk an ordered list of candidate
	// node_modules roots and return the first hit. Order:
	//   1. KCPOS_PLAYWRIGHT_NODE_PATH (env override, highest priority)
	//   2. ~/.kcpos/cache/playwright/node_modules        (kcpos own cache)
	//   3. ./node_modules                                 (project-local)
	//   4. ~/Documents/.../node_modules                   (npx _npx cache, common on macOS)
	//   5. common npm global prefixes (`/usr/local/lib/node_modules`,
	//      `/opt/homebrew/lib/node_modules`)
	//
	// This lets kcpos discover playwright installs the user already has
	// from earlier projects, avoiding redundant 200MB chromium downloads.
	if spec.nodeModule != "" {
		candidates := candidateNodeRoots()
		r.CheckedRoots = append([]string(nil), candidates...)
		var brokenSymlinks []string
		for _, root := range candidates {
			modPath := filepath.Join(root, spec.nodeModule, "package.json")
			if fileExists(modPath) {
				r.Path = filepath.Dir(modPath)
				r.ResolvedRoot = root
				r.CheckedRoots = []string{root} // narrow to the hit
				r.Found = true
				r.Probe = "fs.Stat " + modPath
				if v := readPackageJSONVersion(modPath); v != "" {
					r.Version = v
				} else {
					r.Version = "unknown"
				}
				return r
			}
			// Detect "stale binding": the candidate root is itself a
			// symlink (typical: ~/.kcpos/cache/playwright/node_modules
			// from a prior runtime_link), but its target is gone or
			// doesn't contain the module. This is the canonical "user
			// uninstalled the playwright that I was linked to" failure
			// — surface it specifically so the agent doesn't reinstall
			// blindly and instead re-runs discovery.
			if li, err := os.Lstat(root); err == nil && li.Mode()&os.ModeSymlink != 0 {
				target, _ := os.Readlink(root)
				brokenSymlinks = append(brokenSymlinks, root+" → "+target)
			}
		}
		var staleMsg string
		if len(brokenSymlinks) > 0 {
			staleMsg = fmt.Sprintf(" PRIOR runtime_link BINDING IS BROKEN: %s — the original install was likely uninstalled or moved. Re-discover with bash/glob/find and call runtime_link with the new path; the symlink will be overwritten.", strings.Join(brokenSymlinks, "; "))
		}
		r.Err = fmt.Errorf("preflight: node module %q not found in any of: %s.%s Recommended next step: discover with bash/glob/find (e.g. `find ~ -maxdepth 5 -type d -name %s 2>/dev/null`), then call runtime_link with the discovered path. OR call runtime_install to fetch fresh. OR set KCPOS_PLAYWRIGHT_NODE_PATH for one-off override", spec.nodeModule, strings.Join(candidates, ", "), staleMsg, spec.nodeModule)
		return r
	}
	// For Chromium specifically, the detection needs playwright loaded
	// from wherever it was found. We resolve playwright first, then run
	// the probe with NODE_PATH pointing at that location. We also leave
	// PLAYWRIGHT_BROWSERS_PATH unset so playwright's own default cache
	// resolution (~/Library/Caches/ms-playwright on macOS) is used.
	var dynamicEnv []string
	if t == Chromium {
		play := Detect(Playwright)
		if !play.Found {
			r.Err = fmt.Errorf("chromium probe needs playwright; %v", play.Err)
			return r
		}
		dynamicEnv = []string{"NODE_PATH=" + play.ResolvedRoot}
		// Respect explicit user override; otherwise let playwright resolve.
		if env := os.Getenv("PLAYWRIGHT_BROWSERS_PATH"); env != "" {
			dynamicEnv = append(dynamicEnv, "PLAYWRIGHT_BROWSERS_PATH="+env)
		}
	}
	cmd := spec.detectCmd
	if len(cmd) == 0 {
		r.Err = fmt.Errorf("%w: %s has no detectCmd", ErrUnknownTool, t)
		return r
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	extraEnv := append([]string(nil), spec.detectEnv...)
	extraEnv = append(extraEnv, dynamicEnv...)
	if len(extraEnv) > 0 {
		c.Env = append(c.Environ(), extraEnv...)
	}
	out, err := c.CombinedOutput()
	r.Probe = strings.Join(cmd, " ")
	r.ProbeOut = truncate(string(out), 4096)
	if err != nil {
		r.Err = fmt.Errorf("%s: %w (output: %s)", r.Probe, err, r.ProbeOut)
		return r
	}
	// Resolve path for audit trail. exec.LookPath uses PATH; for
	// node-module probes that aren't truly on PATH, we keep Path empty.
	if path, lerr := exec.LookPath(cmd[0]); lerr == nil {
		r.Path = path
	}
	r.Found = true
	if spec.versionRe != nil {
		m := spec.versionRe.FindStringSubmatch(r.ProbeOut)
		if len(m) >= 2 {
			r.Version = m[1]
		} else {
			r.Version = "unknown"
		}
	} else {
		r.Version = "unknown"
	}
	// Chromium probe outputs "chromium ok <executablePath>"; derive the
	// browsers cache dir (parent of the version-stamped subdir) so
	// runtime_smoke can set PLAYWRIGHT_BROWSERS_PATH consistently with
	// where chromium actually lives. Empty when not parseable — caller
	// then falls back to playwright's default.
	if t == Chromium && r.Found {
		if execPath := extractChromiumExecPath(r.ProbeOut); execPath != "" {
			r.Path = execPath
			r.ExtraPath = deriveBrowsersDir(execPath)
		}
	}
	return r
}

// extractChromiumExecPath reads "chromium ok <path>" from the probe
// output and returns <path>. Returns "" when not present.
func extractChromiumExecPath(out string) string {
	for _, line := range strings.Split(out, "\n") {
		const prefix = "chromium ok "
		if i := strings.Index(line, prefix); i >= 0 {
			return strings.TrimSpace(line[i+len(prefix):])
		}
	}
	return ""
}

// deriveBrowsersDir walks up from a chromium executable path to the
// PLAYWRIGHT_BROWSERS_PATH-equivalent directory. The layout is:
//
//	<browsers>/chromium-1208/chrome-mac/Chromium.app/.../Chromium
//
// We strip up to and including the "chromium-XXXX" segment to find
// the browsers root.
func deriveBrowsersDir(execPath string) string {
	parts := strings.Split(execPath, string(os.PathSeparator))
	for i := len(parts) - 1; i >= 0; i-- {
		p := parts[i]
		if strings.HasPrefix(p, "chromium-") || strings.HasPrefix(p, "chromium_headless_shell-") || strings.HasPrefix(p, "firefox-") || strings.HasPrefix(p, "webkit-") {
			return strings.Join(parts[:i], string(os.PathSeparator))
		}
	}
	return ""
}

// Install runs the canonical install command for tool t under opts.
// Refreshes the detection cache on success.
//
// Returns:
//   - nil on success (tool now Detect-able)
//   - ErrUnknownTool when t is not in the registry
//   - ErrNotInstallable when the tool has no installer recipe
//   - ErrUserDeclined when the confirmation gate refused
//   - ErrInstallFailed (wrapped) when the subprocess returned non-zero
func Install(t Tool, opts InstallOptions) error {
	spec, ok := lookup(t)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownTool, t)
	}
	if spec.installer == nil {
		return fmt.Errorf("%w: %s", ErrNotInstallable, t)
	}
	hint := Hint(t)
	switch opts.Mode {
	case ModeBlocked:
		return fmt.Errorf("%w: %s — %s", ErrUserDeclined, t, hint)
	case ModeInteractive:
		if opts.Confirm == nil || !opts.Confirm(t, hint) {
			return fmt.Errorf("%w: %s", ErrUserDeclined, t)
		}
	case ModeAutoConfirm:
		// proceed
	}
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	if opts.Timeout == 0 {
		opts.Timeout = 15 * time.Minute
	}
	if opts.CacheDir == "" {
		opts.CacheDir = defaultCacheDir()
	}
	if err := spec.installer(opts); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrInstallFailed, t, err)
	}
	ClearCache()
	// Re-probe and confirm the install actually landed. A succeeded
	// command exit code with a still-missing binary means the install
	// recipe is wrong — surface that to the caller.
	if r := Detect(t); !r.Found {
		return fmt.Errorf("%w: %s: install ran but tool still not detected (%v)", ErrInstallFailed, t, r.Err)
	}
	return nil
}

// Hint returns a one-line human install instruction for tool t. Used
// by `kcpos doctor` output and by runtime tool error messages.
func Hint(t Tool) string {
	spec, ok := lookup(t)
	if !ok {
		return string(t) + ": unknown tool"
	}
	return spec.hint
}

// ResolvedNodePath returns the NODE_PATH-eligible directory under
// CacheDir where cached node-module installs live. Callers (runtime
// tools) prepend this to any node subprocess so `require('playwright')`
// resolves to our cached copy. Returns "" when the cache doesn't exist
// yet (Install will create it).
func ResolvedNodePath() string {
	cacheDir := defaultCacheDir()
	if cacheDir == "" {
		return ""
	}
	modDir := filepath.Join(cacheDir, "node_modules")
	if !dirExists(modDir) {
		return ""
	}
	return modDir
}

// CacheDir returns the kcpos cache directory (~/.kcpos/cache by
// default). Exposed for tools that need to write companion artifacts
// (e.g. playwright browsers/ subdirectory).
func CacheDir() string {
	return defaultCacheDir()
}

// truncate cuts s at n bytes. Used to bound ProbeOut so a chatty tool
// (npm install in a fresh project) doesn't balloon the result blob.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// runCmd is a small helper for installer functions in registry.go.
// Wraps exec.CommandContext with timeout, env, stdout/stderr routing.
func runCmd(opts InstallOptions, dir string, env []string, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		c.Dir = dir
	}
	if env != nil {
		c.Env = append(c.Environ(), env...)
	}
	c.Stdout = opts.Stdout
	c.Stderr = opts.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}
