package typecalc

import (
	"os"
	"path/filepath"
)

// newScratchDir makes a working directory under .kcpos/typecalc-tmp/ for
// transient compile/test artefacts. The cleanup function removes the dir
// and its contents. Sandboxing under .kcpos rather than /tmp keeps stray
// files inside the project tree where they're easy to inspect post-mortem.
func newScratchDir(env *RuleEnv, prefix string) (string, func(), error) {
	base := os.TempDir()
	if env != nil && env.WorkDir != "" {
		base = filepath.Join(env.WorkDir, ".kcpos", "typecalc-tmp")
		_ = os.MkdirAll(base, 0o755)
	}
	dir, err := os.MkdirTemp(base, prefix)
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	return dir, cleanup, nil
}

// writeFile is a thin wrapper around os.WriteFile that joins dir + name.
// Used by the language-specific runners in test.go to drop source files
// into a scratch dir before invoking the runner.
func writeFile(dir, name, content string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}
