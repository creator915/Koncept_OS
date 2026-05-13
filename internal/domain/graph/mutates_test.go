package graph

import (
	"strings"
	"testing"
)

func TestLinkMutate_AddsAndIsIdempotent(t *testing.T) {
	g := NewGraph()
	g.AddAttribute("x", NewAttribute("defs/x.ts", ""))
	g.AddObject("F", NewObject("defs/F.ts", ""))

	if err := g.LinkMutate("F", "x"); err != nil {
		t.Fatal(err)
	}
	if err := g.LinkMutate("F", "x"); err != nil {
		t.Fatalf("idempotent re-link should succeed: %v", err)
	}
	if got := g.Objects["F"].Mutates; len(got) != 1 || got[0] != "x" {
		t.Fatalf("Mutates = %v, want [x]", got)
	}
}

func TestLinkMutate_RejectsUnknownAttribute(t *testing.T) {
	g := NewGraph()
	g.AddObject("F", NewObject("defs/F.ts", ""))
	if err := g.LinkMutate("F", "ghost"); err == nil {
		t.Fatal("should reject unknown attribute")
	}
}

func TestUnlinkMutate(t *testing.T) {
	g := NewGraph()
	g.AddAttribute("x", NewAttribute("defs/x.ts", ""))
	g.AddObject("F", NewObject("defs/F.ts", ""))
	_ = g.LinkMutate("F", "x")
	if err := g.UnlinkMutate("F", "x"); err != nil {
		t.Fatal(err)
	}
	if len(g.Objects["F"].Mutates) != 0 {
		t.Fatalf("Mutates should be empty after unlink: %v", g.Objects["F"].Mutates)
	}
}

func TestValidate_FlagsUnknownMutates(t *testing.T) {
	g := NewGraph()
	g.AddObject("F", NewObject("defs/F.ts", ""))
	g.Objects["F"].Mutates = []string{"ghost"}
	r := g.Validate("")
	found := false
	for _, issue := range r.Issues {
		if issue.Severity == Error && strings.Contains(issue.Message, "mutates unknown") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected reference-integrity error for mutates ghost, got: %v", r.Issues)
	}
}

func TestPreflight_IgnoresMutatesEdges(t *testing.T) {
	// Two objects that mutate the SAME attribute should NOT form a cycle.
	g := NewGraph()
	g.AddAttribute("state", NewAttribute("defs/state.ts", ""))
	g.AddObject("A", NewObject("defs/A.ts", ""))
	g.AddObject("B", NewObject("defs/B.ts", ""))
	// Both mutate state. A reads state via consume to know about it.
	_ = g.LinkConsume("A", "state")
	_ = g.LinkMutate("A", "state")
	_ = g.LinkConsume("B", "state")
	_ = g.LinkMutate("B", "state")

	report := g.Preflight([]string{"A", "B"})
	if report.Status != "SAFE" {
		t.Fatalf("preflight should be SAFE (mutates ignored), got %s · cycle=%v", report.Status, report.Cycle)
	}
	if len(report.Cycle) != 0 {
		t.Fatalf("no cycle expected, got: %v", report.Cycle)
	}
}
