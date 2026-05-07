package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chdirTo sets cwd for the duration of the test. defExistenceHook resolves
// non-absolute paths against cwd, so tests need a clean root to avoid
// false negatives from random files in the dev tree.
func chdirTo(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// --- def-existence hook ---

func TestDefExistence_FiresWhenFileMissing(t *testing.T) {
	chdirTo(t, t.TempDir())
	h := &defExistenceHook{}
	args := `{"id": "Op", "intent": "...", "def": "defs/Op.ts"}`
	v := h.After("graph_create_object", args, "ok")
	if v == "" {
		t.Fatal("expected violation when def file missing")
	}
	if !strings.Contains(v, "defs/Op.ts") {
		t.Errorf("violation should reference def path, got: %s", v)
	}
}

func TestDefExistence_PassesWhenFileExists(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "defs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "defs", "Op.ts"), []byte("export type Op = ...\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	chdirTo(t, dir)

	h := &defExistenceHook{}
	args := `{"id": "Op", "intent": "...", "def": "defs/Op.ts"}`
	if v := h.After("graph_create_object", args, "ok"); v != "" {
		t.Errorf("should pass when file exists, got violation: %s", v)
	}
}

func TestDefExistence_UsesDefaultWhenDefEmpty(t *testing.T) {
	chdirTo(t, t.TempDir())
	h := &defExistenceHook{}
	// No `def` in args → tool defaults to defs/<id>.ts; hook should also
	// check that path.
	args := `{"id": "Op", "intent": "..."}`
	v := h.After("graph_create_object", args, "ok")
	if v == "" {
		t.Fatal("expected violation: defs/Op.ts default doesn't exist")
	}
	if !strings.Contains(v, "defs/Op.ts") {
		t.Errorf("should mention default path: %s", v)
	}
}

func TestDefExistence_IgnoresUnrelatedTools(t *testing.T) {
	chdirTo(t, t.TempDir())
	h := &defExistenceHook{}
	if v := h.After("read_file", `{"path":"x"}`, "..."); v != "" {
		t.Errorf("should ignore unrelated tools, got: %s", v)
	}
	if v := h.After("graph_link_consume", `{"object":"Op","attribute":"a"}`, "..."); v != "" {
		t.Errorf("should ignore link tools, got: %s", v)
	}
}

// --- confirmed-impl hook ---

func TestConfirmedImpl_FiresWhenStatusConfirmedButImplMissing(t *testing.T) {
	dir := t.TempDir()
	chdirTo(t, dir)
	// Need K/graph.json with object Op (no impl) to satisfy the lookup.
	if err := os.MkdirAll(filepath.Join(dir, "K"), 0o755); err != nil {
		t.Fatal(err)
	}
	graphJSON := `{
  "attributes": {},
  "objects": {
    "Op": {
      "def": "defs/Op.ts",
      "impl": null,
      "consumes": [],
      "produces": [],
      "intent": "x",
      "temporal": null,
      "preconditions": "",
      "postconditions": "",
      "status": "declared",
      "statusSession": null
    }
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "K", "graph.json"), []byte(graphJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	h := &confirmedImplHook{}
	args := `{"id": "Op", "patch": "{\"status\":\"confirmed\"}"}`
	v := h.After("graph_merge_object", args, "ok")
	if v == "" {
		t.Fatal("expected violation: confirmed but no impl set anywhere")
	}
	if !strings.Contains(v, "Op") || !strings.Contains(v, "impl") {
		t.Errorf("violation should mention object id and impl: %s", v)
	}
}

func TestConfirmedImpl_FiresWhenImplFileMissing(t *testing.T) {
	dir := t.TempDir()
	chdirTo(t, dir)
	h := &confirmedImplHook{}
	args := `{"id": "Op", "patch": "{\"status\":\"confirmed\",\"impl\":\"src/op.go\"}"}`
	v := h.After("graph_merge_object", args, "ok")
	if v == "" {
		t.Fatal("expected violation: impl file missing on disk")
	}
	if !strings.Contains(v, "src/op.go") {
		t.Errorf("violation should mention impl path: %s", v)
	}
}

func TestConfirmedImpl_FiresWhenImplFileEmpty(t *testing.T) {
	dir := t.TempDir()
	chdirTo(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "op.go"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &confirmedImplHook{}
	args := `{"id": "Op", "patch": "{\"status\":\"confirmed\",\"impl\":\"src/op.go\"}"}`
	v := h.After("graph_merge_object", args, "ok")
	if v == "" {
		t.Fatal("expected violation: empty file is not a valid impl")
	}
}

func TestConfirmedImpl_PassesWhenImplFileExists(t *testing.T) {
	dir := t.TempDir()
	chdirTo(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "op.go"), []byte("package main\nfunc Op(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &confirmedImplHook{}
	args := `{"id": "Op", "patch": "{\"status\":\"confirmed\",\"impl\":\"src/op.go\"}"}`
	if v := h.After("graph_merge_object", args, "ok"); v != "" {
		t.Errorf("should pass when impl file exists and is non-empty, got: %s", v)
	}
}

func TestConfirmedImpl_IgnoresPatchWithoutStatusChange(t *testing.T) {
	dir := t.TempDir()
	chdirTo(t, dir)
	h := &confirmedImplHook{}
	// Patch only sets intent, not status — hook should not fire.
	args := `{"id": "Op", "patch": "{\"intent\":\"updated\"}"}`
	if v := h.After("graph_merge_object", args, "ok"); v != "" {
		t.Errorf("hook should not fire on non-status patches, got: %s", v)
	}
}

func TestConfirmedImpl_IgnoresStatusOtherThanConfirmed(t *testing.T) {
	dir := t.TempDir()
	chdirTo(t, dir)
	h := &confirmedImplHook{}
	args := `{"id": "Op", "patch": "{\"status\":\"implementing\"}"}`
	if v := h.After("graph_merge_object", args, "ok"); v != "" {
		t.Errorf("hook should only fire on status=confirmed, got: %s", v)
	}
}

// --- def-impl-distinct hook ---

func TestDefImplDistinct_FiresWhenDefEqualsImpl(t *testing.T) {
	dir := t.TempDir()
	chdirTo(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "K"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Build a graph where Op has def == impl == "index.html"
	graphJSON := `{
  "attributes": {},
  "objects": {
    "Op": {
      "def": "index.html",
      "impl": "index.html",
      "consumes": [],
      "produces": [],
      "intent": "x",
      "temporal": null,
      "preconditions": "",
      "postconditions": "",
      "status": "confirmed",
      "statusSession": null
    }
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "K", "graph.json"), []byte(graphJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	h := &defImplDistinctHook{}
	v := h.After("graph_merge_object", `{"id":"Op","patch":"{\"impl\":\"index.html\"}"}`, "ok")
	if v == "" {
		t.Fatal("expected violation when def == impl")
	}
	if !strings.Contains(v, "Op") {
		t.Errorf("violation should mention object id: %s", v)
	}
}

func TestDefImplDistinct_PassesWhenDifferent(t *testing.T) {
	dir := t.TempDir()
	chdirTo(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "K"), 0o755); err != nil {
		t.Fatal(err)
	}
	graphJSON := `{
  "attributes": {},
  "objects": {
    "Op": {
      "def": "K/defs/Op.ts",
      "impl": "src/op.go",
      "consumes": [],
      "produces": [],
      "intent": "x",
      "temporal": null,
      "preconditions": "",
      "postconditions": "",
      "status": "confirmed",
      "statusSession": null
    }
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "K", "graph.json"), []byte(graphJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	h := &defImplDistinctHook{}
	if v := h.After("graph_merge_object", `{"id":"Op","patch":"{\"impl\":\"src/op.go\"}"}`, "ok"); v != "" {
		t.Errorf("should pass when def != impl: %s", v)
	}
}

func TestDefImplDistinct_IgnoresWhenImplNotSet(t *testing.T) {
	dir := t.TempDir()
	chdirTo(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "K"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Op exists but has no impl yet — comparison is meaningless
	graphJSON := `{
  "attributes": {},
  "objects": {
    "Op": {"def":"defs/Op.ts","impl":null,"consumes":[],"produces":[],"intent":"x","temporal":null,"preconditions":"","postconditions":"","status":"declared","statusSession":null}
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "K", "graph.json"), []byte(graphJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &defImplDistinctHook{}
	if v := h.After("graph_create_object", `{"id":"Op"}`, "ok"); v != "" {
		t.Errorf("should not fire when impl is null: %s", v)
	}
}

// --- def-uniqueness hook ---

func TestDefUniqueness_FiresWhenSharedDef(t *testing.T) {
	dir := t.TempDir()
	chdirTo(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "K"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Two attributes share the same def path — violation
	graphJSON := `{
  "attributes": {
    "a": {"def":"shared.ts","refines":[],"intent":"a","valueSpace":null,"confirmedOps":[],"laws":[],"status":"declared","statusSession":null},
    "b": {"def":"shared.ts","refines":[],"intent":"b","valueSpace":null,"confirmedOps":[],"laws":[],"status":"declared","statusSession":null}
  },
  "objects": {}
}`
	if err := os.WriteFile(filepath.Join(dir, "K", "graph.json"), []byte(graphJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &defUniquenessHook{}
	v := h.After("graph_create_attribute", `{"id":"b","def":"shared.ts"}`, "ok")
	if v == "" {
		t.Fatal("expected violation when two attributes share def")
	}
	if !strings.Contains(v, "shared.ts") {
		t.Errorf("violation should mention shared path: %s", v)
	}
}

func TestDefUniqueness_FiresAcrossKinds(t *testing.T) {
	// Attribute and object sharing the same def is also a violation.
	dir := t.TempDir()
	chdirTo(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "K"), 0o755); err != nil {
		t.Fatal(err)
	}
	graphJSON := `{
  "attributes": {
    "a": {"def":"index.html","refines":[],"intent":"a","valueSpace":null,"confirmedOps":[],"laws":[],"status":"declared","statusSession":null}
  },
  "objects": {
    "Op": {"def":"index.html","impl":null,"consumes":[],"produces":[],"intent":"x","temporal":null,"preconditions":"","postconditions":"","status":"declared","statusSession":null}
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "K", "graph.json"), []byte(graphJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &defUniquenessHook{}
	v := h.After("graph_create_object", `{"id":"Op"}`, "ok")
	if v == "" {
		t.Fatal("expected violation when attribute and object share def")
	}
}

func TestDefUniqueness_PassesWhenAllUnique(t *testing.T) {
	dir := t.TempDir()
	chdirTo(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "K"), 0o755); err != nil {
		t.Fatal(err)
	}
	graphJSON := `{
  "attributes": {
    "a": {"def":"defs/a.ts","refines":[],"intent":"a","valueSpace":null,"confirmedOps":[],"laws":[],"status":"declared","statusSession":null}
  },
  "objects": {
    "Op": {"def":"defs/Op.ts","impl":null,"consumes":[],"produces":[],"intent":"x","temporal":null,"preconditions":"","postconditions":"","status":"declared","statusSession":null}
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "K", "graph.json"), []byte(graphJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &defUniquenessHook{}
	if v := h.After("graph_create_object", `{"id":"Op"}`, "ok"); v != "" {
		t.Errorf("should pass when defs are unique: %s", v)
	}
}

// --- status-transition hook ---

func TestStatusTransition_FlagsSkipFromDeclared(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "K"), 0o755); err != nil {
		t.Fatal(err)
	}
	graphJSON := `{
  "attributes": {},
  "objects": {
    "F": {"def":"defs/F.ts","impl":null,"consumes":[],"produces":[],"intent":"x","temporal":null,"preconditions":"","postconditions":"","status":"declared","statusSession":null}
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "K", "graph.json"), []byte(graphJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	chdirTo(t, dir)

	// The merge tool itself rejects this transition, so the on-disk file
	// won't actually change to confirmed before this hook fires. Simulate
	// the situation by writing a graph where status is already "confirmed"
	// while the hook receives a patch attempting declared → confirmed.
	// In practice the hook runs after the tool succeeds, so what we want
	// to verify is that a patch that *would* skip is flagged.
	h := &statusTransitionHook{}
	args := `{"id":"F","patch":"{\"status\":\"confirmed\"}"}`
	out := h.After("graph_merge_object", args, "ok")
	if out == "" {
		t.Fatal("expected violation when status patch skips implementing")
	}
	if !strings.Contains(out, "implementing") {
		t.Fatalf("violation should mention implementing: %s", out)
	}
}

func TestStatusTransition_AllowsValidStep(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "K"), 0o755); err != nil {
		t.Fatal(err)
	}
	graphJSON := `{
  "attributes": {},
  "objects": {
    "F": {"def":"defs/F.ts","impl":null,"consumes":[],"produces":[],"intent":"x","temporal":null,"preconditions":"","postconditions":"","status":"implementing","statusSession":null}
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "K", "graph.json"), []byte(graphJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	chdirTo(t, dir)

	h := &statusTransitionHook{}
	args := `{"id":"F","patch":"{\"status\":\"confirmed\"}"}`
	if v := h.After("graph_merge_object", args, "ok"); v != "" {
		t.Errorf("implementing → confirmed should be allowed, got: %s", v)
	}
}

// --- FormatViolations ---

func TestFormatViolations_RendersAllItems(t *testing.T) {
	out := FormatViolations([]string{"first issue", "second issue"})
	if !strings.Contains(out, "first issue") || !strings.Contains(out, "second issue") {
		t.Errorf("FormatViolations should include all items: %s", out)
	}
	if !strings.Contains(out, "kcpos spec enforcement") {
		t.Errorf("FormatViolations should include the leading marker: %s", out)
	}
}

func TestFormatViolations_EmptyReturnsEmpty(t *testing.T) {
	if out := FormatViolations(nil); out != "" {
		t.Errorf("empty list should produce empty output, got: %s", out)
	}
}

func TestTypecalcUse_FlagsConfirmWithoutEvidence(t *testing.T) {
	chdirTo(t, t.TempDir())
	h := &typecalcUseHook{}
	args := `{"id":"InitGame","patch":"{\"status\":\"confirmed\",\"impl\":\"index.html\"}"}`
	out := h.After("graph_merge_object", args, "ok")
	if out == "" {
		t.Fatal("expected violation when confirming without typecalc evidence")
	}
	if !strings.Contains(out, "typecalc_compile") {
		t.Errorf("violation should suggest typecalc_compile: %s", out)
	}
	if !strings.Contains(out, "InitGame") {
		t.Errorf("violation should reference object id: %s", out)
	}
}

func TestTypecalcUse_PassesWhenEvidencePresent(t *testing.T) {
	dir := t.TempDir()
	evidenceDir := filepath.Join(dir, ".kcpos", "typecalc-evidence")
	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidenceDir, "InitGame.json"),
		[]byte(`{"objectId":"InitGame","kind":"compile","ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	chdirTo(t, dir)

	h := &typecalcUseHook{}
	args := `{"id":"InitGame","patch":"{\"status\":\"confirmed\",\"impl\":\"index.html\"}"}`
	if v := h.After("graph_merge_object", args, "ok"); v != "" {
		t.Errorf("evidence file present, should pass: %s", v)
	}
}

func TestTypecalcUse_IgnoresOtherStatuses(t *testing.T) {
	chdirTo(t, t.TempDir())
	h := &typecalcUseHook{}
	for _, status := range []string{"declared", "implementing"} {
		args := `{"id":"X","patch":"{\"status\":\"` + status + `\"}"}`
		if v := h.After("graph_merge_object", args, "ok"); v != "" {
			t.Errorf("status=%s should not require typecalc evidence: %s", status, v)
		}
	}
}

func TestTypecalcUse_IgnoresNonMergeTools(t *testing.T) {
	chdirTo(t, t.TempDir())
	h := &typecalcUseHook{}
	if v := h.After("graph_create_object", `{"id":"X"}`, "ok"); v != "" {
		t.Errorf("non-merge tool should be ignored: %s", v)
	}
}
