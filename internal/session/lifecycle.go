package session

import (
	"fmt"
	"time"
)

// Create writes a new session to disk. If parent is non-empty, that parent
// session must already exist; the new id is appended to its children list and
// the parent is re-saved.
func Create(dir, id, parent, task string, input Input) (*Session, error) {
	if Exists(dir, id) {
		return nil, fmt.Errorf("session %s already exists", id)
	}
	if parent != "" {
		if !Exists(dir, parent) {
			return nil, fmt.Errorf("parent session %s does not exist", parent)
		}
	}
	s := New(id, parent, task, input)
	if err := Save(dir, s); err != nil {
		return nil, err
	}
	if parent != "" {
		p, err := Load(dir, parent)
		if err != nil {
			// rollback the just-saved session to keep the directory consistent
			_ = Delete(dir, id)
			return nil, fmt.Errorf("attach to parent %s: %w", parent, err)
		}
		if !contains(p.Children, id) {
			p.Children = append(p.Children, id)
			p.UpdatedAt = time.Now().UTC()
			if err := Save(dir, p); err != nil {
				_ = Delete(dir, id)
				return nil, fmt.Errorf("update parent %s: %w", parent, err)
			}
		}
	}
	return s, nil
}

// SetStatus loads, transitions, and persists. Enforces the parent-child
// invariant from CLAUDE.md §5.4 path B step 10: a session may only finish
// when every child is either finished or already deleted.
func SetStatus(dir, id string, to Status) (*Session, error) {
	s, err := Load(dir, id)
	if err != nil {
		return nil, err
	}
	if to == StatusFinished {
		for _, childID := range s.Children {
			if !Exists(dir, childID) {
				// deleted children count as resolved (per §5.3 deletion semantics)
				continue
			}
			child, err := Load(dir, childID)
			if err != nil {
				return nil, fmt.Errorf("check child %s of %s: %w", childID, id, err)
			}
			if child.Status != StatusFinished {
				return nil, fmt.Errorf("cannot finish %s: child %s is %s (must be finished or deleted first)", id, childID, child.Status)
			}
		}
	}
	if err := s.Transition(to); err != nil {
		return nil, err
	}
	if err := Save(dir, s); err != nil {
		return nil, err
	}
	// If this was the focused session and it just finished, clear focus —
	// graph mutations after this point should not be attributed to it.
	if to == StatusFinished {
		if err := ClearFocusIf(dir, id); err != nil {
			return s, fmt.Errorf("clear focus after finishing %s: %w", id, err)
		}
	}
	return s, nil
}

// DeleteRecursive removes id and all its descendants depth-first, then
// detaches id from its parent's children list. Returns the list of ids
// deleted (in deletion order) for caller logging. Idempotent: a missing id
// returns nil deletions and no error.
func DeleteRecursive(dir, id string) ([]string, error) {
	if !Exists(dir, id) {
		return nil, nil
	}
	s, err := Load(dir, id)
	if err != nil {
		return nil, err
	}
	var deleted []string
	for _, child := range s.Children {
		sub, err := DeleteRecursive(dir, child)
		if err != nil {
			return nil, err
		}
		deleted = append(deleted, sub...)
	}
	if err := Delete(dir, id); err != nil {
		return nil, err
	}
	deleted = append(deleted, id)
	if s.Parent != "" {
		// Detach from parent's children list, if parent still exists.
		if Exists(dir, s.Parent) {
			p, err := Load(dir, s.Parent)
			if err != nil {
				return deleted, fmt.Errorf("detach from parent %s: %w", s.Parent, err)
			}
			p.Children = removeFromSlice(p.Children, id)
			p.UpdatedAt = time.Now().UTC()
			if err := Save(dir, p); err != nil {
				return deleted, fmt.Errorf("update parent %s: %w", s.Parent, err)
			}
		}
	}
	return deleted, nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func removeFromSlice(xs []string, x string) []string {
	out := make([]string, 0, len(xs))
	for _, v := range xs {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}
