package services

import (
	"os"
	"path/filepath"
	"testing"
)

// ② regression guards for the Characterization equivalence oracle.

// reconstructionMode must key strictly on ./probe presence (the
// blackbox harness writes it; SPEC runs do not).
func TestReconstructionMode_ProbePresence(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	if reconstructionMode() {
		t.Fatal("no ./probe ⇒ NOT reconstruction mode (SPEC path must stay unchanged)")
	}
	if err := os.WriteFile(filepath.Join(dir, "probe"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !reconstructionMode() {
		t.Fatal("./probe present ⇒ reconstruction mode")
	}
}

// The battery must be non-trivial, deterministic, and gate-generated
// (no model input). Determinism is the 命门: a reproducible,
// non-model-controlled stimulus set.
func TestGenerateBattery_DeterministicAndNonTrivial(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	a := generateBattery()
	b := generateBattery()
	if len(a) < 10 {
		t.Fatalf("battery too small to be a real oracle: %d", len(a))
	}
	if len(a) != len(b) {
		t.Fatalf("battery not deterministic: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].name != b[i].name || a[i].stdin != b[i].stdin || len(a[i].argv) != len(b[i].argv) {
			t.Fatalf("battery item %d not stable: %+v vs %+v", i, a[i], b[i])
		}
	}
	// Docs-derived flags must be lifted from the task's OWN docs, not
	// invented: a README with -x must yield a flag-x item.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("usage: tool [-x] [-q]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withDocs := generateBattery()
	if len(withDocs) <= len(a) {
		t.Fatal("docs-declared flags must expand the battery deterministically")
	}
	found := false
	for _, it := range withDocs {
		if it.name == "flag-x" {
			found = true
		}
	}
	if !found {
		t.Fatal("a flag declared in the task's own README must appear in the battery")
	}
}

// Absent any recorded characterization, the confirm chokepoint must NOT
// see equivalence evidence (fail-closed).
func TestHasEquivEvidence_AbsentIsFalse(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if hasEquivEvidence("NoSuchObject") {
		t.Fatal("no characterization recorded ⇒ hasEquivEvidence must be false (fail-closed)")
	}
}
