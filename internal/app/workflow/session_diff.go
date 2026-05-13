package workflow

import (
	"time"

	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
)

// CaptureDiff compares before and after, computes the structural diff,
// and merges it into the focused session's graphDiff. No-op if no
// session is focused, focus points to a missing session, or the focused
// session is not in active state.
//
// v9.3.2 (B-strict refactor): this is the orchestrator that reads
// focus, loads the session, calls session.ApplyDiff (pure), and saves
// the session. The pure diff logic lives in domain/session/diff.go.
func CaptureDiff(sessionDir string, before, after *graph.Graph) error {
	focusID, err := persistence.GetFocus(sessionDir)
	if err != nil || focusID == "" {
		return nil
	}
	if !persistence.ExistsSession(sessionDir, focusID) {
		// stale focus pointer; clear it so future calls don't keep retrying
		_ = persistence.SetFocus(sessionDir, "")
		return nil
	}
	s, err := persistence.LoadSession(sessionDir, focusID)
	if err != nil {
		return nil
	}
	if s.Status != session.StatusActive {
		return nil
	}

	session.ApplyDiff(s, before, after)

	s.UpdatedAt = time.Now().UTC()
	return persistence.SaveSession(sessionDir, s)
}
