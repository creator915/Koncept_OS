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

// TestGraphMergeObject_HTMLAutoDerivesImplFragment verifies v12
// behaviour: setting impl to an .html / .htm path WITHOUT
// implFragment is accepted, and kcpos auto-derives
// implFragment=K/frags/<id>.js. Pre-v12 (v9.3.1) the tool refused
// such a patch; v12 hides the K/frags/ path from the agent entirely
// by filling it in server-side. The v93-04 monolithic-inline failure
// mode is still blocked at session_build / AP11 / AP16, just not at
// graph_merge_object anymore.
func TestGraphMergeObject_HTMLAutoDerivesImplFragment(t *testing.T) {
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

	// Case 1 (v12): impl=index.html WITHOUT implFragment — accepted;
	// implFragment auto-derived to K/frags/Foo.js.
	_, err := run(context.Background(), map[string]interface{}{
		"id":    "Foo",
		"patch": `{"impl":"index.html"}`,
	})
	if err != nil {
		t.Fatalf("v12: setting impl=index.html without implFragment must succeed (auto-derive); got: %v", err)
	}
	g2, lerr := persistence.LoadGraph(persistence.GraphDefaultPath)
	if lerr != nil {
		t.Fatal(lerr)
	}
	got := g2.Objects["Foo"]
	if got == nil || got.ImplFragment == nil || *got.ImplFragment != "K/frags/Foo.js" {
		var frag string
		if got != nil && got.ImplFragment != nil {
			frag = *got.ImplFragment
		}
		t.Errorf("expected auto-derived implFragment=K/frags/Foo.js; got %q", frag)
	}

	// Case 2: impl=index.html WITH explicit implFragment in same patch —
	// still accepted; explicit value wins over auto-derive. Use a
	// second object (Bar) added to the existing graph so Foo's state
	// from Case 1 is preserved for Case 3.
	g2.Objects["Bar"] = graph.NewObject("K/defs/Bar.js", "Bar intent")
	_ = persistence.SaveGraph(persistence.GraphDefaultPath, g2)
	_, err = run(context.Background(), map[string]interface{}{
		"id":    "Bar",
		"patch": `{"impl":"index.html","implFragment":"K/frags/Bar.js"}`,
	})
	if err != nil {
		t.Fatalf("setting impl + explicit implFragment together must succeed; got: %v", err)
	}

	// Case 3: re-setting impl=index.html on an object that already has
	// implFragment (Foo from Case 1's auto-derive) — accepted.
	_, err = run(context.Background(), map[string]interface{}{
		"id":    "Foo",
		"patch": `{"impl":"index.html"}`,
	})
	if err != nil {
		t.Fatalf("re-setting impl=index.html when implFragment is already on the object must succeed; got: %v", err)
	}
	_ = strings.Contains // keep import used
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
	// trip later if something looks. NOTE: 2026-05-21 — Go testable-contract
	// guard refuses `package main` impls, so this fixture uses a sub-package
	// to keep the test focused on the implFragment-not-required invariant
	// (the package-main guard has its own dedicated test).
	_ = os.MkdirAll("src", 0o755)
	_ = os.WriteFile("src/Bar.go", []byte("package bar\n"), 0o644)

	tool := graphMergeObjectTool()
	_, err := tool.Run(context.Background(), map[string]interface{}{
		"id":    "Bar",
		"patch": `{"impl":"src/Bar.go"}`,
	})
	if err != nil {
		t.Fatalf("non-HTML impl must not require implFragment; got: %v", err)
	}
}
