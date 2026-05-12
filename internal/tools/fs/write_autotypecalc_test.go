package fs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for §5.5: write_file auto-triggers typecalc_compile when the
// path resembles an implementation file. We verify three paths:
//   1. *.impl.go pattern → evidence written for the inferred object id
//   2. graph.Object.impl == path → evidence written for the matched id
//   3. random docs path → no auto-typecalc, no evidence written

func chdirToFreshProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	return dir
}

func evidencePath(dir, id string) string {
	// v9.0: unified bundle path
	return filepath.Join(dir, ".kcpos", "typecalc", id+".json")
}

func TestWriteFile_AutoTypecalc_ImplPattern_GoOK(t *testing.T) {
	dir := chdirToFreshProject(t)
	tool := writeFileTool()
	args := map[string]interface{}{
		"path":    "src/Foo.impl.go",
		"content": "package main\nfunc Foo() int { return 1 }\n",
	}
	out, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[auto-typecalc]") {
		t.Errorf("expected auto-typecalc banner in output: %s", out)
	}
	// Evidence should exist for "Foo" — v9.0 unified bundle with a
	// Compile section.
	raw, err := os.ReadFile(evidencePath(dir, "Foo"))
	if err != nil {
		t.Fatalf("evidence file missing: %v", err)
	}
	var rec struct {
		ObjectID string `json:"objectId"`
		Compile  *struct {
			Kind string `json:"kind"`
			OK   bool   `json:"ok"`
		} `json:"compile"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.ObjectID != "Foo" || rec.Compile == nil || rec.Compile.Kind != "compile" || !rec.Compile.OK {
		t.Errorf("evidence wrong: %+v", rec)
	}
}

func TestWriteFile_AutoTypecalc_ImplPattern_GoFails(t *testing.T) {
	dir := chdirToFreshProject(t)
	tool := writeFileTool()
	args := map[string]interface{}{
		"path":    "src/Bad.impl.go",
		"content": "this is not Go code",
	}
	out, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	// File should still be written (best-effort write semantic).
	if _, err := os.Stat(filepath.Join(dir, "src", "Bad.impl.go")); err != nil {
		t.Errorf("file should be written: %v", err)
	}
	// But evidence should NOT exist.
	if _, err := os.Stat(evidencePath(dir, "Bad")); err == nil {
		t.Errorf("evidence should NOT be written for failed compile")
	}
	if !strings.Contains(out, "COMPILE FAILED") {
		t.Errorf("expected failure banner: %s", out)
	}
}

func TestWriteFile_AutoTypecalc_NonImplPath_NoOp(t *testing.T) {
	dir := chdirToFreshProject(t)
	tool := writeFileTool()
	args := map[string]interface{}{
		"path":    "docs/README.md",
		"content": "# Hello",
	}
	out, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "[auto-typecalc]") {
		t.Errorf("docs files should NOT auto-trigger: %s", out)
	}
	// No evidence dir created at all (v9.0 bundle dir).
	if _, err := os.Stat(filepath.Join(dir, ".kcpos", "typecalc")); err == nil {
		t.Errorf("evidence dir should not exist for docs writes")
	}
}

func TestWriteFile_AutoTypecalc_GraphImplMatch(t *testing.T) {
	dir := chdirToFreshProject(t)
	// Create K/graph.json with an object whose impl is "main.go"
	if err := os.MkdirAll(filepath.Join(dir, "K"), 0o755); err != nil {
		t.Fatal(err)
	}
	graphJSON := `{
	  "attributes": {"x": {"def":"defs/x.ts","refines":[],"intent":"","valueSpace":null,"confirmedOps":[],"laws":[],"status":"declared","statusSession":null}},
	  "objects": {"Bar": {"def":"defs/Bar.ts","impl":"main.go","consumes":[],"produces":["x"],"mutates":[],"intent":"","temporal":null,"preconditions":"","postconditions":"","status":"declared","statusSession":null}}
	}`
	if err := os.WriteFile(filepath.Join(dir, "K", "graph.json"), []byte(graphJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := writeFileTool()
	args := map[string]interface{}{
		"path":    "main.go",
		"content": "package main\nfunc main() {}\n",
	}
	if _, err := tool.Run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	// Evidence should be attributed to "Bar" (graph match), not the
	// filename-derived id "main".
	if _, err := os.Stat(evidencePath(dir, "Bar")); err != nil {
		t.Errorf("evidence for Bar missing: %v", err)
	}
	if _, err := os.Stat(evidencePath(dir, "main")); err == nil {
		t.Errorf("graph match should win over filename heuristic")
	}
}
