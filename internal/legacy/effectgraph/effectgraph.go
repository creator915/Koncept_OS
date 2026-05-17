// Package effectgraph implements the code-impact graph from 屎山代码
// 维护Agent设计文档 v1.0 Part 2.6, plus 原则 E's environment-as-tag /
// upgrade-to-node discipline. It is the substrate two designed-but-
// unbuilt pieces need next: EPA fault injection along DataFlow edges
// (Part 4.1) and Feathers effect reasoning (Part 6.7 — forward/backward
// over the three propagation channels).
//
// The three edge kinds are kept DISTINCT on purpose (设计文档 Part 2.6
// "三类 Edge 的区分意义"): DataFlow isolates via typing, Contract needs
// the promise lifted to a type/invariant, State needs a shared-state
// inventory. Collapsing them would lose the action-space distinction.
//
// 原则 E: environment dims (os/compiler/locale/deps) are NOT graph
// nodes by default — they ride as ContextTagSet metadata. Only when a
// dimension is SUSPECTED does it get upgraded to a real node with an
// Environmental edge to the implicated code (避免污染：环境维度极多).
package effectgraph

import "fmt"

// EdgeKind — 设计文档 Part 2.6.
type EdgeKind string

const (
	// EdgeDataFlow — return value / parameter / field. Interface
	// partially isolable; primarily eliminated by typing.
	EdgeDataFlow EdgeKind = "DataFlow"
	// EdgeContract — implicit promise. Interface barely isolates; lift
	// the contract to a type or explicit invariant.
	EdgeContract EdgeKind = "Contract"
	// EdgeState — shared mutable state. Interface does not isolate at
	// all; needs a shared-state inventory + invariant constraints.
	EdgeState EdgeKind = "State"
	// EdgeEnvironmental — an UPGRADED context dimension (原则 E). Only
	// exists after a dimension was suspected and promoted to a node.
	EdgeEnvironmental EdgeKind = "Environmental"
)

// ContextDim — an environment dimension that can be upgraded (原则 E).
type ContextDim string

const (
	DimOS       ContextDim = "os"
	DimCompiler ContextDim = "compiler"
	DimLocale   ContextDim = "locale"
	DimDeps     ContextDim = "deps"
	DimTimezone ContextDim = "timezone"
)

// ContextTagSet is per-node environment metadata. All fields optional
// (默认 None). `Suspected` tracks dimensions flagged as upgrade
// candidates but not yet promoted.
type ContextTagSet struct {
	OS        string
	Compiler  string
	Locale    string
	Deps      map[string]string
	Timezone  string
	Suspected map[ContextDim]bool
}

// Node is a code scope. UnderwrittenBy/RequiresAssumptions wire it to
// the Oracle/Assumption world (设计文档 Part 2.6 GraphNode).
type Node struct {
	ID                 string
	CodeScope          string
	UnderwrittenBy     []string // Oracle ids guaranteeing this node
	RequiresAssumptions []string // Assumption ids it depends on
	// Environmental marks a node that exists only because a context
	// dimension was upgraded (原则 E). Normal code nodes leave it "".
	Environmental ContextDim
}

// Edge — 设计文档 Part 2.6 GraphEdge. DerivationAssumptions records the
// assumptions this edge's existence rests on (refuting one can drop the
// edge).
type Edge struct {
	From                 string
	To                   string
	Kind                 EdgeKind
	Channel              string   // DataChannel / SharedResource detail
	DerivationAssumptions []string
}

// Graph is the per-branch effect graph (设计文档 Part 2.6 EffectGraph).
type Graph struct {
	Branch      string
	Nodes       map[string]*Node
	Edges       []Edge
	ContextTags map[string]*ContextTagSet // nodeID → tags
}

func New(branch string) *Graph {
	return &Graph{
		Branch:      branch,
		Nodes:       map[string]*Node{},
		ContextTags: map[string]*ContextTagSet{},
	}
}

// AddNode inserts (or replaces) a code node.
func (g *Graph) AddNode(n *Node) {
	g.Nodes[n.ID] = n
	if g.ContextTags[n.ID] == nil {
		g.ContextTags[n.ID] = &ContextTagSet{Suspected: map[ContextDim]bool{}}
	}
}

// AddEdge connects two existing nodes with a typed effect edge.
func (g *Graph) AddEdge(e Edge) error {
	if _, ok := g.Nodes[e.From]; !ok {
		return fmt.Errorf("effectgraph: edge from unknown node %q", e.From)
	}
	if _, ok := g.Nodes[e.To]; !ok {
		return fmt.Errorf("effectgraph: edge to unknown node %q", e.To)
	}
	if e.Kind == EdgeEnvironmental {
		return fmt.Errorf("effectgraph: Environmental edges are created only via UpgradeContextDim, not AddEdge")
	}
	g.Edges = append(g.Edges, e)
	return nil
}

// SuspectContextDim flags a dimension on a node as an upgrade candidate
// WITHOUT yet creating a node (原则 E: 默认 tag，不污染图).
func (g *Graph) SuspectContextDim(nodeID string, dim ContextDim) error {
	t := g.ContextTags[nodeID]
	if t == nil {
		return fmt.Errorf("effectgraph: SuspectContextDim on unknown node %q", nodeID)
	}
	if t.Suspected == nil {
		t.Suspected = map[ContextDim]bool{}
	}
	t.Suspected[dim] = true
	return nil
}

// UpgradeContextDim promotes a SUSPECTED dimension into a real node +
// an Environmental edge to the implicated code node (原则 E: 只有在怀疑
// 该维度引起问题时才升级为节点). Refuses to upgrade a dimension that was
// never suspected — escalation discipline (no unfounded env nodes).
func (g *Graph) UpgradeContextDim(nodeID string, dim ContextDim) (*Node, error) {
	t := g.ContextTags[nodeID]
	if t == nil {
		return nil, fmt.Errorf("effectgraph: UpgradeContextDim on unknown node %q", nodeID)
	}
	if !t.Suspected[dim] {
		return nil, fmt.Errorf("effectgraph: dimension %q was never suspected on %q — upgrade requires prior suspicion (原则 E discipline)", dim, nodeID)
	}
	envID := "env:" + string(dim) + "->" + nodeID
	envNode := &Node{ID: envID, CodeScope: "environment:" + string(dim), Environmental: dim}
	g.AddNode(envNode)
	g.Edges = append(g.Edges, Edge{From: envID, To: nodeID, Kind: EdgeEnvironmental, Channel: string(dim)})
	delete(t.Suspected, dim) // promoted; no longer a mere candidate
	return envNode, nil
}

// DataFlowEdges returns the DataFlow edges — the set EPA (Part 4.1)
// walks to inject perturbations along (M_src → M_tgt).
func (g *Graph) DataFlowEdges() []Edge {
	var out []Edge
	for _, e := range g.Edges {
		if e.Kind == EdgeDataFlow {
			out = append(out, e)
		}
	}
	return out
}

// Callers returns node ids with an edge INTO target — the backward
// step of Feathers effect reasoning (Part 6.7 "if has return value,
// check all callers").
func (g *Graph) Callers(target string) []string {
	var out []string
	for _, e := range g.Edges {
		if e.To == target {
			out = append(out, e.From)
		}
	}
	return out
}
