package sessiontools

import (
	"os"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
)

// v10 fix: session_build must assemble from graph ImplContent (the v10
// source of truth) when the K/frags/ projection file is absent — because
// write_file is forbidden to create K/frags/ in v10, which previously
// dead-locked the HTML/pong L0→L4 path (confirm_object's pre-smoke
// session_build could never assemble → never confirmed → never L4).
// Reuses runInTempDir + writeGraph from build_test.go (same package).

func strptr(s string) *string { return &s }

// Deadlock case: object has impl=index.html + ImplContent, NO
// implFragment, NO K/frags file. Pre-fix: "nothing to assemble".
func TestSessionBuild_v10_MaterializesImplContent(t *testing.T) {
	defer runInTempDir(t)()
	g := graph.NewGraph()
	o := graph.NewObject("defs/InitGame.ts", "init")
	o.Impl = strptr("index.html")
	o.ImplContent = "function InitGame(){ return {x:0}; }"
	g.Objects["InitGame"] = o
	writeGraph(t, g)

	out, err := runSessionBuild("inline")
	if err != nil {
		t.Fatalf("v10 implContent must assemble, got error: %v", err)
	}
	if !strings.Contains(out, "assembled 1 fragment") {
		t.Fatalf("expected assembly of 1 fragment, got: %s", out)
	}
	b, rerr := os.ReadFile("K/frags/InitGame.js")
	if rerr != nil {
		t.Fatalf("session_build must materialise K/frags/InitGame.js from ImplContent: %v", rerr)
	}
	if !strings.Contains(string(b), "function InitGame()") {
		t.Errorf("materialised fragment must hold ImplContent, got: %s", b)
	}
	html, herr := os.ReadFile("index.html")
	if herr != nil || !strings.Contains(string(html), "function InitGame()") {
		t.Errorf("index.html must contain the assembled code, err=%v", herr)
	}
}

// Guard: an on-disk fragment is authoritative — must NOT be clobbered by
// ImplContent (materialise only when the file is absent).
func TestSessionBuild_v10_DiskFragmentWins(t *testing.T) {
	defer runInTempDir(t)()
	if err := os.MkdirAll("K/frags", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("K/frags/Foo.js", []byte("function Foo(){ return 'DISK'; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := graph.NewGraph()
	o := graph.NewObject("defs/Foo.ts", "f")
	o.Impl = strptr("index.html")
	o.ImplFragment = strptr("K/frags/Foo.js")
	o.ImplContent = "function Foo(){ return 'CONTENT'; }"
	g.Objects["Foo"] = o
	writeGraph(t, g)

	if _, err := runSessionBuild("inline"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile("K/frags/Foo.js")
	if !strings.Contains(string(b), "DISK") || strings.Contains(string(b), "CONTENT") {
		t.Errorf("existing disk fragment must NOT be overwritten by ImplContent, got: %s", b)
	}
}

// Guard: a plain multi-file object (impl set, no fragment, no
// ImplContent) must STILL be skipped — don't over-block.
func TestSessionBuild_v10_PlainMultiFileStillSkipped(t *testing.T) {
	defer runInTempDir(t)()
	g := graph.NewGraph()
	o := graph.NewObject("defs/Bar.ts", "b")
	o.Impl = strptr("src/Bar.go")
	g.Objects["Bar"] = o
	writeGraph(t, g)

	out, err := runSessionBuild("inline")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nothing to assemble") {
		t.Errorf("plain multi-file object must still be a no-op, got: %s", out)
	}
}
