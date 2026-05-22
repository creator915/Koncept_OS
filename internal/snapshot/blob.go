// Package snapshot — kcpos event-sourced snapshot system.
//
// Design (per 2026-05-21 spec):
//
//   - Events form an append-only, sha-chained log: every state-mutating
//     agent action is captured as one event, indexed by sha256(parent_sha
//     || canonical(payload)). Read-only actions (probe / read_file) are
//     recorded as bare metadata; their outputs come back from the log on
//     replay, no re-invocation.
//
//   - File contents (sources, bundle JSON sections, compiled binaries)
//     live in a content-addressed blob store, referenced from events
//     by sha. Deduplicates aggressively — across batch #8 the entire
//     5-instance run touches ~50-200 MB of unique blob content.
//
//   - Named refs point at events (milestone:graph-declared,
//     attempt:run-3, ...). Like git refs — mutable pointers, immutable
//     events.
//
//   - Replay re-applies side_effects in order, NEVER re-invokes the LLM
//     or external probes. Output of those is whatever the original
//     run captured. Determinism is in the side-effect replay, not the
//     causation.
//
//   - Rollback is "set workdir to event N's state + archive
//     events N+1..tip into a branch + synthesize a lesson". The lesson
//     becomes injectable context for the next attempt.
//
// This file: content-addressed blob storage.
package snapshot

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// BlobStore is a content-addressed object store. Layout matches git's
// loose object format: <root>/objects/<first-2-chars-of-sha>/<rest>.
// That layout caps directory fan-out at 256 and keeps inode pressure
// reasonable even with 100k+ blobs.
//
// Integrity: Get verifies the on-disk content's sha matches the
// requested id by default. The chain's tamper-evident property only
// holds end-to-end if blob contents can't drift silently — a flipped
// bit on a source-file blob would otherwise replay into a different
// workdir than the original run. Set VerifyOnGet=false to skip the
// check (e.g. for hot paths reading already-validated content; the
// guard is otherwise cheap — sha256 of a few KB is microseconds).
type BlobStore struct {
	root         string // e.g. ".kcpos/snapshots/objects"
	VerifyOnGet  bool   // default true; mismatch → ErrBlobCorrupt
}

// NewBlobStore returns a store rooted at the given directory. The
// directory is created on first Put; NewBlobStore itself is a no-op
// constructor (so it can be safely used to read from a not-yet-existing
// store — Get on missing blob returns ErrBlobNotFound, not a path
// error).
func NewBlobStore(root string) *BlobStore {
	return &BlobStore{root: root, VerifyOnGet: true}
}

// Put writes content into the store and returns its sha256 hex digest.
// Idempotent: if the blob already exists, Put is a no-op and returns
// the existing sha. Uses atomic-write semantics (temp file + rename)
// so a crash mid-Put never leaves a partial blob visible.
func (s *BlobStore) Put(content []byte) (string, error) {
	sha := blobSha(content)
	target := s.path(sha)
	if _, err := os.Stat(target); err == nil {
		return sha, nil // already stored — dedup hit
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("blobstore: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".tmp-*")
	if err != nil {
		return "", fmt.Errorf("blobstore: temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("blobstore: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("blobstore: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("blobstore: close: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("blobstore: rename: %w", err)
	}
	return sha, nil
}

// Get returns the content for sha. Returns ErrBlobNotFound when the
// blob is absent (distinct from any other I/O error so callers can
// distinguish "we never stored this" from "disk failed"). Returns
// ErrBlobCorrupt when VerifyOnGet is set (default) and the on-disk
// content's recomputed sha doesn't match the requested id — that's
// disk corruption, tampering, or a bug somewhere upstream.
func (s *BlobStore) Get(sha string) ([]byte, error) {
	if !validSha(sha) {
		return nil, fmt.Errorf("blobstore: invalid sha %q", sha)
	}
	f, err := os.Open(s.path(sha))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrBlobNotFound
		}
		return nil, fmt.Errorf("blobstore: open: %w", err)
	}
	defer f.Close()
	body, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("blobstore: read: %w", err)
	}
	if s.VerifyOnGet {
		if got := blobSha(body); got != sha {
			return nil, fmt.Errorf("%w: requested %s, on-disk content hashes to %s", ErrBlobCorrupt, sha[:16], got[:16])
		}
	}
	return body, nil
}

// Exists reports whether sha is in the store. Cheaper than Get when
// the caller only needs presence (e.g. dedup pre-check during a
// large bulk operation).
func (s *BlobStore) Exists(sha string) bool {
	if !validSha(sha) {
		return false
	}
	_, err := os.Stat(s.path(sha))
	return err == nil
}

// ErrBlobNotFound is returned by Get when the requested sha is not
// in the store.
var ErrBlobNotFound = fmt.Errorf("blob not found")

// ErrBlobCorrupt is returned by Get (with VerifyOnGet) when the
// on-disk content's recomputed sha doesn't match the requested id.
// Indicates external tampering, disk corruption, or a writer bug —
// callers should NOT silently fall through to the corrupt content.
var ErrBlobCorrupt = fmt.Errorf("blob corrupt: content hash does not match path")

// path resolves a sha to its on-disk location. <root>/<2>/<remaining>.
// We never expose this externally — callers should use Put/Get/Exists
// so the layout can evolve (e.g. packed objects later) without
// breaking out-of-tree code.
func (s *BlobStore) path(sha string) string {
	return filepath.Join(s.root, sha[:2], sha[2:])
}

// blobSha is the canonical sha256(content) hex digest. Kept identical
// to typecalc/core.HashSource so a blob's sha matches the source-hash
// of the file it represents — avoids needing a translation table when
// matching graph-state sourceHash fields against blob ids.
func blobSha(content []byte) string {
	h := sha256.Sum256(content)
	return fmt.Sprintf("%x", h)
}

// validSha rejects obviously-malformed sha strings before they reach
// the filesystem (defends against path traversal: a sha like
// "..\\evil" must not become a directory traversal).
func validSha(sha string) bool {
	if len(sha) != 64 {
		return false
	}
	for _, r := range sha {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
