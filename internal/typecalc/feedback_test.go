package typecalc

import (
	"encoding/json"
	"testing"

	"github.com/creator915/Koncept_OS/internal/graph"
)

func TestApplyValueAdjust_UpdatesValueSpace(t *testing.T) {
	g := graph.NewGraph()
	g.Attributes["height"] = graph.NewAttribute("defs/height.ts", "player jump height")

	d := &ValueAdjustDetail{AttrPath: "height"}
	d.NewValue, _ = json.Marshal(map[string]any{"meters": 2.5})
	affected, err := ApplyValueAdjust(g, d)
	if err != nil {
		t.Fatal(err)
	}
	if g.Attributes["height"].ValueSpace["meters"] != 2.5 {
		t.Fatalf("valueSpace not updated: %+v", g.Attributes["height"].ValueSpace)
	}
	if affected == nil {
		t.Fatal("affected nil")
	}
}

func TestApplyLawMissing_AppendsLaw(t *testing.T) {
	g := graph.NewGraph()
	g.Attributes["v"] = graph.NewAttribute("defs/v.ts", "velocity")

	d := &LawMissingDetail{AttrPath: "v", NewLaw: "magnitude(v) >= 0"}
	if _, err := ApplyLawMissing(g, d); err != nil {
		t.Fatal(err)
	}
	if len(g.Attributes["v"].Laws) != 1 {
		t.Fatalf("laws = %v", g.Attributes["v"].Laws)
	}
	// Idempotent re-apply.
	if _, err := ApplyLawMissing(g, d); err != nil {
		t.Fatal(err)
	}
	if len(g.Attributes["v"].Laws) != 1 {
		t.Fatalf("re-apply duplicated: %v", g.Attributes["v"].Laws)
	}
}

func TestApplyValueAdjust_CollectsDownstream(t *testing.T) {
	g := graph.NewGraph()
	g.Attributes["x"] = graph.NewAttribute("d/x.ts", "")
	g.Attributes["y"] = graph.NewAttribute("d/y.ts", "")

	o := graph.NewObject("d/F.ts", "x → y")
	o.Consumes = []string{"x"}
	o.Produces = []string{"y"}
	g.Objects["F"] = o

	d := &ValueAdjustDetail{AttrPath: "x"}
	d.NewValue, _ = json.Marshal(map[string]any{"v": 1})
	affected, _ := ApplyValueAdjust(g, d)
	if len(affected.Objects) != 1 || affected.Objects[0] != "F" {
		t.Fatalf("downstream = %v", affected.Objects)
	}
}

func TestSplitAttrPath(t *testing.T) {
	root, sub, err := SplitAttrPath("player_state.position.y")
	if err != nil {
		t.Fatal(err)
	}
	if root != "player_state" {
		t.Fatalf("root = %s", root)
	}
	if len(sub) != 2 || sub[0] != "position" || sub[1] != "y" {
		t.Fatalf("sub = %v", sub)
	}
}
