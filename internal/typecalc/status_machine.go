package typecalc

import (
	"fmt"

	"github.com/creator915/Koncept_OS/internal/graph"
)

// ValidStatusTransitions encodes §5.2 of the design doc:
//
//	declared → implementing → confirmed
//
// Skipping a step (declared → confirmed directly) is forbidden because it
// would mean a status was advanced without going through the
// implementation phase that produces (and thus types) the value. The
// inverse direction is also not allowed via Transition; rollback is the
// only legitimate way to demote status.
var ValidStatusTransitions = map[string][]string{
	graph.StatusDeclared:     {graph.StatusImplementing},
	graph.StatusImplementing: {graph.StatusConfirmed},
	graph.StatusConfirmed:    {}, // terminal; rollback (delete) is the only escape
}

// CheckStatusTransition reports whether moving from `from` to `to` is
// allowed. It returns nil on success and an error describing the
// violation otherwise.
func CheckStatusTransition(from, to string) error {
	if from == to {
		return nil // no-op is fine
	}
	allowed, ok := ValidStatusTransitions[from]
	if !ok {
		return fmt.Errorf("unknown source status %q", from)
	}
	for _, candidate := range allowed {
		if candidate == to {
			return nil
		}
	}
	return fmt.Errorf("illegal status transition %s → %s (must follow declared → implementing → confirmed)",
		from, to)
}

// ValidateMergeStatusChange is a precondition check for graph_merge_*
// tools. Given the existing entity status and the patched status, it
// returns nil if the change is permitted and an error otherwise.
//
// Empty `to` means the merge patch did not change the status field —
// always permitted.
func ValidateMergeStatusChange(from, to string) error {
	if to == "" {
		return nil
	}
	return CheckStatusTransition(from, to)
}
