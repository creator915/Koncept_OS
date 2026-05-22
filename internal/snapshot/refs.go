package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RefStore holds named pointers into the event log — like git refs.
// Refs are MUTABLE (Set overwrites), events are immutable. Common ref
// patterns:
//
//	tip                          — current tip of the linear log
//	milestone/<phase>-<instance> — auto-set on Outer.* transitions
//	attempt/<n>-<instance>       — set when rollback archives a branch
//	pinned/<name>                — manually pinned (never auto-pruned)
//
// Storage: <root>/<name>.txt holding the event id (single line).
// Names may contain "/" which becomes a subdirectory — that lets the
// "milestone/" and "pinned/" namespaces live as actual directories.
type RefStore struct {
	root string // e.g. ".kcpos/snapshots/refs"
}

func NewRefStore(root string) *RefStore {
	return &RefStore{root: root}
}

// ErrRefNotFound distinguishes missing-ref from I/O errors.
var ErrRefNotFound = fmt.Errorf("ref not found")

// Set creates or overwrites the ref. eventID must be a valid sha — we
// don't (here) verify that the event actually exists in the log; that
// keeps RefStore decoupled from EventLog. Caller can validate when it
// matters (e.g. before Get-and-replay).
func (r *RefStore) Set(name, eventID string) error {
	if err := validRefName(name); err != nil {
		return err
	}
	if !validSha(eventID) {
		return fmt.Errorf("refstore: invalid event id %q", eventID)
	}
	target := r.path(name)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("refstore: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".tmp-*")
	if err != nil {
		return fmt.Errorf("refstore: temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(eventID + "\n"); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("refstore: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("refstore: rename: %w", err)
	}
	return nil
}

// Get returns the event id the ref points to, or ErrRefNotFound.
func (r *RefStore) Get(name string) (string, error) {
	if err := validRefName(name); err != nil {
		return "", err
	}
	data, err := os.ReadFile(r.path(name))
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrRefNotFound
		}
		return "", fmt.Errorf("refstore: read: %w", err)
	}
	id := strings.TrimSpace(string(data))
	if !validSha(id) {
		return "", fmt.Errorf("refstore: ref %q points to invalid id %q", name, id)
	}
	return id, nil
}

// Delete removes the ref. Idempotent — deleting a missing ref is not
// an error (caller usually doesn't care).
func (r *RefStore) Delete(name string) error {
	if err := validRefName(name); err != nil {
		return err
	}
	err := os.Remove(r.path(name))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("refstore: remove: %w", err)
	}
	return nil
}

// List returns all refs as a map from full name to event id. Walks
// the entire refs/ tree — refs are expected to be ≤ 100s for any
// realistic project, so a single pass is fine.
func (r *RefStore) List() (map[string]string, error) {
	out := map[string]string{}
	if _, err := os.Stat(r.root); os.IsNotExist(err) {
		return out, nil
	}
	err := filepath.Walk(r.root, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(r.root, p)
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(filepath.ToSlash(rel), ".txt")
		id, err := r.Get(name)
		if err != nil {
			return err
		}
		out[name] = id
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// path resolves a ref name to its on-disk file. Adds the .txt
// extension so a future "refs" hierarchy can interleave directories
// (e.g. milestone/) with files (e.g. tip.txt) cleanly.
func (r *RefStore) path(name string) string {
	return filepath.Join(r.root, name+".txt")
}

// validRefName rejects names that would escape the refs directory
// (".." traversal) or that conflict with our .txt suffix convention.
func validRefName(name string) error {
	if name == "" {
		return fmt.Errorf("refstore: name required")
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("refstore: name contains '..' which would traverse outside refs/: %q", name)
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") {
		return fmt.Errorf("refstore: name must be relative: %q", name)
	}
	if strings.HasSuffix(name, ".txt") {
		return fmt.Errorf("refstore: name must NOT include the .txt suffix; provide the logical name only")
	}
	return nil
}
