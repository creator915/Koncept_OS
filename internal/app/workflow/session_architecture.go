package workflow

import (
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"fmt"
	"time"
)

// SetArchitecture writes a free-form architecture description to a
// session's output.architecture field. Implements Fix 4 (architecture
// artifact). The root finish gate refuses sessions whose architecture
// is empty — see CheckGate in gate.go.
//
// The format is intentionally unspecified — anything that lists the
// sub-modules and intermediate variables works. Markdown is conventional.
func SetArchitecture(dir, id, description string) (*session.Session, error) {
	s, err := persistence.LoadSession(dir, id)
	if err != nil {
		return nil, err
	}
	s.Output.Architecture = description
	s.UpdatedAt = time.Now().UTC()
	if err := persistence.SaveSession(dir, s); err != nil {
		return nil, fmt.Errorf("save session %s: %w", id, err)
	}
	return s, nil
}
