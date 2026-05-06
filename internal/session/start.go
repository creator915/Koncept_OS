package session

import "fmt"

// Start atomically creates a session, transitions it to active, and focuses
// it. Equivalent to Create + SetStatus(active) + SetFocus, but in a single
// step so the agent's natural workflow ("begin a new unit of work") cannot
// accidentally do graph mutations between Create and SetFocus — which would
// silently leave those mutations un-captured in any session's graphDiff.
//
// On error, attempts to roll back: if Start fails after creating the session
// but before focus is set, the partial session is deleted so the dir is
// consistent. Failures during focus-set are tolerated (session exists +
// active, just not focused — caller can retry SetFocus).
func Start(dir, id, parent, task string, input Input) (*Session, error) {
	s, err := Create(dir, id, parent, task, input)
	if err != nil {
		return nil, err
	}
	if _, err := SetStatus(dir, id, StatusActive); err != nil {
		// Rollback: undo the create.
		_, _ = DeleteRecursive(dir, id)
		return nil, fmt.Errorf("session_start: failed to activate %s: %w", id, err)
	}
	if err := SetFocus(dir, id); err != nil {
		// SetFocus failure is non-fatal: session is created and active.
		// Surface as a warning but return the session so caller can decide.
		return s, fmt.Errorf("session_start: created+activated %s but failed to focus: %w", id, err)
	}
	// Reload to get the up-to-date status.
	return Load(dir, id)
}
