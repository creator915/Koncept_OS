// Package effectreason implements Feathers effect reasoning over the
// machine EffectGraph (屎山代码维护Agent设计文档 v1.0 Part 6.7). It is
// the bridge between the graph (Part 2.6) and the two analysis engines:
// FORWARD reasoning ("做了 X 会怎样" — pairs with EPA) and BACKWARD
// reasoning ("出现 Y 是什么造成" — pairs with SBFL/Bayesian).
//
// The three propagation channels are kept distinct (Part 6.7 / 2.6):
// return value (data), passed-object mutation (data/contract), global/
// static (state). The 6-step heuristic (Part 6.7) is mechanized over
// the graph rather than walked by eye.
//
// "But that would be stupid" rules (Part 6.7 / 6.16): a project-level
// invariant that lets the sketch PRUNE a path ("in this project a list
// is never externally mutated"). Pruning is RECORDED, not silent, and
// each rule is tied to an Assumption — if that assumption is later
// refuted, Violated() flags it so the pruned paths are reinstated and
// the decision downgraded (设计文档 "每条规则有违反触发降级机制").
package effectreason

import "github.com/creator915/Koncept_OS/internal/legacy/effectgraph"

// Channel — the propagation channel a node was reached through.
type Channel string

const (
	ChannelReturn      Channel = "return"         // data: return value
	ChannelParamMutate Channel = "param-mutation" // data/contract: modify passed object
	ChannelSharedState Channel = "shared-state"   // state: global / static
	ChannelContract    Channel = "contract"       // implicit promise
	ChannelEnvironment Channel = "environment"    // upgraded context dim (原则 E)
)

func channelOf(e effectgraph.Edge) Channel {
	switch e.Kind {
	case effectgraph.EdgeState:
		return ChannelSharedState
	case effectgraph.EdgeContract:
		return ChannelContract
	case effectgraph.EdgeEnvironmental:
		return ChannelEnvironment
	default: // DataFlow
		if e.Channel == "param" || e.Channel == "arg" || e.Channel == "args" {
			return ChannelParamMutate
		}
		return ChannelReturn
	}
}

// Impact is one reached node and how it was reached. PrunedBy != ""
// means a "but that would be stupid" rule cut the path here (recorded
// for audit, not dropped).
type Impact struct {
	Node     string
	Via      Channel
	Distance int // hops from origin — proximity for Pinch/priority (Part 6.8)
	PrunedBy string
}

// StupidRule — a project invariant that prunes a sketch path
// (设计文档 Part 6.7 "But that would be stupid"). AssumptionID ties it
// to the assumption world so Violated() can trigger the downgrade.
type StupidRule struct {
	ID           string
	Statement    string
	PrunesVia    Channel
	AssumptionID string
}

// ForwardSketch BFS-walks effects OUT from origin (the change point):
// "if I change origin, what is reached?" — Feathers Effect Sketch.
// A StupidRule whose PrunesVia matches an edge's channel cuts traversal
// PAST that node: the node is still recorded (PrunedBy set) but its
// downstream is not explored. Pairs with EPA (forward).
func ForwardSketch(g *effectgraph.Graph, origin string, rules []StupidRule) []Impact {
	return sketch(g, origin, rules, true)
}

// BackwardSketch BFS-walks CAUSES into problem: "Y happened — what
// could have caused it?" Pairs with SBFL/Bayesian (backward). Stupid
// rules do NOT prune the backward search — when hunting a real failure
// you must not trust the "that would be stupid" assumption (it may be
// exactly what was violated).
func BackwardSketch(g *effectgraph.Graph, problem string) []Impact {
	return sketch(g, problem, nil, false)
}

func sketch(g *effectgraph.Graph, start string, rules []StupidRule, forward bool) []Impact {
	if _, ok := g.Nodes[start]; !ok {
		return nil
	}
	pruneByChannel := map[Channel]string{}
	for _, r := range rules {
		pruneByChannel[r.PrunesVia] = r.ID
	}
	type qitem struct {
		node string
		dist int
	}
	seen := map[string]bool{start: true}
	queue := []qitem{{start, 0}}
	var out []Impact
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range g.Edges {
			var next string
			if forward && e.From == cur.node {
				next = e.To
			} else if !forward && e.To == cur.node {
				next = e.From
			} else {
				continue
			}
			if seen[next] {
				continue
			}
			seen[next] = true
			ch := channelOf(e)
			imp := Impact{Node: next, Via: ch, Distance: cur.dist + 1}
			if rid, pruned := pruneByChannel[ch]; pruned {
				imp.PrunedBy = rid
				out = append(out, imp)
				continue // do NOT explore past a pruned path
			}
			out = append(out, imp)
			queue = append(queue, qitem{next, cur.dist + 1})
		}
	}
	return out
}

// SixStepInspection mechanizes the Part 6.7 6-step effect heuristic
// for a change node. Returns the inspection set keyed by step. Steps
// needing type-hierarchy / def-use that the graph doesn't model yet
// are returned empty (honest — the scanner adapter fills them later),
// not faked.
func SixStepInspection(g *effectgraph.Graph, changeNode string) map[int][]string {
	res := map[int][]string{1: {changeNode}}
	// Step 2 — if it has a return value, check all callers.
	res[2] = g.Callers(changeNode)
	// Step 3 — if it modifies values, who uses those values (DataFlow
	// out via param-mutation / return).
	for _, e := range g.Edges {
		if e.From == changeNode && e.Kind == effectgraph.EdgeDataFlow {
			res[3] = append(res[3], e.To)
		}
	}
	// Steps 4,5 — parents/subclasses, param-object usage: need a type
	// hierarchy / alias model not in the graph yet.
	res[4] = nil
	res[5] = nil
	// Step 6 — global/static modifications (State edges out).
	for _, e := range g.Edges {
		if e.From == changeNode && e.Kind == effectgraph.EdgeState {
			res[6] = append(res[6], e.To)
		}
	}
	return res
}

// Violated reports whether a stupid-rule's underlying assumption is in
// the refuted set — i.e. the prune is no longer safe and the affected
// sketch must be recomputed and the decision downgraded (设计文档 Part
// 6.7 "违反触发降级").
func Violated(r StupidRule, refutedAssumptionIDs map[string]bool) bool {
	if r.AssumptionID == "" {
		return false
	}
	return refutedAssumptionIDs[r.AssumptionID]
}
