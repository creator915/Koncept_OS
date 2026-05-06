package graph

import (
	"fmt"
	"sort"
	"strings"
)

// PreflightResult is the safety analysis for parallelizing a batch of objects
// per CLAUDE.md §5.4 path B step 4-5.
type PreflightResult struct {
	Status   string     // "SAFE" or "UNSAFE"
	Waves    [][]string // topologically sorted; each wave is parallel-safe
	Cycle    []string   // populated when Status=UNSAFE due to cyclic deps
	Warnings []string   // value-dep concerns (not blocking)
	Unknown  []string   // ids passed in but not present in the graph
}

// Preflight analyzes a batch of object ids for parallel-execution safety.
// Builds a dependency graph among them based on consumes/produces edges
// (with subtype substitution from the refines partial order), detects
// cycles, and groups the rest into topologically sorted waves where each
// wave's members can be implemented in parallel.
//
// Limitation: value-dependency detection per spec §5.4 step 4(b) is heuristic.
// We flag (as warnings, not errors) cases where two objects in the same wave
// consume the same attribute — they may need serial execution if their
// value-space assumptions diverge. Mechanical detection would require
// confirmed valueSpace semantics; the agent must judge.
func (g *Graph) Preflight(objectIDs []string) *PreflightResult {
	r := &PreflightResult{Status: "SAFE", Waves: [][]string{}}

	// 1. Validate all ids exist as objects.
	for _, id := range objectIDs {
		if _, ok := g.Objects[id]; !ok {
			r.Unknown = append(r.Unknown, id)
		}
	}
	if len(r.Unknown) > 0 {
		r.Status = "UNSAFE"
		return r
	}
	if len(objectIDs) == 0 {
		return r
	}

	// 2. Build dep map: deps[id] = ids in batch that id depends on
	//    (some other obj in batch produces an attr id consumes, directly or via subtype).
	deps := buildDeps(g, objectIDs)

	// 3. Cycle detection.
	if cycle := findCycle(deps, objectIDs); len(cycle) > 0 {
		r.Status = "UNSAFE"
		r.Cycle = cycle
		return r
	}

	// 4. Topological sort into waves (Kahn's, processed in stable order for determinism).
	r.Waves = topoWaves(deps, objectIDs)

	// 5. Value-dep warnings (heuristic).
	for waveIdx, wave := range r.Waves {
		shared := map[string][]string{}
		for _, id := range wave {
			for _, attr := range g.Objects[id].Consumes {
				shared[attr] = append(shared[attr], id)
			}
		}
		// stable iteration: sort attribute keys
		attrs := make([]string, 0, len(shared))
		for a := range shared {
			attrs = append(attrs, a)
		}
		sort.Strings(attrs)
		for _, attr := range attrs {
			consumers := shared[attr]
			if len(consumers) < 2 {
				continue
			}
			sort.Strings(consumers)
			r.Warnings = append(r.Warnings, fmt.Sprintf(
				"wave %d: %s all consume %q — verify value-space alignment (potential value-dep, may need serial execution)",
				waveIdx, strings.Join(consumers, " & "), attr))
		}
	}

	return r
}

func buildDeps(g *Graph, ids []string) map[string]map[string]bool {
	idSet := map[string]bool{}
	for _, id := range ids {
		idSet[id] = true
	}
	deps := map[string]map[string]bool{}
	for _, id := range ids {
		deps[id] = map[string]bool{}
	}
	for _, consumerID := range ids {
		consumer := g.Objects[consumerID]
		for _, needed := range consumer.Consumes {
			for _, producerID := range ids {
				if producerID == consumerID {
					continue
				}
				producer := g.Objects[producerID]
				for _, prod := range producer.Produces {
					// Direct match or subtype substitution: producer outputs prod,
					// consumer needs `needed`, accepts if prod == needed or prod refines needed.
					if prod == needed || g.Refines(prod, needed) {
						deps[consumerID][producerID] = true
						break
					}
				}
			}
		}
	}
	return deps
}

func findCycle(deps map[string]map[string]bool, order []string) []string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	parent := map[string]string{}
	var cycle []string

	// stable iteration: walk dependency neighbors in sorted order so cycle
	// reporting is reproducible across runs / map orderings.
	neighbors := func(id string) []string {
		out := make([]string, 0, len(deps[id]))
		for d := range deps[id] {
			out = append(out, d)
		}
		sort.Strings(out)
		return out
	}

	var dfs func(id string) bool
	dfs = func(id string) bool {
		color[id] = gray
		for _, d := range neighbors(id) {
			if color[d] == gray {
				cycle = traceBack(parent, id, d)
				return true
			}
			if color[d] == white {
				parent[d] = id
				if dfs(d) {
					return true
				}
			}
		}
		color[id] = black
		return false
	}

	for _, id := range order {
		if color[id] == white {
			if dfs(id) {
				return cycle
			}
		}
	}
	return nil
}

// traceBack reconstructs the cycle path. fromID has a back-edge to closeID
// (which is in the recursion stack). The cycle is closeID → ... → fromID → closeID.
func traceBack(parent map[string]string, fromID, closeID string) []string {
	var path []string
	cur := fromID
	for cur != closeID {
		path = append(path, cur)
		next, ok := parent[cur]
		if !ok {
			break
		}
		cur = next
	}
	path = append(path, closeID)
	// reverse so closeID comes first
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	path = append(path, closeID) // close the loop visually
	return path
}

func topoWaves(deps map[string]map[string]bool, order []string) [][]string {
	inDegree := map[string]int{}
	dependents := map[string][]string{}
	for id, ds := range deps {
		inDegree[id] = len(ds)
		for d := range ds {
			dependents[d] = append(dependents[d], id)
		}
	}
	placed := map[string]bool{}
	var waves [][]string
	for len(placed) < len(order) {
		var wave []string
		for _, id := range order {
			if placed[id] {
				continue
			}
			if inDegree[id] == 0 {
				wave = append(wave, id)
			}
		}
		if len(wave) == 0 {
			// Defensive: shouldn't happen if findCycle returned nil.
			break
		}
		sort.Strings(wave)
		waves = append(waves, wave)
		for _, id := range wave {
			placed[id] = true
			for _, dep := range dependents[id] {
				inDegree[dep]--
			}
		}
	}
	return waves
}

// String renders the result as a human/agent-readable report.
func (r *PreflightResult) String() string {
	var b strings.Builder
	totalObj := 0
	for _, w := range r.Waves {
		totalObj += len(w)
	}
	fmt.Fprintf(&b, "preflight: %s (%d objects, %d wave(s))\n", r.Status, totalObj, len(r.Waves))
	if len(r.Unknown) > 0 {
		fmt.Fprintf(&b, "\nunknown object ids: %s\n", strings.Join(r.Unknown, ", "))
	}
	if r.Status == "UNSAFE" && len(r.Cycle) > 0 {
		fmt.Fprintf(&b, "\ncycle: %s\n", strings.Join(r.Cycle, " → "))
	}
	for i, wave := range r.Waves {
		fmt.Fprintf(&b, "\nwave %d (%d):\n", i, len(wave))
		for _, id := range wave {
			fmt.Fprintf(&b, "  %s\n", id)
		}
	}
	if len(r.Warnings) > 0 {
		fmt.Fprintf(&b, "\nwarnings:\n")
		for _, w := range r.Warnings {
			fmt.Fprintf(&b, "  %s\n", w)
		}
	}
	return b.String()
}
