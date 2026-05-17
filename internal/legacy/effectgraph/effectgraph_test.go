package effectgraph

import "testing"

func TestGraph_TypedEdgesStayDistinct(t *testing.T) {
	g := New("b0")
	g.AddNode(&Node{ID: "transfer", CodeScope: "func transfer"})
	g.AddNode(&Node{ID: "audit", CodeScope: "func audit"})
	g.AddNode(&Node{ID: "ledger", CodeScope: "global ledger"})
	if err := g.AddEdge(Edge{From: "transfer", To: "audit", Kind: EdgeDataFlow, Channel: "return"}); err != nil {
		t.Fatal(err)
	}
	if err := g.AddEdge(Edge{From: "transfer", To: "ledger", Kind: EdgeState, Channel: "ledger"}); err != nil {
		t.Fatal(err)
	}
	if err := g.AddEdge(Edge{From: "audit", To: "transfer", Kind: EdgeContract}); err != nil {
		t.Fatal(err)
	}
	df := g.DataFlowEdges()
	if len(df) != 1 || df[0].To != "audit" {
		t.Fatalf("DataFlowEdges must isolate only the DataFlow kind, got %+v", df)
	}
	if c := g.Callers("transfer"); len(c) != 1 || c[0] != "audit" {
		t.Fatalf("Callers(transfer) backward step wrong: %v", c)
	}
}

func TestGraph_AddEdgeRejectsUnknownNodesAndEnvKind(t *testing.T) {
	g := New("b0")
	g.AddNode(&Node{ID: "a"})
	if err := g.AddEdge(Edge{From: "a", To: "ghost", Kind: EdgeDataFlow}); err == nil {
		t.Fatal("edge to unknown node must error")
	}
	g.AddNode(&Node{ID: "b"})
	if err := g.AddEdge(Edge{From: "a", To: "b", Kind: EdgeEnvironmental}); err == nil {
		t.Fatal("Environmental edges must only come from UpgradeContextDim")
	}
}

// 原则 E: a dimension rides as a tag until SUSPECTED, and only then can
// it be UPGRADED to a node — never without prior suspicion.
func TestGraph_ContextDimUpgradeRequiresPriorSuspicion(t *testing.T) {
	g := New("b0")
	g.AddNode(&Node{ID: "parse", CodeScope: "func parseDate"})

	// Default: no env nodes, no suspicion — graph stays uncontaminated.
	if len(g.Nodes) != 1 {
		t.Fatalf("env must NOT be a node by default, nodes=%v", g.Nodes)
	}
	if _, err := g.UpgradeContextDim("parse", DimLocale); err == nil {
		t.Fatal("upgrade without prior suspicion must be refused (原则 E discipline)")
	}

	// Suspect, then upgrade.
	if err := g.SuspectContextDim("parse", DimLocale); err != nil {
		t.Fatal(err)
	}
	envNode, err := g.UpgradeContextDim("parse", DimLocale)
	if err != nil {
		t.Fatalf("upgrade after suspicion must succeed: %v", err)
	}
	if envNode.Environmental != DimLocale {
		t.Fatalf("upgraded node must be tagged Environmental=%s, got %q", DimLocale, envNode.Environmental)
	}
	// An Environmental edge now links the env node into the code node.
	found := false
	for _, e := range g.Edges {
		if e.Kind == EdgeEnvironmental && e.To == "parse" && e.Channel == string(DimLocale) {
			found = true
		}
	}
	if !found {
		t.Fatalf("upgrade must add an Environmental edge env→parse, edges=%+v", g.Edges)
	}
	// Suspicion consumed (promoted, no longer a mere candidate).
	if g.ContextTags["parse"].Suspected[DimLocale] {
		t.Fatal("upgraded dimension must no longer be a pending suspicion")
	}
	// Second upgrade refused again (suspicion was consumed).
	if _, err := g.UpgradeContextDim("parse", DimLocale); err == nil {
		t.Fatal("re-upgrade without re-suspicion must be refused")
	}
}
