package persistence

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// focusFileName is the marker file storing the id of the currently focused
// session. Lives next to the sessions/ directory (e.g. K/.current-session
// when sessionDir is K/sessions). Plain text; one line; trailing newline OK.
const focusFileName = ".current-session"

// FocusFilePath returns the on-disk path of the focus marker.
func FocusFilePath(sessionDir string) string {
	return filepath.Join(filepath.Dir(sessionDir), focusFileName)
}

// GetFocus reads the currently focused session id, or "" if none.
func GetFocus(sessionDir string) (string, error) {
	data, err := os.ReadFile(FocusFilePath(sessionDir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// FindRoot walks the parent chain from id upward and returns the
// topmost ancestor (the one with Parent==""). Used by spawn_subagent
// to set the new session's parent to the project root regardless of
// what session is currently focused.
//
// v9.3: pre-v9.3 spawn_subagent used GetFocus() to pick a parent, which
// caused the "chain spawn" bug (v9.0.6 terraria-05, v92 03/04): when
// agent A spawned subagent B and later B spawned subagent C from the
// same root context, focus was still on B → C got parent=B not parent=root.
// Walking up via this helper always lands on root regardless of focus
// state.
//
// Returns ("", nil) when id is empty (caller has nothing to walk from).
// Returns (id, nil) when id IS already a root (parent==""). On load
// errors mid-walk (broken parent chain), returns the last valid ancestor.
func FindRoot(sessionDir, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	visited := map[string]bool{}
	cur := id
	for {
		if visited[cur] {
			// Cycle protection — shouldn't happen but defensive.
			return cur, nil
		}
		visited[cur] = true
		s, err := LoadSession(sessionDir, cur)
		if err != nil {
			// Best-effort: return the last valid id we had.
			return cur, nil
		}
		if s.Parent == "" {
			return cur, nil
		}
		cur = s.Parent
	}
}

// SetFocus writes the focus pointer. Pass id="" to clear it.
func SetFocus(sessionDir, id string) error {
	path := FocusFilePath(sessionDir)
	if id == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(id+"\n"), 0o644)
}

// ClearFocusIf clears focus if the current pointer matches id. Used when a
// focused session is deleted/finished so we don't leave a dangling pointer.
func ClearFocusIf(sessionDir, id string) error {
	cur, err := GetFocus(sessionDir)
	if err != nil {
		return err
	}
	if cur == id {
		return SetFocus(sessionDir, "")
	}
	return nil
}
