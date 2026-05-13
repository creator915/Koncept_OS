package persistence

import (
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultDir is the conventional location for session JSONs, relative to the
// project root (cwd when kcpos runs).
const SessionDefaultDir = "K/sessions"

// Path returns the JSON file path for a session id under dir.
func SessionPath(dir, id string) string {
	return filepath.Join(dir, id+".json")
}

// Load reads a session JSON.
func LoadSession(dir, id string) (*session.Session, error) {
	data, err := os.ReadFile(SessionPath(dir, id))
	if err != nil {
		return nil, fmt.Errorf("load session %s: %w", id, err)
	}
	var s session.Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse session %s: %w", id, err)
	}
	// Defensive: re-init nil maps so downstream mutations don't panic.
	if s.Children == nil {
		s.Children = []string{}
	}
	if s.Input.Signatures == nil {
		s.Input.Signatures = []string{}
	}
	if s.Input.Context == nil {
		s.Input.Context = []string{}
	}
	if s.Output.Implementations == nil {
		s.Output.Implementations = []string{}
	}
	if s.Output.NewSignatures == nil {
		s.Output.NewSignatures = []string{}
	}
	if s.Output.NewAttributes == nil {
		s.Output.NewAttributes = []string{}
	}
	if s.Output.Tests == nil {
		s.Output.Tests = []string{}
	}
	return &s, nil
}

// Exists reports whether a session JSON exists for id under dir.
func ExistsSession(dir, id string) bool {
	_, err := os.Stat(SessionPath(dir, id))
	return err == nil
}

// Save writes the session to disk, creating parent dirs as needed.
func SaveSession(dir string, s *session.Session) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(SessionPath(dir, s.ID), data, 0o644)
}

// List returns the IDs of all sessions in dir, sorted alphabetically.
// Returns an empty slice (not error) if dir doesn't exist.
func ListSessions(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, ".json"))
	}
	sort.Strings(ids)
	return ids, nil
}

// Delete removes a single session JSON file. Caller is responsible for
// cleaning up children and updating the parent's children list.
func DeleteSession(dir, id string) error {
	if err := os.Remove(SessionPath(dir, id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
