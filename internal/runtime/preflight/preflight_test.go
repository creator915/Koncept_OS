package preflight

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRegistryCoverage ensures every registered tool has the fields
// callers rely on: detection path (cmd or nodeModule), and a non-empty
// hint string. This is the "registered a tool but forgot a field"
// catcher.
func TestRegistryCoverage(t *testing.T) {
	for _, s := range registry {
		if s.tool == "" {
			t.Errorf("registry entry has empty tool id")
			continue
		}
		if len(s.detectCmd) == 0 && s.nodeModule == "" {
			t.Errorf("tool %s: no detectCmd and no nodeModule (cannot be probed)", s.tool)
		}
		if s.hint == "" {
			t.Errorf("tool %s: empty hint (kcpos doctor and runtime errors will be unhelpful)", s.tool)
		}
	}
}

// TestVersionRegexes asserts the canonical version regexes capture what
// kcpos expects from each tool's --version output.
func TestVersionRegexes(t *testing.T) {
	cases := []struct {
		name    string
		re      string
		input   string
		want    string
		wantNot bool
	}{
		{"node-v18", "reVPrefixed", "v18.20.4\n", "18.20.4", false},
		{"node-v22-beta", "reVPrefixed", "v22.0.0-beta.1\n", "22.0.0-beta.1", false},
		{"npm-plain", "rePlain", "10.5.0\n", "10.5.0", false},
		{"go-darwin", "reGo", "go version go1.22.1 darwin/arm64\n", "1.22.1", false},
		{"py-310", "rePy", "Python 3.10.13\n", "3.10.13", false},
		{"py-no-patch", "rePy", "Python 3.11\n", "3.11", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var re = map[string]*struct{ name string }{
				"reVPrefixed": {"reVPrefixed"},
				"rePlain":     {"rePlain"},
				"reGo":        {"reGo"},
				"rePy":        {"rePy"},
			}[tc.re]
			if re == nil {
				t.Fatalf("unknown regex %q", tc.re)
			}
			var got []string
			switch tc.re {
			case "reVPrefixed":
				got = reVPrefixed.FindStringSubmatch(tc.input)
			case "rePlain":
				got = rePlain.FindStringSubmatch(tc.input)
			case "reGo":
				got = reGo.FindStringSubmatch(tc.input)
			case "rePy":
				got = rePy.FindStringSubmatch(tc.input)
			}
			if tc.wantNot {
				if len(got) >= 2 {
					t.Errorf("expected no match, got %v", got)
				}
				return
			}
			if len(got) < 2 {
				t.Fatalf("no match on %q", tc.input)
			}
			if got[1] != tc.want {
				t.Errorf("version = %q, want %q", got[1], tc.want)
			}
		})
	}
}

// TestDetectMissingTool drives Detect against an isolated PATH where
// the requested binary is absent. Should return Found=false with an
// Err describing the failure.
func TestDetectMissingTool(t *testing.T) {
	// Empty PATH means exec.LookPath fails for any name.
	t.Setenv("PATH", t.TempDir()) // only the temp dir, no binaries
	ClearCache()
	r := Detect(Node)
	if r.Found {
		t.Errorf("expected Node not found with empty PATH, got %+v", r)
	}
	if r.Err == nil {
		t.Error("expected non-nil Err when tool missing")
	}
}

// TestDetectStubbed sets up a stub binary on PATH that echoes a
// recognizable version string, then verifies Detect parses it.
func TestDetectStubbed(t *testing.T) {
	tmp := t.TempDir()
	stubNode := filepath.Join(tmp, "node")
	body := "#!/bin/sh\necho 'v20.0.0'\n"
	if err := os.WriteFile(stubNode, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PATH", tmp)
	ClearCache()
	r := Detect(Node)
	if !r.Found {
		t.Fatalf("expected Node found via stub, got %+v", r)
	}
	if r.Version != "20.0.0" {
		t.Errorf("version = %q, want 20.0.0", r.Version)
	}
	if r.Path == "" {
		t.Errorf("expected Path resolved, got empty")
	}
}

// TestHint returns the registered hint for a known tool.
func TestHint(t *testing.T) {
	h := Hint(Playwright)
	if !strings.Contains(h, "playwright") {
		t.Errorf("Hint(Playwright) = %q, expected to mention playwright", h)
	}
	h2 := Hint(Tool("nonexistent"))
	if !strings.Contains(h2, "unknown") {
		t.Errorf("Hint(unknown) should say unknown, got %q", h2)
	}
}

// TestInstallNotInstallable asserts Install returns ErrNotInstallable
// for tools without an install recipe (e.g. Go).
func TestInstallNotInstallable(t *testing.T) {
	err := Install(Go, InstallOptions{Mode: ModeAutoConfirm})
	if err == nil {
		t.Fatal("expected error for Install(Go)")
	}
	if !errorsIs(err, ErrNotInstallable) {
		t.Errorf("expected ErrNotInstallable, got %v", err)
	}
}

// TestInstallBlocked asserts ModeBlocked returns ErrUserDeclined.
func TestInstallBlocked(t *testing.T) {
	err := Install(Playwright, InstallOptions{Mode: ModeBlocked})
	if err == nil {
		t.Fatal("expected error for ModeBlocked")
	}
	if !errorsIs(err, ErrUserDeclined) {
		t.Errorf("expected ErrUserDeclined, got %v", err)
	}
}

// TestCacheRoundTrip verifies Detect caches results and ClearCache
// forces a re-probe.
func TestCacheRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	stub := filepath.Join(tmp, "node")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho v1.0.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmp)
	ClearCache()
	r1 := Detect(Node)
	if !r1.Found {
		t.Fatalf("first Detect: %+v", r1)
	}
	// Remove the stub; Detect should still return cached Found=true.
	os.Remove(stub)
	r2 := Detect(Node)
	if !r2.Found {
		t.Errorf("cached Detect lost Found=true after removal")
	}
	ClearCache()
	r3 := Detect(Node)
	if r3.Found {
		t.Errorf("post-ClearCache Detect should re-probe and miss; got %+v", r3)
	}
}

// errorsIs is a local stand-in for errors.Is to keep test imports tight.
// Production code uses errors.Is via fmt.Errorf("%w") wrapping.
func errorsIs(err, target error) bool {
	if err == nil {
		return target == nil
	}
	return strings.Contains(err.Error(), target.Error())
}
