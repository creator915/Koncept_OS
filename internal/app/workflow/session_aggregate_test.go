package workflow

import (
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
)

// Aggregate v2 (post-2026-05-07): Aggregate now derives implementations,
// newSignatures, newAttributes, tests from graphDiff + on-disk evidence
// instead of solely from output.X fields. These tests verify the
// derivation rules end-to-end so the agent never has to hand-patch
// session JSON to satisfy the gate.

func TestAggregate_DerivesImplFromAddedAndModifiedObjects(t *testing.T) {
	dir := chdirTempForAggregate(t)
	sessionDir := filepath.Join(dir, "K", "sessions")

	if _, err := Create(sessionDir, "s_root", "", "root", session.Input{}); err != nil {
		t.Fatal(err)
	}
	// Hand-craft a session whose graphDiff has BOTH an added object with
	// impl set, AND a modified object whose 'after' has impl set.
	s, err := persistence.LoadSession(sessionDir, "s_root")
	if err != nil {
		t.Fatal(err)
	}

	addedImpl := "src/Added.impl.go"
	addedObj := graph.NewObject("defs/Added.ts", "")
	addedObj.Impl = &addedImpl
	addedRaw, _ := json.Marshal(addedObj)
	s.Output.GraphDiff.Added.Objects["Added"] = addedRaw

	modImpl := "src/Modified.impl.go"
	beforeObj := graph.NewObject("defs/Modified.ts", "")
	beforeRaw, _ := json.Marshal(beforeObj)
	afterObj := graph.NewObject("defs/Modified.ts", "")
	afterObj.Impl = &modImpl
	afterRaw, _ := json.Marshal(afterObj)
	s.Output.GraphDiff.Modified.Objects["Modified"] = session.ModifiedRecord{
		Before: beforeRaw,
		After:  afterRaw,
	}
	if err := persistence.SaveSession(sessionDir, s); err != nil {
		t.Fatal(err)
	}

	got, err := Aggregate(sessionDir, "s_root")
	if err != nil {
		t.Fatal(err)
	}
	if !contains2(got.Output.Implementations, "src/Added.impl.go") {
		t.Errorf("missing added impl: %v", got.Output.Implementations)
	}
	if !contains2(got.Output.Implementations, "src/Modified.impl.go") {
		t.Errorf("missing modified impl: %v", got.Output.Implementations)
	}
	if !contains2(got.Output.NewSignatures, "Added") {
		t.Errorf("missing newSignatures: %v", got.Output.NewSignatures)
	}
}

func TestAggregate_DerivesNewAttributes(t *testing.T) {
	dir := chdirTempForAggregate(t)
	sessionDir := filepath.Join(dir, "K", "sessions")
	if _, err := Create(sessionDir, "s_root", "", "root", session.Input{}); err != nil {
		t.Fatal(err)
	}
	s, _ := persistence.LoadSession(sessionDir, "s_root")
	attrRaw, _ := json.Marshal(graph.NewAttribute("defs/foo.ts", ""))
	s.Output.GraphDiff.Added.Attributes["foo"] = attrRaw
	_ = persistence.SaveSession(sessionDir, s)

	got, _ := Aggregate(sessionDir, "s_root")
	if !contains2(got.Output.NewAttributes, "foo") {
		t.Errorf("missing newAttributes: %v", got.Output.NewAttributes)
	}
}

func TestAggregate_DerivesTestsFromEvidenceFiles(t *testing.T) {
	dir := chdirTempForAggregate(t)
	sessionDir := filepath.Join(dir, "K", "sessions")
	if _, err := Create(sessionDir, "s_root", "", "root", session.Input{}); err != nil {
		t.Fatal(err)
	}
	s, _ := persistence.LoadSession(sessionDir, "s_root")
	addedRaw, _ := json.Marshal(graph.NewObject("defs/Touched.ts", ""))
	s.Output.GraphDiff.Added.Objects["Touched"] = addedRaw
	_ = persistence.SaveSession(sessionDir, s)

	// Create evidence file for "Touched"; another stale one for an
	// unrelated id should NOT show up. v9.0: unified bundle path.
	evDir := filepath.Join(dir, ".kcpos", "typecalc")
	if err := os.MkdirAll(evDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Post-Fix-2: only kind=test evidence counts as a "test".
	touchedBundle := `{"objectId":"Touched","version":1,"updatedAt":"1970-01-01T00:00:00Z","test":{"kind":"test","lang":"Go","ok":true}}`
	if err := os.WriteFile(filepath.Join(evDir, "Touched.json"), []byte(touchedBundle), 0o644); err != nil {
		t.Fatal(err)
	}
	unrelatedBundle := `{"objectId":"Unrelated","version":1,"updatedAt":"1970-01-01T00:00:00Z","test":{"kind":"test","lang":"Go","ok":true}}`
	if err := os.WriteFile(filepath.Join(evDir, "Unrelated.json"), []byte(unrelatedBundle), 0o644); err != nil {
		t.Fatal(err)
	}

	got, _ := Aggregate(sessionDir, "s_root")
	want := filepath.Join(".kcpos", "typecalc", "Touched.json")
	if !contains2(got.Output.Tests, want) {
		t.Errorf("expected tests to include %s, got: %v", want, got.Output.Tests)
	}
	for _, e := range got.Output.Tests {
		if strings.Contains(e, "Unrelated") {
			t.Errorf("tests should not contain Unrelated session's evidence: %v", got.Output.Tests)
		}
	}
}

func TestAggregate_BackwardCompat_FallsBackToOutputFields(t *testing.T) {
	// If a session has nothing in graphDiff but has output.X populated
	// (e.g. legacy data, or the agent did a manual edit), aggregate
	// should still pick those up.
	dir := chdirTempForAggregate(t)
	sessionDir := filepath.Join(dir, "K", "sessions")
	if _, err := Create(sessionDir, "s_root", "", "root", session.Input{}); err != nil {
		t.Fatal(err)
	}
	s, _ := persistence.LoadSession(sessionDir, "s_root")
	s.Output.Implementations = []string{"legacy.impl.go"}
	s.Output.Tests = []string{"legacy.test.go"}
	_ = persistence.SaveSession(sessionDir, s)

	got, _ := Aggregate(sessionDir, "s_root")
	if !contains2(got.Output.Implementations, "legacy.impl.go") {
		t.Errorf("legacy impl dropped: %v", got.Output.Implementations)
	}
	if !contains2(got.Output.Tests, "legacy.test.go") {
		t.Errorf("legacy test dropped: %v", got.Output.Tests)
	}
}

func TestAggregate_WalksDescendantsAndDerives(t *testing.T) {
	dir := chdirTempForAggregate(t)
	sessionDir := filepath.Join(dir, "K", "sessions")

	if _, err := Create(sessionDir, "s_root", "", "root", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(sessionDir, "s_a", "s_root", "child a", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(sessionDir, "s_b", "s_root", "child b", session.Input{}); err != nil {
		t.Fatal(err)
	}

	// Each child added a different object via graphDiff.
	for _, info := range []struct{ sid, oid, impl string }{
		{"s_a", "FromA", "src/FromA.impl.go"},
		{"s_b", "FromB", "src/FromB.impl.go"},
	} {
		s, _ := persistence.LoadSession(sessionDir, info.sid)
		obj := graph.NewObject("defs/"+info.oid+".ts", "")
		impl := info.impl
		obj.Impl = &impl
		raw, _ := json.Marshal(obj)
		s.Output.GraphDiff.Added.Objects[info.oid] = raw
		_ = persistence.SaveSession(sessionDir, s)
	}

	got, err := Aggregate(sessionDir, "s_root")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"src/FromA.impl.go", "src/FromB.impl.go"} {
		if !contains2(got.Output.Implementations, want) {
			t.Errorf("missing %s in aggregated impls: %v", want, got.Output.Implementations)
		}
	}
	for _, want := range []string{"FromA", "FromB"} {
		if !contains2(got.Output.NewSignatures, want) {
			t.Errorf("missing %s in newSignatures: %v", want, got.Output.NewSignatures)
		}
	}
}

// --- helpers ---

func chdirTempForAggregate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := mkdirAll(filepath.Join(dir, "K", "sessions")); err != nil {
		t.Fatal(err)
	}
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	return dir
}

func contains2(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
