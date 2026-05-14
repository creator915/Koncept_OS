package lang

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// newScratchDir makes a working directory under .kcpos/typecalc-tmp/ for
// transient compile/test artefacts. The cleanup function removes the dir
// and its contents. Sandboxing under .kcpos rather than /tmp keeps stray
// files inside the project tree where they're easy to inspect post-mortem.
//
// The returned path is ALWAYS absolute. Runners use it both as cmd.Dir
// (relative to cwd) and as a test-target argument (which the test runner
// resolves relative to its own cwd == cmd.Dir). If we returned a relative
// path, pytest with args like `pytest -q <dir>` would see `<dir>/<dir>`
// after chdir and fail with "file or directory not found" — see the
// HE batch 2026-05-14 runPythonTest regression for the exact symptom.
func newScratchDir(env *core.RuleEnv, prefix string) (string, func(), error) {
	base := os.TempDir()
	if env != nil && env.WorkDir != "" {
		base = filepath.Join(env.WorkDir, ".kcpos", "typecalc-tmp")
		_ = os.MkdirAll(base, 0o755)
	}
	dir, err := os.MkdirTemp(base, prefix)
	if err != nil {
		return "", func() {}, err
	}
	if abs, absErr := filepath.Abs(dir); absErr == nil {
		dir = abs
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	return dir, cleanup, nil
}

// writeFile is a thin wrapper around os.WriteFile that joins dir + name.
func writeFile(dir, name, content string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}

// stageGoPackage prepares a scratch directory containing the impl
// payload AND every .go file from the impl's source directory (plus
// K/defs/ as a co-located fallback). This is how Go's "a package is
// every file in a directory" model gets shoehorned into kcpos's
// per-object chain: instead of compiling the impl alone (which fails
// the moment the impl references a type defined elsewhere), we
// reproduce the whole package directory contents in scratch.
//
// The impl payload is written under the supplied name (typically
// "code.go" for compile, or matched filename for test). Sibling files
// are copied with their original basenames; if a name collides with
// the impl payload's own basename, the payload wins (so an
// authoritative source of the function under test is the in-memory
// content, not whatever happens to be on disk).
//
// Falls back to single-file mode (one impl, no siblings) when env or
// env.ImplPath is empty — preserves the legacy behavior for unit tests
// and any caller that doesn't model file locations.
//
// Skips _test.go files in the sibling sweep — the test runner writes
// its own _test.go separately and we don't want stale or unrelated
// tests interfering with the current run.
//
// All paths returned are absolute, mirroring newScratchDir's contract.
func stageGoPackage(env *core.RuleEnv, implPayload, implName string) (string, func(), error) {
	dir, cleanup, err := newScratchDir(env, "kcpos-gopkg-")
	if err != nil {
		return "", func() {}, err
	}
	// Always write the impl payload first under its expected name.
	// Subsequent siblings won't overwrite it (see below).
	if err := writeFile(dir, implName, implPayload); err != nil {
		cleanup()
		return "", func() {}, err
	}
	// Resolve the impl's own absolute path so the sweep can skip it
	// (otherwise the file gets staged twice — once as implName, once
	// under its original basename — and Go errors on the redeclared
	// function / type).
	var implAbs string
	if env != nil && env.ImplPath != "" {
		if abs, err := filepath.Abs(env.ImplPath); err == nil {
			implAbs = abs
		}
	}
	// Also track the staged basename so explicit collisions (a sibling
	// happens to be named "code.go") don't overwrite the payload.
	seenBase := map[string]bool{implName: true}
	roots := goPackageRoots(env)
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(name, ".go") {
				continue
			}
			if strings.HasSuffix(name, "_test.go") {
				// Skip _test.go siblings — stale tests from earlier
				// runs would race the runner's fresh code_test.go.
				continue
			}
			fullPath := filepath.Join(root, name)
			if implAbs != "" {
				if abs, err := filepath.Abs(fullPath); err == nil && abs == implAbs {
					// This sibling IS the impl file we already staged
					// under implName — skip to avoid redeclaration.
					continue
				}
			}
			// v9.6.1 — skip PascalCase object def stubs under K/defs/.
			// kcpos's graph_create_object writes `K/defs/<ObjectId>.go`
			// with a function declaration whose body is a panic stub.
			// If staged alongside the real impl (which redeclares the
			// same function with a real body) Go errors out with
			// "<fn> redeclared in this block". The 2026-05-14 fx batch
			// hit this — agent ended up rewriting def to comment-only
			// as workaround. Real defs of attributes (snake_case
			// filenames under K/defs/) are kept since they declare
			// types referenced by impl.
			if isObjectDefStub(root, name) {
				continue
			}
			if seenBase[name] {
				continue
			}
			seenBase[name] = true
			data, err := os.ReadFile(fullPath)
			if err != nil {
				continue
			}
			if err := writeFile(dir, name, string(data)); err != nil {
				cleanup()
				return "", func() {}, fmt.Errorf("stage %s: %w", name, err)
			}
		}
	}
	return dir, cleanup, nil
}

// isObjectDefStub returns true for `K/defs/<PascalCaseId>.go` paths —
// these are stub function declarations kcpos creates at graph_create_object
// time. Staging them next to the real impl would redeclare the same
// function. Attribute def files (snake_case basename) are NOT flagged:
// they hold the type declarations the impl references.
//
// Heuristic: parent dir basename == "defs" AND parent's parent basename
// == "K" (so we match K/defs/ wherever the project is staged) AND the
// file basename without extension is PascalCase (uppercase first rune,
// no underscore separators).
func isObjectDefStub(rootDir, name string) bool {
	if !strings.HasSuffix(name, ".go") {
		return false
	}
	// Confirm rootDir ends in "K/defs" (after normalizing separators).
	norm := strings.ReplaceAll(rootDir, "\\", "/")
	if !strings.HasSuffix(norm, "/K/defs") && norm != "K/defs" {
		return false
	}
	stem := strings.TrimSuffix(name, ".go")
	if stem == "" {
		return false
	}
	first := rune(stem[0])
	if first < 'A' || first > 'Z' {
		return false
	}
	if strings.Contains(stem, "_") {
		// snake_case (attribute def) — keep.
		return false
	}
	return true
}

// goPackageRoots returns the source directories to sweep for sibling
// .go files when staging. Order matters — earlier dirs win on basename
// collisions (because seen-set prevents later writes). Order:
//  1. The impl file's own directory (most authoritative).
//  2. K/defs/ relative to WorkDir (co-located type decls in kcpos's
//     conventional layout).
// Returns absolute paths; missing dirs are filtered out by the caller.
func goPackageRoots(env *core.RuleEnv) []string {
	var roots []string
	if env != nil && env.ImplPath != "" {
		implDir := filepath.Dir(env.ImplPath)
		if abs, err := filepath.Abs(implDir); err == nil {
			roots = append(roots, abs)
		} else {
			roots = append(roots, implDir)
		}
	}
	workDir := "."
	if env != nil && env.WorkDir != "" {
		workDir = env.WorkDir
	}
	defsDir := filepath.Join(workDir, "K", "defs")
	if abs, err := filepath.Abs(defsDir); err == nil {
		defsDir = abs
	}
	// Only add K/defs if it's a different path from the impl dir.
	already := false
	for _, r := range roots {
		if r == defsDir {
			already = true
			break
		}
	}
	if !already {
		roots = append(roots, defsDir)
	}
	return roots
}
