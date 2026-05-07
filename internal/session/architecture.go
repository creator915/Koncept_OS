package session

import (
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
func SetArchitecture(dir, id, description string) (*Session, error) {
	s, err := Load(dir, id)
	if err != nil {
		return nil, err
	}
	s.Output.Architecture = description
	s.UpdatedAt = time.Now().UTC()
	if err := Save(dir, s); err != nil {
		return nil, fmt.Errorf("save session %s: %w", id, err)
	}
	return s, nil
}
