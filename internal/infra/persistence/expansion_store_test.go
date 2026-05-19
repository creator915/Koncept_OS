package persistence

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
)

// P1.1.1 regression guards for layered hypergraph storage.

// Path must be exactly K/expansions/<sid>/graph.json.
func TestExpansionGraphPath(t *testing.T) {
	got := ExpansionGraphPath("s_physics")
	want := filepath.Join("K", "expansions", "s_physics", "graph.json")
	if got != want {
		t.Fatalf("ExpansionGraphPath = %q, want %q", got, want)
	}
	if ExpansionGraphPath("s_render") == ExpansionGraphPath("s_physics") {
		t.Fatal("distinct sessions must map to distinct files")
	}
}

// SaveExpansionGraph must create K/expansions/<sid>/ on disk (dir
// auto-creation), and the sub-graph must NOT leak into the top-level
// K/graph.json (isolation — the core invariant for non-polluting
// rollback later).
func TestExpansionGraph_IsolationAndDirCreation(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	// Top-level graph holds object A.
	top := graph.NewGraph()
	top.Objects["A"] = graph.NewObject("defs/A.ts", "top object")
	if err := SaveGraph(GraphDefaultPath, top); err != nil {
		t.Fatal(err)
	}

	// Expansion s1 holds a DIFFERENT object B.
	sub := graph.NewGraph()
	sub.Objects["B"] = graph.NewObject("defs/B.ts", "child object")
	if err := SaveExpansionGraph("s1", sub); err != nil {
		t.Fatal(err)
	}

	// Dir auto-created on disk.
	if _, err := os.Stat(filepath.Join(dir, "K", "expansions", "s1", "graph.json")); err != nil {
		t.Fatalf("expansion dir/file not created: %v", err)
	}

	// Top-level still ONLY A — no cross-contamination from the sub write.
	reTop, err := LoadGraph(GraphDefaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, hasA := reTop.Objects["A"]; !hasA {
		t.Error("top-level lost its own object A")
	}
	if _, hasB := reTop.Objects["B"]; hasB {
		t.Error("sub-graph object B leaked into top-level K/graph.json")
	}

	// Expansion s1 still ONLY B.
	reSub, err := LoadExpansionGraphOrInit("s1")
	if err != nil {
		t.Fatal(err)
	}
	if _, hasB := reSub.Objects["B"]; !hasB {
		t.Error("expansion lost its own object B")
	}
	if _, hasA := reSub.Objects["A"]; hasA {
		t.Error("top-level object A leaked into expansion s1")
	}
}

// Missing expansion file must init-empty (degrades like the flat path),
// not error.
func TestLoadExpansionGraphOrInit_AbsentIsEmpty(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	g, err := LoadExpansionGraphOrInit("never_created")
	if err != nil {
		t.Fatalf("absent expansion must init-empty, got err: %v", err)
	}
	if g == nil || len(g.Objects) != 0 || len(g.Attributes) != 0 {
		t.Fatalf("absent expansion must be empty graph, got %+v", g)
	}
}
