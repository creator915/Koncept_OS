package graph

import (
	"strings"
	"testing"
)

func TestPreflight_Empty(t *testing.T) {
	g := NewGraph()
	r := g.Preflight(nil)
	if r.Status != "SAFE" || len(r.Waves) != 0 {
		t.Errorf("empty batch: %s", r.String())
	}
}

func TestPreflight_SingleObject(t *testing.T) {
	g := NewGraph()
	g.AddAttribute("x", NewAttribute("defs/x.ts", "x"))
	g.AddObject("Op", NewObject("defs/Op.ts", "op"))
	g.LinkProduce("Op", "x")
	r := g.Preflight([]string{"Op"})
	if r.Status != "SAFE" || len(r.Waves) != 1 || r.Waves[0][0] != "Op" {
		t.Errorf("single object should be 1 wave with 1 obj: %s", r.String())
	}
}

func TestPreflight_Chain(t *testing.T) {
	// A produces x, B consumes x produces y, C consumes y → 3 waves
	g := NewGraph()
	g.AddAttribute("x", NewAttribute("defs/x.ts", "x"))
	g.AddAttribute("y", NewAttribute("defs/y.ts", "y"))
	g.AddObject("A", NewObject("defs/A.ts", "A"))
	g.AddObject("B", NewObject("defs/B.ts", "B"))
	g.AddObject("C", NewObject("defs/C.ts", "C"))
	g.LinkProduce("A", "x")
	g.LinkConsume("B", "x")
	g.LinkProduce("B", "y")
	g.LinkConsume("C", "y")

	r := g.Preflight([]string{"A", "B", "C"})
	if r.Status != "SAFE" {
		t.Fatalf("chain should be SAFE: %s", r.String())
	}
	if len(r.Waves) != 3 {
		t.Errorf("chain should be 3 waves, got %d: %s", len(r.Waves), r.String())
	}
	if r.Waves[0][0] != "A" || r.Waves[1][0] != "B" || r.Waves[2][0] != "C" {
		t.Errorf("wrong wave order: %s", r.String())
	}
}

func TestPreflight_FanOut(t *testing.T) {
	// A produces x and y; B consumes x; C consumes y → wave 0 [A], wave 1 [B,C]
	g := NewGraph()
	g.AddAttribute("x", NewAttribute("defs/x.ts", "x"))
	g.AddAttribute("y", NewAttribute("defs/y.ts", "y"))
	g.AddObject("A", NewObject("defs/A.ts", "A"))
	g.AddObject("B", NewObject("defs/B.ts", "B"))
	g.AddObject("C", NewObject("defs/C.ts", "C"))
	g.LinkProduce("A", "x")
	g.LinkProduce("A", "y")
	g.LinkConsume("B", "x")
	g.LinkConsume("C", "y")

	r := g.Preflight([]string{"A", "B", "C"})
	if r.Status != "SAFE" || len(r.Waves) != 2 {
		t.Fatalf("fan-out should be SAFE 2 waves: %s", r.String())
	}
	if len(r.Waves[1]) != 2 {
		t.Errorf("wave 1 should have 2 parallelizable objects: %s", r.String())
	}
}

func TestPreflight_Cycle(t *testing.T) {
	// A produces x consumes y; B produces y consumes x → cycle
	g := NewGraph()
	g.AddAttribute("x", NewAttribute("defs/x.ts", "x"))
	g.AddAttribute("y", NewAttribute("defs/y.ts", "y"))
	g.AddObject("A", NewObject("defs/A.ts", "A"))
	g.AddObject("B", NewObject("defs/B.ts", "B"))
	g.LinkProduce("A", "x")
	g.LinkConsume("A", "y")
	g.LinkProduce("B", "y")
	g.LinkConsume("B", "x")

	r := g.Preflight([]string{"A", "B"})
	if r.Status != "UNSAFE" {
		t.Errorf("cycle should be UNSAFE: %s", r.String())
	}
	if len(r.Cycle) == 0 {
		t.Errorf("cycle path should be reported: %s", r.String())
	}
}

func TestPreflight_SubtypeSubstitution(t *testing.T) {
	// A produces temperature_celsius; B consumes temperature; celsius <: temperature
	// → B depends on A (subtype substitution).
	g := NewGraph()
	g.AddAttribute("temperature", NewAttribute("defs/t.ts", "general"))
	g.AddAttribute("temperature_celsius", NewAttribute("defs/tc.ts", "celsius"))
	g.LinkRefine("temperature_celsius", "temperature")
	g.AddObject("A", NewObject("defs/A.ts", "A"))
	g.AddObject("B", NewObject("defs/B.ts", "B"))
	g.LinkProduce("A", "temperature_celsius")
	g.LinkConsume("B", "temperature")

	r := g.Preflight([]string{"A", "B"})
	if r.Status != "SAFE" || len(r.Waves) != 2 {
		t.Errorf("subtype subst should yield 2 waves: %s", r.String())
	}
	if r.Waves[0][0] != "A" || r.Waves[1][0] != "B" {
		t.Errorf("A must precede B: %s", r.String())
	}
}

func TestPreflight_ValueDepWarning(t *testing.T) {
	// A and B both consume x in same wave (no dep between them) → value-dep warning.
	g := NewGraph()
	g.AddAttribute("x", NewAttribute("defs/x.ts", "x"))
	g.AddObject("A", NewObject("defs/A.ts", "A"))
	g.AddObject("B", NewObject("defs/B.ts", "B"))
	g.LinkConsume("A", "x")
	g.LinkConsume("B", "x")

	r := g.Preflight([]string{"A", "B"})
	if r.Status != "SAFE" {
		t.Errorf("status should still be SAFE (warning, not error): %s", r.String())
	}
	if len(r.Warnings) == 0 {
		t.Errorf("expected value-dep warning: %s", r.String())
	}
	if !strings.Contains(strings.Join(r.Warnings, "\n"), "x") {
		t.Errorf("warning should mention attribute 'x': %s", r.String())
	}
}

func TestPreflight_UnknownObject(t *testing.T) {
	g := NewGraph()
	r := g.Preflight([]string{"DoesNotExist"})
	if r.Status != "UNSAFE" || len(r.Unknown) != 1 {
		t.Errorf("unknown id should be UNSAFE: %s", r.String())
	}
}

func TestPreflight_DiamondPattern(t *testing.T) {
	// A produces x, y. B consumes x produces u. C consumes y produces v. D consumes u, v.
	// → wave 0 [A], wave 1 [B, C], wave 2 [D]
	g := NewGraph()
	for _, attr := range []string{"x", "y", "u", "v"} {
		g.AddAttribute(attr, NewAttribute("defs/"+attr+".ts", attr))
	}
	for _, obj := range []string{"A", "B", "C", "D"} {
		g.AddObject(obj, NewObject("defs/"+obj+".ts", obj))
	}
	g.LinkProduce("A", "x")
	g.LinkProduce("A", "y")
	g.LinkConsume("B", "x")
	g.LinkProduce("B", "u")
	g.LinkConsume("C", "y")
	g.LinkProduce("C", "v")
	g.LinkConsume("D", "u")
	g.LinkConsume("D", "v")

	r := g.Preflight([]string{"A", "B", "C", "D"})
	if r.Status != "SAFE" || len(r.Waves) != 3 {
		t.Fatalf("diamond should be SAFE 3 waves: %s", r.String())
	}
	if len(r.Waves[1]) != 2 {
		t.Errorf("wave 1 should have B and C parallel: %s", r.String())
	}
}
