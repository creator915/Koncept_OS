package session

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
