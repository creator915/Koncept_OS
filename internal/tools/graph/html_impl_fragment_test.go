package graphtools

import (
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
)

// TestGraphMergeObject_HTMLRequiresImplFragment verifies v9.3.1
// enforcement: setting impl to an .html / .htm path requires
// implFragment in the same patch (or already present on the object).
// v93-04 monolithic retro: agent set impl=index.html with no
// implFragment → wrote a 1176-line monolithic file → review's
// implFragment-aware optimisation became inert → token overflow.
func TestGraphMergeObject_HTMLRequiresImplFragment(t *testing.T) {
	dir := chdirTempProject(t)
	_ = dir

	// Project root must contain index.html so existing checks fire on a
	// representative HTML project.
	if err := os.WriteFile("index.html", []byte("<!doctype html><html></html>\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	g := graph.NewGraph()
	defPath := "K/defs/Foo.js"
	obj := graph.NewObject(defPath, "Foo intent")
	g.Objects["Foo"] = obj
	if err := os.MkdirAll(filepath.Dir(persistence.GraphDefaultPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := persistence.SaveGraph(persistence.GraphDefaultPath, g); err != nil {
		t.Fatal(err)
	}

	tool := graphMergeObjectTool()
	run := tool.Run

	// Case 1: impl=index.html WITHOUT implFragment — must be refused.
	_, err := run(context.Background(), map[string]interface{}{
		"id":    "Foo",
		"patch": `{"impl":"index.html"}`,
	})
	if err == nil {
		t.Fatal("expected error refusing impl=index.html without implFragment, got nil")
	}
	if !strings.Contains(err.Error(), "implFragment") {
		t.Errorf("error must mention implFragment requirement; got: %v", err)
	}
	if !strings.Contains(err.Error(), "K/frags/Foo.js") {
		t.Errorf("error must suggest the canonical fragment path; got: %v", err)
	}

	// Case 2: impl=index.html WITH implFragment in same patch — accepted.
	_, err = run(context.Background(), map[string]interface{}{
		"id":    "Foo",
		"patch": `{"impl":"index.html","implFragment":"K/frags/Foo.js"}`,
	})
	if err != nil {
		t.Fatalf("setting impl + implFragment together must succeed; got: %v", err)
	}

	// Case 3: impl=index.html setting again (separately) when implFragment
	// already exists on the object — accepted.
	_, err = run(context.Background(), map[string]interface{}{
		"id":    "Foo",
		"patch": `{"impl":"index.html"}`,
	})
	if err != nil {
		t.Fatalf("re-setting impl=index.html when implFragment is already on the object must succeed; got: %v", err)
	}
}

// TestGraphMergeObject_NonHTMLDoesNotRequireFragment confirms the
// enforcement is HTML-scoped. Multi-file projects (.go / .ts / etc.)
// don't use fragments and must not be forced to declare implFragment.
func TestGraphMergeObject_NonHTMLDoesNotRequireFragment(t *testing.T) {
	chdirTempProject(t)
	// No index.html in root; this is a Go-style project.

	g := graph.NewGraph()
	obj := graph.NewObject("K/defs/Bar.go", "Bar intent")
	g.Objects["Bar"] = obj
	if err := os.MkdirAll(filepath.Dir(persistence.GraphDefaultPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := persistence.SaveGraph(persistence.GraphDefaultPath, g); err != nil {
		t.Fatal(err)
	}
	// Create the impl file so the static check on impl-on-disk doesn't
	// trip later if something looks.
	_ = os.MkdirAll("src", 0o755)
	_ = os.WriteFile("src/Bar.go", []byte("package main\n"), 0o644)

	tool := graphMergeObjectTool()
	_, err := tool.Run(context.Background(), map[string]interface{}{
		"id":    "Bar",
		"patch": `{"impl":"src/Bar.go"}`,
	})
	if err != nil {
		t.Fatalf("non-HTML impl must not require implFragment; got: %v", err)
	}
}
