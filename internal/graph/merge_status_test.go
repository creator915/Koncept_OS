package graph

import (
	"strings"
	"testing"
)

func TestMergeObject_RejectsStatusSkip(t *testing.T) {
	g := NewGraph()
	g.Objects["F"] = NewObject("defs/F.ts", "")
	if err := g.MergeObject("F", map[string]any{"status": StatusConfirmed}); err == nil {
		t.Fatal("declared → confirmed should be rejected")
	} else if !strings.Contains(err.Error(), "implementing") {
		t.Fatalf("error should mention implementing: %v", err)
	}
}

func TestMergeObject_AllowsStatusChain(t *testing.T) {
	g := NewGraph()
	g.Objects["F"] = NewObject("defs/F.ts", "")
	if err := g.MergeObject("F", map[string]any{"status": StatusImplementing}); err != nil {
		t.Fatalf("declared → implementing: %v", err)
	}
	if err := g.MergeObject("F", map[string]any{"status": StatusConfirmed}); err != nil {
		t.Fatalf("implementing → confirmed: %v", err)
	}
	// Cannot demote a confirmed entity.
	if err := g.MergeObject("F", map[string]any{"status": StatusImplementing}); err == nil {
		t.Fatal("confirmed → implementing should be rejected")
	}
}

func TestMergeAttribute_RejectsStatusSkip(t *testing.T) {
	g := NewGraph()
	g.Attributes["a"] = NewAttribute("defs/a.ts", "")
	if err := g.MergeAttribute("a", map[string]any{"status": StatusConfirmed}); err == nil {
		t.Fatal("declared → confirmed should be rejected")
	}
}
