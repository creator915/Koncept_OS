package graph

// Refines reports whether descendant <: ancestor in the refines DAG.
// Reflexive: any id refines itself. Transitive over Refines edges.
// Returns false if descendant is not an attribute id, or the chain
// contains a non-existent attribute (defensive — should not happen in
// a validated graph).
func (g *Graph) Refines(descendant, ancestor string) bool {
	if descendant == ancestor {
		return true
	}
	visited := map[string]bool{}
	return g.refinesDFS(descendant, ancestor, visited)
}

func (g *Graph) refinesDFS(cur, target string, visited map[string]bool) bool {
	if visited[cur] {
		return false
	}
	visited[cur] = true
	a, ok := g.Attributes[cur]
	if !ok {
		return false
	}
	for _, parent := range a.Refines {
		if parent == target {
			return true
		}
		if g.refinesDFS(parent, target, visited) {
			return true
		}
	}
	return false
}
