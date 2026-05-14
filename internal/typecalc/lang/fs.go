package lang

import (
	"os"
	"path/filepath"

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
