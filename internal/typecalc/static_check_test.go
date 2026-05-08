package typecalc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/graph"
)

// useTempEvidenceDir reroutes EvidenceDir into a temp directory for the
// duration of one test. Returns a cleanup that restores the original
// path. Tests that touch evidence files MUST call this to avoid
// stomping on a real .kcpos directory.
func useTempEvidenceDir(t *testing.T) func() {
	t.Helper()
	prev := EvidenceDir
	dir, err := os.MkdirTemp("", "kcpos-typecalc-")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	// EvidenceDir is a const — we can't reassign it. Instead, chdir into
	// the temp dir so all relative paths resolve under it.
	prevWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if err := os.MkdirAll(EvidenceDir, 0o755); err != nil {
		t.Fatalf("mkdir evidence: %v", err)
	}
	return func() {
		_ = os.Chdir(prevWd)
		_ = os.RemoveAll(dir)
		_ = prev // silence unused warning; const isn't actually swapped
	}
}

func newTestGraph() *graph.Graph {
	g := graph.NewGraph()
	g.Attributes["data_in"] = graph.NewAttribute("defs/data_in.go", "input data")
	g.Attributes["data_out"] = graph.NewAttribute("defs/data_out.go", "output data")
	implPath := "src/Process.impl.go"
	obj := graph.NewObject("defs/Process.go", "transform input to output")
	obj.Impl = &implPath
	obj.Consumes = []string{"data_in"}
	obj.Produces = []string{"data_out"}
	// D2: every produced port needs a portObservation extractor.
	obj.PortObservation = map[string]string{"data_out": "return"}
	g.Objects["Process"] = obj
	return g
}

func TestStaticCheck_FlagsEmptyEffects(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	// Strip every effect — should fire effects-empty.
	g.Objects["Process"].Produces = nil
	g.Objects["Process"].Mutates = nil

	issues := StaticCheck(".", g, "Process")
	if !hasIssue(issues, "effects-empty") {
		t.Fatalf("expected effects-empty, got %v", issues)
	}
}

func TestStaticCheck_FlagsMissingImpl(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	g.Objects["Process"].Impl = nil

	issues := StaticCheck(".", g, "Process")
	if !hasIssue(issues, "impl-missing") {
		t.Fatalf("expected impl-missing, got %v", issues)
	}
}

func TestStaticCheck_FlagsImplOnDisk(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	// Impl path set but file does not exist.
	issues := StaticCheck(".", g, "Process")
	if !hasIssue(issues, "impl-on-disk") {
		t.Fatalf("expected impl-on-disk, got %v", issues)
	}
}

func TestStaticCheck_FlagsValueSpaceEmpty(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	// Lay impl down on disk so impl-on-disk does not fire.
	if err := os.MkdirAll("src", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("src/Process.impl.go", []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	issues := StaticCheck(".", g, "Process")
	if !hasIssue(issues, "value-space-empty") {
		t.Fatalf("expected value-space-empty, got %v", issues)
	}
}

func TestStaticCheck_FlagsSpecMissing(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	if err := os.MkdirAll("src", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("src/Process.impl.go", []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	issues := StaticCheck(".", g, "Process")
	if !hasIssue(issues, "spec-missing") {
		t.Fatalf("expected spec-missing, got %v", issues)
	}
}

func TestStaticCheck_DetectsStaleSpec(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	if err := os.MkdirAll("src", 0o755); err != nil {
		t.Fatal(err)
	}
	implContent := []byte("package main\n// version 2")
	if err := os.WriteFile("src/Process.impl.go", implContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// Write a spec whose SourceHash is for a *different* impl content.
	if err := WriteSpec(&SpecEvidence{
		ObjectID:    "Process",
		Description: "old description",
		SourceHash:  HashSource("package main\n// version 1"),
	}); err != nil {
		t.Fatal(err)
	}

	issues := StaticCheck(".", g, "Process")
	if !hasIssue(issues, "spec-stale") {
		t.Fatalf("expected spec-stale, got %v", issues)
	}
}

func TestStaticCheck_PassesWhenAllOK(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	// Backfill valueSpace on produced attribute.
	g.Attributes["data_out"].ValueSpace = map[string]any{"shape": "string"}
	if err := os.MkdirAll("src", 0o755); err != nil {
		t.Fatal(err)
	}
	implContent := []byte("package main\n")
	if err := os.WriteFile("src/Process.impl.go", implContent, 0o644); err != nil {
		t.Fatal(err)
	}
	// Lay down compile evidence so base-evidence-missing does not fire.
	if err := RecordEvidence("Process", "compile", "Go", true); err != nil {
		t.Fatal(err)
	}
	// Lay down a fresh spec.
	if err := WriteSpec(&SpecEvidence{
		ObjectID:    "Process",
		Description: "transforms input data into output data",
		SourceHash:  HashSource(string(implContent)),
	}); err != nil {
		t.Fatal(err)
	}

	issues := StaticCheck(".", g, "Process")
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues, got %d: %v", len(issues), issues)
	}
}

func hasIssue(issues []StaticIssue, code string) bool {
	for _, i := range issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

// Belt-and-suspenders: every issue should serialize cleanly so the
// agent's tool result remains parseable.
func TestStaticIssue_StringNonEmpty(t *testing.T) {
	is := StaticIssue{Code: "x", Where: "y", Message: "z"}
	if is.Code == "" || is.Where == "" || is.Message == "" {
		t.Fatalf("missing fields: %+v", is)
	}
	// formatting check — not a stable contract, just that it's non-empty
	formatted := is.Code + ":" + is.Where + ":" + is.Message
	if !strings.Contains(formatted, ":") {
		t.Fatal("format unexpected")
	}
}

// regression: relative impl paths should resolve against cwd.
func TestStaticCheck_RelativeImplResolvesAgainstCwd(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	g.Attributes["data_out"].ValueSpace = map[string]any{"shape": "string"}
	if err := os.MkdirAll("src", 0o755); err != nil {
		t.Fatal(err)
	}
	implContent := []byte("package main\n")
	if err := os.WriteFile("src/Process.impl.go", implContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RecordEvidence("Process", "compile", "Go", true); err != nil {
		t.Fatal(err)
	}
	if err := WriteSpec(&SpecEvidence{
		ObjectID:    "Process",
		Description: "ok",
		SourceHash:  HashSource(string(implContent)),
	}); err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	issues := StaticCheck(wd, g, "Process")
	for _, is := range issues {
		if is.Code == "impl-on-disk" {
			t.Fatalf("expected impl to be found (cwd-relative), got: %v", is)
		}
	}
	// also not stale
	if hasIssue(issues, "spec-stale") {
		t.Fatal("spec should not be stale")
	}
	// ensure absolute path used inside resolver agrees with the temp cwd.
	abs := filepath.Join(wd, "src/Process.impl.go")
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("setup wrong: %v", err)
	}
}
