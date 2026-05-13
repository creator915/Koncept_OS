package core

import (
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
)

func TestCheckStatusTransition_Legal(t *testing.T) {
	cases := [][2]string{
		{graph.StatusDeclared, graph.StatusImplementing},
		{graph.StatusImplementing, graph.StatusConfirmed},
		{graph.StatusDeclared, graph.StatusDeclared}, // no-op
	}
	for _, c := range cases {
		if err := CheckStatusTransition(c[0], c[1]); err != nil {
			t.Errorf("%s → %s: %v", c[0], c[1], err)
		}
	}
}

func TestCheckStatusTransition_Illegal(t *testing.T) {
	cases := [][2]string{
		{graph.StatusDeclared, graph.StatusConfirmed},          // skips implementing
		{graph.StatusImplementing, graph.StatusDeclared},       // demotes
		{graph.StatusConfirmed, graph.StatusImplementing},      // demotes from terminal
		{graph.StatusConfirmed, graph.StatusDeclared},          // demotes from terminal
	}
	for _, c := range cases {
		if err := CheckStatusTransition(c[0], c[1]); err == nil {
			t.Errorf("%s → %s should be illegal", c[0], c[1])
		}
	}
}

func TestValidateMergeStatusChange_NoChange(t *testing.T) {
	if err := ValidateMergeStatusChange(graph.StatusDeclared, ""); err != nil {
		t.Fatalf("empty 'to' should be a no-op: %v", err)
	}
}
