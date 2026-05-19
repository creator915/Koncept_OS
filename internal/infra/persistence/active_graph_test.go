package persistence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/domain/session"
)

// P1.1.2 regression guards for active-graph resolution.

// Root session (no parent) → top-level K/graph.json.
func TestActiveGraphPath_RootGoesTopLevel(t *testing.T) {
	root := session.New("s_root", "", "task", session.Input{})
	if got := ActiveGraphPath(root); got != GraphDefaultPath {
		t.Fatalf("root session must map to %q, got %q", GraphDefaultPath, got)
	}
}

// An EXPANSION session (ExpandsObject set) → its own sub-hypergraph.
func TestActiveGraphPath_ExpansionSessionGoesExpansion(t *testing.T) {
	sub := session.New("s_physics", "s_root", "expand UpdatePhysics", session.Input{})
	sub.ExpandsObject = "UpdatePhysics" // <- this, not Parent, makes it an expansion
	want := ExpansionGraphPath("s_physics")
	if got := ActiveGraphPath(sub); got != want {
		t.Fatalf("expansion session must map to %q, got %q", want, got)
	}
	if want == GraphDefaultPath {
		t.Fatal("expansion path must NOT be the top-level path")
	}
}

// REGRESSION GUARD (pong-01, 2026-05-19): an ORDINARY child session —
// the pervasive spawn_subagent pattern, parent set but NO ExpandsObject
// — MUST resolve to the top-level graph, NOT a phantom expansion. The
// previous keying on Parent broke every real multi-agent run. This must
// never come back.
func TestActiveGraphPath_PlainChildSessionStaysTopLevel(t *testing.T) {
	child := session.New("s_impl_initgame", "s_pingpong", "implement InitGame", session.Input{})
	// child.ExpandsObject == "" (ordinary subagent child)
	if got := ActiveGraphPath(child); got != GraphDefaultPath {
		t.Fatalf("ordinary child session MUST stay top-level (pong-01 dead-lock regression), got %q", got)
	}
}

// nil session → top-level safe fallback (degradation: no active
// session ⇒ today's flat-graph behaviour).
func TestActiveGraphPath_NilSafeFallback(t *testing.T) {
	if got := ActiveGraphPath(nil); got != GraphDefaultPath {
		t.Fatalf("nil session must safely fall back to %q, got %q", GraphDefaultPath, got)
	}
}

// Malformed sub-session id: Load/Save must FAIL CLOSED with a
// diagnosable error rather than silently writing into the top-level
// graph (the pollution this whole layer exists to prevent).
func TestActiveGraph_MalformedSubSessionFailsClosed(t *testing.T) {
	bad := &session.Session{ID: "../../evil", Parent: "s_root", ExpandsObject: "X"}

	_, lerr := LoadActiveGraph(bad)
	if lerr == nil {
		t.Fatal("LoadActiveGraph must fail closed on malformed sub-session id")
	}
	if !strings.Contains(lerr.Error(), "malformed expansion-session id") {
		t.Errorf("error must be diagnosable, got: %v", lerr)
	}
	if serr := SaveActiveGraph(bad, graph.NewGraph()); serr == nil {
		t.Fatal("SaveActiveGraph must fail closed on malformed sub-session id")
	}
}

// End-to-end isolation: writing via root vs via a sub-session must land
// in different files and never cross-contaminate.
func TestLoadSaveActiveGraph_RootVsSubIsolation(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	root := session.New("s_root", "", "root", session.Input{})
	sub := session.New("s_child", "s_root", "child", session.Input{})
	sub.ExpandsObject = "SomeObj" // expansion session (keyed on ExpandsObject)

	rg := graph.NewGraph()
	rg.Objects["RootObj"] = graph.NewObject("defs/RootObj.ts", "root")
	if err := SaveActiveGraph(root, rg); err != nil {
		t.Fatal(err)
	}
	sg := graph.NewGraph()
	sg.Objects["ChildObj"] = graph.NewObject("defs/ChildObj.ts", "child")
	if err := SaveActiveGraph(sub, sg); err != nil {
		t.Fatal(err)
	}

	// Files landed where expected.
	if _, err := os.Stat(filepath.Join(dir, "K", "graph.json")); err != nil {
		t.Errorf("root write must hit K/graph.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "K", "expansions", "s_child", "graph.json")); err != nil {
		t.Errorf("sub write must hit K/expansions/s_child/graph.json: %v", err)
	}

	// No cross-contamination.
	backRoot, err := LoadActiveGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, has := backRoot.Objects["ChildObj"]; has {
		t.Error("child object leaked into the top-level graph")
	}
	backSub, err := LoadActiveGraph(sub)
	if err != nil {
		t.Fatal(err)
	}
	if _, has := backSub.Objects["RootObj"]; has {
		t.Error("root object leaked into the sub-session graph")
	}
	if _, has := backSub.Objects["ChildObj"]; !has {
		t.Error("sub-session lost its own object")
	}
}
