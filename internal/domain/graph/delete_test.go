package graph

import (
	"strings"
	"testing"
)

// TestDeleteObject_HappyPath — removing a declared object leaves the
// graph in a valid state (object gone; attributes untouched).
func TestDeleteObject_HappyPath(t *testing.T) {
	g := NewGraph()
	g.AddAttribute("x", NewAttribute("defs/x.ts", ""))
	g.AddObject("F", NewObject("defs/F.ts", ""))
	g.AddObject("G", NewObject("defs/G.ts", ""))

	if err := g.DeleteObject("F"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := g.Objects["F"]; ok {
		t.Errorf("F should be gone")
	}
	if _, ok := g.Objects["G"]; !ok {
		t.Errorf("G should still be present")
	}
	if _, ok := g.Attributes["x"]; !ok {
		t.Errorf("attribute x should remain (no cascade)")
	}
}

// TestDeleteObject_UnknownID — deleting a non-existent object yields
// a clear error rather than a silent no-op.
func TestDeleteObject_UnknownID(t *testing.T) {
	g := NewGraph()
	err := g.DeleteObject("Ghost")
	if err == nil {
		t.Fatal("expected error for unknown id, got nil")
	}
	if !strings.Contains(err.Error(), "not in graph") {
		t.Errorf("error should mention not in graph: %v", err)
	}
}

// TestDeleteObject_EmptyID — defensive: empty id is a programmer
// error, surface it loudly.
func TestDeleteObject_EmptyID(t *testing.T) {
	g := NewGraph()
	g.AddObject("F", NewObject("defs/F.ts", ""))
	if err := g.DeleteObject(""); err == nil {
		t.Fatal("expected error for empty id, got nil")
	}
	if _, ok := g.Objects["F"]; !ok {
		t.Errorf("empty-id delete should not affect existing objects")
	}
}

// TestDeleteObject_OrphanAttributes_LeftAlone — IO-boundary repair
// scenario: deleting an object that was the only consumer of an
// attribute leaves the attribute behind. Documented behavior: the
// repair caller can decide to clean orphan attributes separately;
// auto-cascade would risk taking out attributes still needed by
// objects added later in the same repair sub-agent loop.
func TestDeleteObject_OrphanAttributes_LeftAlone(t *testing.T) {
	g := NewGraph()
	g.AddAttribute("only_F_consumes", NewAttribute("defs/x.ts", ""))
	g.AddObject("F", NewObject("defs/F.ts", ""))
	g.LinkConsume("F", "only_F_consumes")

	if err := g.DeleteObject("F"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := g.Attributes["only_F_consumes"]; !ok {
		t.Errorf("orphan attribute should NOT be cascade-deleted (documented behavior)")
	}
}
