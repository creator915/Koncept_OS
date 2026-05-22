package snapshot

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Round-trip: Put → Get → identical bytes back.
func TestBlobStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewBlobStore(filepath.Join(dir, "objects"))
	content := []byte("hello world\n")

	sha, err := s.Put(content)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if len(sha) != 64 {
		t.Errorf("sha must be 64-char hex, got %q", sha)
	}

	got, err := s.Get(sha)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("round-trip content mismatch: got %q want %q", got, content)
	}
}

// Dedup: same content → same sha → second Put is a no-op (proves
// idempotency, not just hash equality).
func TestBlobStore_DedupSameContent(t *testing.T) {
	dir := t.TempDir()
	s := NewBlobStore(filepath.Join(dir, "objects"))
	content := []byte("dedupe me")

	sha1, _ := s.Put(content)
	// Capture mtime of the first store.
	stat1, _ := os.Stat(filepath.Join(dir, "objects", sha1[:2], sha1[2:]))
	mtime1 := stat1.ModTime()

	sha2, _ := s.Put(content)
	if sha1 != sha2 {
		t.Errorf("same content must yield same sha: %q vs %q", sha1, sha2)
	}
	stat2, _ := os.Stat(filepath.Join(dir, "objects", sha1[:2], sha1[2:]))
	// Second Put should NOT rewrite the file (no-op on existing blob).
	if !stat2.ModTime().Equal(mtime1) {
		t.Errorf("second Put rewrote existing blob — should be no-op")
	}
}

// Different content → different sha (cryptographic property; this
// guards against accidentally hashing only a prefix or some such).
func TestBlobStore_DifferentContent(t *testing.T) {
	dir := t.TempDir()
	s := NewBlobStore(filepath.Join(dir, "objects"))
	sha1, _ := s.Put([]byte("alpha"))
	sha2, _ := s.Put([]byte("beta"))
	if sha1 == sha2 {
		t.Fatal("different content must yield different sha")
	}
}

// Get on missing sha returns ErrBlobNotFound (NOT some random path
// error — callers want to branch on "we never stored this").
func TestBlobStore_GetMissing(t *testing.T) {
	dir := t.TempDir()
	s := NewBlobStore(filepath.Join(dir, "objects"))
	missingSha := strings.Repeat("a", 64) // valid shape, doesn't exist
	_, err := s.Get(missingSha)
	if !errors.Is(err, ErrBlobNotFound) {
		t.Errorf("expected ErrBlobNotFound, got %v", err)
	}
}

// Path-traversal-shaped shas must be rejected without touching the
// filesystem. ".." or non-hex chars must error at validation, not at
// open.
func TestBlobStore_RejectsInvalidSha(t *testing.T) {
	dir := t.TempDir()
	s := NewBlobStore(filepath.Join(dir, "objects"))
	for _, bad := range []string{
		"",
		"too-short",
		"../etc/passwd",
		strings.Repeat("g", 64),       // valid length, invalid hex
		strings.Repeat("a", 64) + "x", // too long
	} {
		if _, err := s.Get(bad); err == nil || errors.Is(err, ErrBlobNotFound) {
			t.Errorf("Get(%q): expected validation error, got %v", bad, err)
		}
	}
}

// Exists is consistent with Get: present blobs report true, absent
// false, malformed shas false (not an error — Exists is best-effort
// presence check).
func TestBlobStore_Exists(t *testing.T) {
	dir := t.TempDir()
	s := NewBlobStore(filepath.Join(dir, "objects"))
	sha, _ := s.Put([]byte("hi"))
	if !s.Exists(sha) {
		t.Error("Exists must report true for stored blob")
	}
	if s.Exists(strings.Repeat("0", 64)) {
		t.Error("Exists must report false for unknown sha")
	}
	if s.Exists("not-a-sha") {
		t.Error("Exists must report false (not panic) for malformed sha")
	}
}
