package synthesize

import (
	"os"
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// useTempEvidenceDir reroutes EvidenceDir into a temp directory for the
// duration of one test by chdir'ing into the temp dir. Mirrors the
// helper in review/static_check_test.go.
func useTempEvidenceDir(t *testing.T) func() {
	t.Helper()
	dir, err := os.MkdirTemp("", "kcpos-typecalc-")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	prevWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if err := os.MkdirAll(core.EvidenceDir, 0o755); err != nil {
		t.Fatalf("mkdir evidence: %v", err)
	}
	return func() {
		_ = os.Chdir(prevWd)
		_ = os.RemoveAll(dir)
	}
}
