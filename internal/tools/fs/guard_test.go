package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGuardAgentManagedPaths_HardRefuses pins the hard-refuse policy on
// agent-managed SoT paths for BOTH write_file and edit. Prior to this
// guard, edit had no path filtering — an LLM could mutate K/graph.json
// directly via `edit`, bypassing graph_merge_object's invariants (e.g.
// the v11 status=confirmed oracle) and CaptureDiff. Do not weaken: the
// graph is the chain's source of truth.
func TestGuardAgentManagedPaths_HardRefuses(t *testing.T) {
	dir := chdirToFreshProject(t)
	if err := os.MkdirAll(filepath.Join(dir, "K"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Stage a real K/graph.json + K/frags/old.js so a naive
	// LLM-style attempt has a real target to read first.
	if err := os.WriteFile(filepath.Join(dir, "K", "graph.json"),
		[]byte(`{"attributes":{},"objects":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "K", "frags"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "K", "frags", "old.js"),
		[]byte("// legacy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		path string
		want string // substring expected in error
	}{
		{"graph.json", "K/graph.json", "source of truth"},
		{"graph.expansions.json", "K/graph.expansions.json", "source of truth"},
		{"frags/old.js", "K/frags/old.js", "K/frags/*"},
	}

	writeTool := writeFileTool()
	editTool := editTool()
	for _, tc := range cases {
		t.Run("write_file/"+tc.name, func(t *testing.T) {
			_, err := writeTool.Run(context.Background(), map[string]interface{}{
				"path":    tc.path,
				"content": "garbage",
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected refusal containing %q, got: %v", tc.want, err)
			}
		})
		t.Run("edit/"+tc.name, func(t *testing.T) {
			_, err := editTool.Run(context.Background(), map[string]interface{}{
				"path":       tc.path,
				"old_string": "{",
				"new_string": "GARBAGE",
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected refusal containing %q, got: %v", tc.want, err)
			}
		})
	}

	// Verify the staged files weren't actually mutated (defense in
	// depth — error path must short-circuit before any write).
	graphAfter, _ := os.ReadFile(filepath.Join(dir, "K", "graph.json"))
	if !strings.Contains(string(graphAfter), `"objects":{}`) {
		t.Errorf("K/graph.json was mutated despite refusal: %q", graphAfter)
	}
}

// TestEdit_LegitimatePathStillAllowed is the regression guard against
// over-blocking: edit must continue to work on ordinary files.
func TestEdit_LegitimatePathStillAllowed(t *testing.T) {
	dir := chdirToFreshProject(t)
	p := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(p, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := editTool().Run(context.Background(), map[string]interface{}{
		"path":       "notes.md",
		"old_string": "world",
		"new_string": "kcpos",
	})
	if err != nil {
		t.Fatalf("edit on ordinary path should succeed, got: %v", err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "hello kcpos" {
		t.Errorf("edit did not apply: %q", got)
	}
}
