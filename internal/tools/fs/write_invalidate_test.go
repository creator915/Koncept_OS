package fs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// 2026-05-21 — write_file auto-invalidate on post-confirm edit.
//
// PB-30 batch #8 bat (5/5 confirmed) + entr (3/3 confirmed) both
// terminated rc=1 on [method-use-rule] at gate: agent had edited
// already-confirmed impl files but never re-ran confirm_object, so
// the Characterization lock's CodeHash drifted from the artifact
// hash. Gate fail at finish phase is terminal (no repair handler).
//
// Fix: when write_file overwrites an impl path owned by a confirmed
// object whose Characterization.CodeHash != HashSource(new content),
// auto-demote the object (confirmed → declared) and clear downstream
// evidence so H_confirm_one's natural re-pick drives it back through
// the chain.

// writeConfirmedFixture stages a confirmed object with characterization
// locked at the original content's hash. Returns the workdir.
func writeConfirmedFixture(t *testing.T, id, implRel, origContent string) string {
	t.Helper()
	dir := chdirToFreshProject(t)
	if err := os.MkdirAll(filepath.Join(dir, "K"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Graph: status=confirmed
	graphJSON := `{"attributes":{},"objects":{"` + id + `":{"def":"defs/` + id +
		`.h","impl":"` + implRel + `","consumes":[],"produces":[],"mutates":[],` +
		`"intent":"","temporal":null,"preconditions":"","postconditions":"",` +
		`"status":"confirmed","statusSession":null}}}`
	if err := os.WriteFile(filepath.Join(dir, "K", "graph.json"), []byte(graphJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	// Bundle: Characterization with CodeHash = HashSource(origContent)
	bundleDir := filepath.Join(dir, ".kcpos", "typecalc")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bundle := map[string]any{
		"objectId":  id,
		"version":   1,
		"updatedAt": "1970-01-01T00:00:00Z",
		"sourceHash": core.HashSource(origContent),
		"compile": map[string]any{"kind": "compile", "lang": "C", "ok": true},
		"accepted": map[string]any{
			"ok": true,
			"reasonableness": map[string]any{"verdict": "pass", "confidence": 0.95},
		},
		"characterization": map[string]any{
			"suiteId":     "equiv-" + id,
			"lang":        "C",
			"codeHash":    core.HashSource(origContent),
			"lockedCount": 12,
		},
	}
	raw, _ := json.Marshal(bundle)
	if err := os.WriteFile(filepath.Join(bundleDir, id+".json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	// Write the original impl content so write_file's sanity stat finds it.
	implPath := filepath.Join(dir, implRel)
	if err := os.MkdirAll(filepath.Dir(implPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(implPath, []byte(origContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestWriteFile_AutoInvalidate_ConfirmedImpl_DemotesAndClears(t *testing.T) {
	id := "Doit"
	implRel := "src/doit.c"
	dir := writeConfirmedFixture(t, id, implRel, "int doit(){ return 1; }\n")

	// Now overwrite with content that has a DIFFERENT hash.
	newContent := "int doit(){ return 2; }\n" // semantic change → new hash
	tool := writeFileTool()
	out, err := tool.Run(context.Background(), map[string]interface{}{
		"path":    implRel,
		"content": newContent,
	})
	if err != nil {
		t.Fatalf("write_file errored: %v", err)
	}

	// Result must mention the invalidation so the agent sees it.
	if !strings.Contains(out, "method-use-rule") || !strings.Contains(out, "auto-invalidated") {
		t.Errorf("write_file result must announce the auto-invalidation; got: %s", out)
	}

	// Graph: status reset to declared.
	graphRaw, _ := os.ReadFile(filepath.Join(dir, "K", "graph.json"))
	var g map[string]any
	_ = json.Unmarshal(graphRaw, &g)
	objs := g["objects"].(map[string]any)
	obj := objs[id].(map[string]any)
	if obj["status"] != "declared" {
		t.Errorf("status must be reset to declared, got %v", obj["status"])
	}

	// Bundle: Characterization + Accepted cleared.
	bundleRaw, _ := os.ReadFile(filepath.Join(dir, ".kcpos", "typecalc", id+".json"))
	var b map[string]any
	_ = json.Unmarshal(bundleRaw, &b)
	if b["characterization"] != nil {
		t.Errorf("characterization must be cleared after invalidation, got: %v", b["characterization"])
	}
	if b["accepted"] != nil {
		t.Errorf("accepted must be cleared after invalidation, got: %v", b["accepted"])
	}
}

func TestWriteFile_AutoInvalidate_SameHash_NoOp(t *testing.T) {
	// Writing the same content (identical hash) must NOT invalidate.
	// The hash check is the only guard against churn — re-writing the
	// same source should be idempotent for a confirmed object.
	id := "Same"
	implRel := "src/same.c"
	origContent := "int same(){ return 7; }\n"
	dir := writeConfirmedFixture(t, id, implRel, origContent)

	tool := writeFileTool()
	_, err := tool.Run(context.Background(), map[string]interface{}{
		"path":    implRel,
		"content": origContent, // same bytes = same hash
	})
	if err != nil {
		t.Fatalf("write_file errored: %v", err)
	}

	graphRaw, _ := os.ReadFile(filepath.Join(dir, "K", "graph.json"))
	var g map[string]any
	_ = json.Unmarshal(graphRaw, &g)
	objs := g["objects"].(map[string]any)
	obj := objs[id].(map[string]any)
	if obj["status"] != "confirmed" {
		t.Errorf("identical content rewrite must NOT demote — status should stay confirmed, got %v", obj["status"])
	}
}

func TestWriteFile_AutoInvalidate_NonConfirmed_NoOp(t *testing.T) {
	// Writing to a declared / implementing object must NOT touch the
	// bundle — invalidation is specifically for the confirmed→declared
	// transition that defeats the gate's method-use-rule.
	dir := chdirToFreshProject(t)
	id := "Decl"
	implRel := "src/decl.c"
	if err := os.MkdirAll(filepath.Join(dir, "K"), 0o755); err != nil {
		t.Fatal(err)
	}
	graphJSON := `{"attributes":{},"objects":{"` + id + `":{"def":"defs/` + id +
		`.h","impl":"` + implRel + `","consumes":[],"produces":[],"mutates":[],` +
		`"intent":"","temporal":null,"preconditions":"","postconditions":"",` +
		`"status":"declared","statusSession":null}}}`
	_ = os.WriteFile(filepath.Join(dir, "K", "graph.json"), []byte(graphJSON), 0o644)

	tool := writeFileTool()
	_, err := tool.Run(context.Background(), map[string]interface{}{
		"path":    implRel,
		"content": "int decl(){ return 0; }\n",
	})
	if err != nil {
		t.Fatalf("write_file errored: %v", err)
	}
	graphRaw, _ := os.ReadFile(filepath.Join(dir, "K", "graph.json"))
	var g map[string]any
	_ = json.Unmarshal(graphRaw, &g)
	objs := g["objects"].(map[string]any)
	obj := objs[id].(map[string]any)
	if obj["status"] != "declared" {
		t.Errorf("declared status must NOT be touched by invalidate path, got: %v", obj["status"])
	}
}
