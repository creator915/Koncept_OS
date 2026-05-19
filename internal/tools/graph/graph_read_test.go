package graphtools

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
)

// P1.1.3: graph-read family. The six ops are show / show-expanded /
// validate / validate-deep / dep-order / query-downstream. show &
// validate are graph_show / graph_validate (current-layer); the four
// layered ones are added here. Each op gets >=1 assertion, with
// cross-layer cases for the recursive three.

// layeredFixture builds a top graph with object A (consumes raw,
// produces mid, expands into s_child) and object B (consumes mid), plus
// a sub-hypergraph s_child containing object SubC (consumes raw).
func layeredFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	top := graph.NewGraph()
	top.Attributes["raw"] = graph.NewAttribute("defs/raw.ts", "raw input")
	top.Attributes["mid"] = graph.NewAttribute("defs/mid.ts", "intermediate")
	sid := "s_child"
	a := graph.NewObject("defs/A.ts", "A")
	a.Consumes = []string{"raw"}
	a.Produces = []string{"mid"}
	a.Expansion = &sid
	top.Objects["A"] = a
	b := graph.NewObject("defs/B.ts", "B")
	b.Consumes = []string{"mid"}
	top.Objects["B"] = b
	if err := persistence.SaveGraph(persistence.GraphDefaultPath, top); err != nil {
		t.Fatal(err)
	}

	sub := graph.NewGraph()
	sub.Attributes["raw"] = graph.NewAttribute("defs/raw.ts", "raw (refined in child)")
	sc := graph.NewObject("defs/SubC.ts", "SubC")
	sc.Consumes = []string{"raw"}
	sub.Objects["SubC"] = sc
	if err := persistence.SaveExpansionGraph(sid, sub); err != nil {
		t.Fatal(err)
	}
	return dir
}

func run(t *testing.T, tool toolcall.Tool, args map[string]interface{}) string {
	t.Helper()
	out, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatalf("tool error: %v", err)
	}
	return out
}

// op1 show (existing graph_show, current layer).
func TestGraphRead_Show(t *testing.T) {
	layeredFixture(t)
	out := run(t, graphShowTool(), map[string]interface{}{"id": "A"})
	if !strings.Contains(out, "object A") || !strings.Contains(out, "consumes: raw") {
		t.Fatalf("show must render node A: %s", out)
	}
}

// op2 show-expanded: top node + hyperlink + recursed sub-graph node.
func TestGraphRead_ShowExpanded_CrossLayer(t *testing.T) {
	layeredFixture(t)
	out := run(t, graphShowExpandedTool(), map[string]interface{}{"id": "A"})
	if !strings.Contains(out, "object A") {
		t.Errorf("must show A: %s", out)
	}
	if !strings.Contains(out, "↳ expansion: s_child") {
		t.Errorf("must surface the expansion hyperlink: %s", out)
	}
	if !strings.Contains(out, "object SubC") {
		t.Errorf("must recurse into sub-hypergraph and show SubC: %s", out)
	}
}

// op3 validate (existing graph_validate, current layer).
func TestGraphRead_Validate(t *testing.T) {
	layeredFixture(t)
	out := run(t, graphValidateTool(), map[string]interface{}{})
	if !strings.Contains(out, "validate") {
		t.Fatalf("validate must produce a report: %s", out)
	}
}

// op4 validate-deep: must validate BOTH top and the expansion layer.
func TestGraphRead_ValidateDeep_CrossLayer(t *testing.T) {
	layeredFixture(t)
	out := run(t, graphValidateDeepTool(), map[string]interface{}{})
	if !strings.Contains(out, "[top]") {
		t.Errorf("must validate top layer: %s", out)
	}
	if !strings.Contains(out, "[expansion s_child]") {
		t.Errorf("must recurse-validate the expansion layer: %s", out)
	}
}

// op5 dep-order: topological waves of the current layer. A produces mid
// which B consumes ⇒ A must order before B.
func TestGraphRead_DepOrder(t *testing.T) {
	layeredFixture(t)
	out := run(t, graphDepOrderTool(), map[string]interface{}{})
	if !strings.Contains(out, "preflight:") {
		t.Fatalf("dep-order must use preflight topo: %s", out)
	}
	if !strings.Contains(out, "A") || !strings.Contains(out, "B") {
		t.Fatalf("dep-order must list the layer's objects: %s", out)
	}
	if strings.Index(out, "A") > strings.Index(out, "B") {
		t.Errorf("A (produces mid) must topologically precede B (consumes mid): %s", out)
	}
}

// op6 query-downstream: raw → A (consumes raw, produces mid) → B
// (consumes mid); cross-layer descends into s_child where SubC also
// consumes raw.
func TestGraphRead_QueryDownstream_CrossLayer(t *testing.T) {
	layeredFixture(t)
	out := run(t, graphQueryDownstreamTool(), map[string]interface{}{"attribute": "raw"})
	if !strings.Contains(out, "downstream@top") {
		t.Errorf("must report top-layer downstream: %s", out)
	}
	if !strings.Contains(out, "A") || !strings.Contains(out, "B") {
		t.Errorf("raw must reach A then transitively B: %s", out)
	}
	if !strings.Contains(out, "downstream@s_child") || !strings.Contains(out, "SubC") {
		t.Errorf("must cross into the expansion layer and find SubC: %s", out)
	}
}

// Read ops must NOT mutate the graph on disk.
func TestGraphRead_IsReadOnly(t *testing.T) {
	dir := layeredFixture(t)
	info, _ := os.Stat(dir + "/K/graph.json")
	mt := info.ModTime()
	_ = run(t, graphShowExpandedTool(), map[string]interface{}{"id": "A"})
	_ = run(t, graphValidateDeepTool(), map[string]interface{}{})
	_ = run(t, graphDepOrderTool(), map[string]interface{}{})
	_ = run(t, graphQueryDownstreamTool(), map[string]interface{}{"attribute": "raw"})
	info2, _ := os.Stat(dir + "/K/graph.json")
	if !info2.ModTime().Equal(mt) {
		t.Error("read ops must not rewrite K/graph.json")
	}
}
