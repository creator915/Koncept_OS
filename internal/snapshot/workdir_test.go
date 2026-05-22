package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

// Empty workdir → empty snapshot, no error. Defensive: a fresh
// agent run starts with no files in the watched roots, taking a
// snapshot pre-first-tool-call must succeed.
func TestWorkdirSnapshot_EmptyRoot(t *testing.T) {
	snap, err := TakeWorkdirSnapshot(t.TempDir())
	if err != nil {
		t.Fatalf("empty workdir: %v", err)
	}
	if len(snap.Files) != 0 {
		t.Errorf("empty workdir must yield empty Files, got %v", snap.Files)
	}
}

// Missing workdir → empty snapshot, no error. Same defensive shape
// as empty: never propagate "the directory does not exist" if we
// can treat it as a vacuous snapshot.
func TestWorkdirSnapshot_MissingRoot(t *testing.T) {
	snap, err := TakeWorkdirSnapshot("/no/such/path/" + t.Name())
	if err != nil {
		t.Fatalf("missing workdir must be vacuous, got %v", err)
	}
	if len(snap.Files) != 0 {
		t.Errorf("missing workdir must have empty Files, got %v", snap.Files)
	}
}

// Coverage policy spot-check: source files at top level get hashed,
// LICENSE/README at top level do NOT (they're upstream fixtures).
func TestWorkdirSnapshot_TopLevelCoverage(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []struct{ name, content string }{
		{"main.c", "int main(){return 0;}\n"},          // hashed
		{"compile.sh", "#!/bin/sh\ngcc main.c\n"},      // hashed
		{"SPEC.md", "# Rebuild this\n"},                // hashed
		{"LICENSE", "Apache 2.0\n"},                    // NOT hashed
		{"README", "rebuild instructions\n"},           // NOT hashed
		{"run.log", "log content\n"},                   // NOT hashed
		{"trace.json", "{}\n"},                         // NOT hashed
		{"probe", "#!/bin/sh\ndocker exec ...\n"},      // NOT hashed (harness)
	} {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snap, _ := TakeWorkdirSnapshot(dir)
	want := map[string]bool{"main.c": true, "compile.sh": true, "SPEC.md": true}
	got := map[string]bool{}
	for path := range snap.Files {
		got[path] = true
	}
	for p := range want {
		if !got[p] {
			t.Errorf("expected %q in snapshot, got %v", p, got)
		}
	}
	for p := range got {
		if !want[p] {
			t.Errorf("unexpected %q in snapshot (should be filtered out): got %v", p, got)
		}
	}
}

// Recursion into K/ and .kcpos/typecalc/ — both core state surfaces.
func TestWorkdirSnapshot_RecursesIntoK(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "K"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "K", "sessions"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, ".kcpos", "typecalc"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "K", "graph.json"), []byte(`{}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "K", "sessions", "s_root.json"), []byte(`{"id":"root"}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".kcpos", "typecalc", "Foo.json"), []byte(`{"objectId":"Foo"}`), 0o644)

	snap, _ := TakeWorkdirSnapshot(dir)
	for _, want := range []string{"K/graph.json", "K/sessions/s_root.json", ".kcpos/typecalc/Foo.json"} {
		if _, ok := snap.Files[want]; !ok {
			t.Errorf("expected %q hashed, snap has: %v", want, snap.Files)
		}
	}
}

// Recursion guard: .kcpos/snapshots/* must NEVER be hashed (would
// recurse into the snapshot store as it grows, eventually OOM).
func TestWorkdirSnapshot_SkipsSnapshotsSubtree(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".kcpos", "snapshots", "objects", "ab"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".kcpos", "snapshots", "objects", "ab", "cdef1234"), []byte("blob"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, ".kcpos", "snapshots", "events"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".kcpos", "snapshots", "events", "abc.json"), []byte(`{"id":"abc"}`), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, ".kcpos", "typecalc"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".kcpos", "typecalc", "F.json"), []byte(`{}`), 0o644)

	snap, _ := TakeWorkdirSnapshot(dir)
	for path := range snap.Files {
		if filepath.ToSlash(path) == ".kcpos/typecalc/F.json" {
			continue
		}
		t.Errorf("recursion guard failed: %q should not be in snapshot (only typecalc/F.json should be)", path)
	}
}

// Diff: same snapshot vs itself produces empty diff. Identity check —
// re-snapshotting after a read-only tool call yields no side_effects.
func TestWorkdirDiff_Identity(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "main.c"), []byte("body"), 0o644)
	a, _ := TakeWorkdirSnapshot(dir)
	b, _ := TakeWorkdirSnapshot(dir)
	d := a.Diff(b)
	if !d.IsEmpty() {
		t.Errorf("identity diff must be empty, got %+v", d)
	}
}

// Diff: added file shows up in Added bucket, hash recorded.
func TestWorkdirDiff_DetectsAdded(t *testing.T) {
	dir := t.TempDir()
	pre, _ := TakeWorkdirSnapshot(dir)
	_ = os.WriteFile(filepath.Join(dir, "main.c"), []byte("body"), 0o644)
	post, _ := TakeWorkdirSnapshot(dir)
	d := pre.Diff(post)
	if len(d.Added) != 1 || d.Added[0] != "main.c" {
		t.Errorf("expected [main.c] added, got %v", d.Added)
	}
	if d.HashPost["main.c"] == "" {
		t.Errorf("HashPost must carry the added file's sha for blob lookup")
	}
}

// Diff: modified file → Modified bucket.
func TestWorkdirDiff_DetectsModified(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "main.c"), []byte("first"), 0o644)
	pre, _ := TakeWorkdirSnapshot(dir)
	_ = os.WriteFile(filepath.Join(dir, "main.c"), []byte("second"), 0o644)
	post, _ := TakeWorkdirSnapshot(dir)
	d := pre.Diff(post)
	if len(d.Modified) != 1 || d.Modified[0] != "main.c" {
		t.Errorf("expected [main.c] modified, got %v", d.Modified)
	}
}

// Diff: deleted file → Deleted bucket. Hash not stored (file is gone).
func TestWorkdirDiff_DetectsDeleted(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "main.c"), []byte("body"), 0o644)
	pre, _ := TakeWorkdirSnapshot(dir)
	_ = os.Remove(filepath.Join(dir, "main.c"))
	post, _ := TakeWorkdirSnapshot(dir)
	d := pre.Diff(post)
	if len(d.Deleted) != 1 || d.Deleted[0] != "main.c" {
		t.Errorf("expected [main.c] deleted, got %v", d.Deleted)
	}
}

// Diff sort property: when multiple files change in one call, the
// resulting slice MUST be stable (sorted), otherwise the event sha
// becomes non-deterministic.
func TestWorkdirDiff_DeterministicOrder(t *testing.T) {
	dir := t.TempDir()
	pre, _ := TakeWorkdirSnapshot(dir)
	for _, name := range []string{"zeta.c", "alpha.c", "mu.c", "beta.c"} {
		_ = os.WriteFile(filepath.Join(dir, name), []byte("body"), 0o644)
	}
	post, _ := TakeWorkdirSnapshot(dir)
	d := pre.Diff(post)
	want := []string{"alpha.c", "beta.c", "mu.c", "zeta.c"}
	if len(d.Added) != len(want) {
		t.Fatalf("expected %d added, got %d", len(want), len(d.Added))
	}
	for i := range want {
		if d.Added[i] != want[i] {
			t.Errorf("Added[%d] = %q, want %q (deterministic order is load-bearing for sha chain)", i, d.Added[i], want[i])
		}
	}
}
