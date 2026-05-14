package lang

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// TestRunPythonTest_PathBugRegression covers the 2026-05-14 HE batch
// bug: newScratchDir returned a relative path, runPythonTest passed
// `dir` as BOTH cmd.Dir AND a pytest positional argument, and pytest
// resolved the arg relative to its own cwd (== dir) — looking for
// `<dir>/<dir>/test_code.py` and bailing with "file or directory not
// found". Symptoms in HE batch logs: every Python project's
// typecalc_test failed; agents resorted to editing evidence files to
// flip ok:false → ok:true (HE0), spawning inotify watchers to inject
// __init__.py mid-run (HE22), and rewriting the impl as JavaScript so
// the JS harness chain would run (HE24). The fix has two parts:
// (a) newScratchDir always returns an absolute path, (b) runPythonTest
// uses "." (matching runGoTest) instead of dir as the pytest target.
// This test exercises both.
func TestRunPythonTest_PathBugRegression(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		if _, err := exec.LookPath("python"); err != nil {
			t.Skip("python not available")
		}
	}
	// Use a relative WorkDir to reproduce the original failure mode —
	// before the fix, newScratchDir would propagate that as a relative
	// dir, and pytest would fail.
	cwd := t.TempDir()
	rel, err := filepath.Rel(mustGetwd(t), cwd)
	if err != nil {
		t.Fatalf("rel: %v", err)
	}
	env := &core.RuleEnv{WorkDir: rel}

	implSrc := "def add(a, b):\n    return a + b\n"
	testSrc := `import sys, os
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from code import add

def test_add_pos():
    assert add(2, 3) == 5

def test_add_zero():
    assert add(0, 0) == 0
`
	compiled := core.New(core.KindCode, implSrc).WithState(core.StateCompiled).WithLang(core.LangPython)
	suite := core.New(core.KindTestSuite, testSrc).WithLang(core.LangPython)

	result, err := runPythonTest(context.Background(), env, compiled, suite)
	if err != nil {
		t.Fatalf("runPythonTest returned error: %v", err)
	}
	if result.State != core.StateTestedPass {
		// Pull the failing-case detail (if present) into the failure
		// message so the cause is visible in CI without re-running.
		detail := result.Payload
		if strings.Contains(detail, "file or directory not found") || strings.Contains(detail, "No such file") {
			t.Fatalf("runPythonTest hit the relative-path bug (test runner couldn't locate test file): %s", detail)
		}
		t.Fatalf("expected TestedPass, got %s\n%s", result.Tag(), detail)
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := exec.Command("pwd").Output()
	if err != nil {
		t.Fatalf("pwd: %v", err)
	}
	return strings.TrimSpace(string(wd))
}
