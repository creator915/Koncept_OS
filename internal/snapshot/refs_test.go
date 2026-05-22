package snapshot

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefStore_SetGetRoundTrip(t *testing.T) {
	r := NewRefStore(filepath.Join(t.TempDir(), "refs"))
	id := strings.Repeat("a", 64)
	if err := r.Set("tip", id); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := r.Get("tip")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != id {
		t.Errorf("round-trip: got %q want %q", got, id)
	}
}

// Nested name with "/" → subdirectory layout.
func TestRefStore_NestedNames(t *testing.T) {
	r := NewRefStore(filepath.Join(t.TempDir(), "refs"))
	id := strings.Repeat("b", 64)
	if err := r.Set("milestone/graph-declared", id); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Get("milestone/graph-declared")
	if got != id {
		t.Errorf("nested ref roundtrip failed: got %q want %q", got, id)
	}
}

// Set overwrites (refs ARE mutable — events are immutable, refs point
// at them). This regression-guards future code that mistakenly turns
// Set into create-only.
func TestRefStore_SetOverwrites(t *testing.T) {
	r := NewRefStore(filepath.Join(t.TempDir(), "refs"))
	a := strings.Repeat("a", 64)
	b := strings.Repeat("b", 64)
	_ = r.Set("tip", a)
	_ = r.Set("tip", b)
	got, _ := r.Get("tip")
	if got != b {
		t.Errorf("Set must overwrite: got %q want %q", got, b)
	}
}

func TestRefStore_GetMissing(t *testing.T) {
	r := NewRefStore(filepath.Join(t.TempDir(), "refs"))
	_, err := r.Get("nope")
	if !errors.Is(err, ErrRefNotFound) {
		t.Errorf("expected ErrRefNotFound, got %v", err)
	}
}

func TestRefStore_Delete(t *testing.T) {
	r := NewRefStore(filepath.Join(t.TempDir(), "refs"))
	id := strings.Repeat("c", 64)
	_ = r.Set("temp", id)
	if err := r.Delete("temp"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get("temp"); !errors.Is(err, ErrRefNotFound) {
		t.Errorf("after Delete, Get must return ErrRefNotFound, got %v", err)
	}
	// Idempotent.
	if err := r.Delete("temp"); err != nil {
		t.Errorf("Delete on missing must be idempotent (no error), got %v", err)
	}
}

func TestRefStore_RejectsTraversal(t *testing.T) {
	r := NewRefStore(filepath.Join(t.TempDir(), "refs"))
	for _, bad := range []string{
		"",
		"../escape",
		"a/../../b",
		"/abs",
		"foo.txt", // we add .txt ourselves
	} {
		if err := r.Set(bad, strings.Repeat("a", 64)); err == nil {
			t.Errorf("Set(%q) must be rejected", bad)
		}
	}
}

func TestRefStore_List(t *testing.T) {
	r := NewRefStore(filepath.Join(t.TempDir(), "refs"))
	a := strings.Repeat("a", 64)
	b := strings.Repeat("b", 64)
	c := strings.Repeat("c", 64)
	_ = r.Set("tip", a)
	_ = r.Set("milestone/x", b)
	_ = r.Set("attempt/3", c)
	all, err := r.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 refs, got %d: %v", len(all), all)
	}
	if all["tip"] != a || all["milestone/x"] != b || all["attempt/3"] != c {
		t.Errorf("ref list values wrong: %v", all)
	}
}
