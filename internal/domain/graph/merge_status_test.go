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

// v9.0.1 A — implSymbol must be in the merge allowlist so the agent
// can set it on a graph object after creation. Pre-v9.0.1 the field
// existed on the struct but the merge tool rejected the patch field.
func TestMergeObject_AcceptsImplSymbol(t *testing.T) {
	g := NewGraph()
	g.Objects["UpdatePhysics"] = NewObject("defs/UpdatePhysics.ts", "")
	if err := g.MergeObject("UpdatePhysics", map[string]any{"implSymbol": "updatePhysics"}); err != nil {
		t.Fatalf("implSymbol patch should be accepted: %v", err)
	}
	if g.Objects["UpdatePhysics"].ImplSymbol != "updatePhysics" {
		t.Errorf("ImplSymbol not applied: %q", g.Objects["UpdatePhysics"].ImplSymbol)
	}
	// nil clears the symbol back to default-(object-id).
	if err := g.MergeObject("UpdatePhysics", map[string]any{"implSymbol": nil}); err != nil {
		t.Fatalf("implSymbol=nil should clear: %v", err)
	}
	if g.Objects["UpdatePhysics"].ImplSymbol != "" {
		t.Errorf("ImplSymbol not cleared: %q", g.Objects["UpdatePhysics"].ImplSymbol)
	}
	// Type errors are rejected.
	if err := g.MergeObject("UpdatePhysics", map[string]any{"implSymbol": 42}); err == nil {
		t.Error("non-string implSymbol should be rejected")
	}
}

// v9.0.3 A — implFragment is the per-object writing target for
// single-file deliverables. Field must be in the allowlist and apply
// like other string-or-nil patches.
func TestMergeObject_AcceptsImplFragment(t *testing.T) {
	g := NewGraph()
	g.Objects["WorldGen"] = NewObject("defs/WorldGen.ts", "")
	if err := g.MergeObject("WorldGen", map[string]any{"implFragment": "K/frags/WorldGen.js"}); err != nil {
		t.Fatalf("implFragment patch should be accepted: %v", err)
	}
	if g.Objects["WorldGen"].ImplFragment == nil || *g.Objects["WorldGen"].ImplFragment != "K/frags/WorldGen.js" {
		t.Errorf("ImplFragment not applied: %v", g.Objects["WorldGen"].ImplFragment)
	}
	// nil clears.
	if err := g.MergeObject("WorldGen", map[string]any{"implFragment": nil}); err != nil {
		t.Fatalf("implFragment=nil should clear: %v", err)
	}
	if g.Objects["WorldGen"].ImplFragment != nil {
		t.Error("ImplFragment not cleared by nil")
	}
}

func TestMergeAttribute_RejectsStatusSkip(t *testing.T) {
	g := NewGraph()
	g.Attributes["a"] = NewAttribute("defs/a.ts", "")
	if err := g.MergeAttribute("a", map[string]any{"status": StatusConfirmed}); err == nil {
		t.Fatal("declared → confirmed should be rejected")
	}
}
