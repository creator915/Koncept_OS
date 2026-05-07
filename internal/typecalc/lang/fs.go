package lang

import (
	"os"
	"path/filepath"

	"github.com/creator915/Koncept_OS/internal/typecalc"
)

// newScratchDir makes a working directory under .kcpos/typecalc-tmp/ for
// transient compile/test artefacts. The cleanup function removes the dir
// and its contents. Sandboxing under .kcpos rather than /tmp keeps stray
// files inside the project tree where they're easy to inspect post-mortem.
func newScratchDir(env *typecalc.RuleEnv, prefix string) (string, func(), error) {
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
func writeFile(dir, name, content string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}
