package graph

import "encoding/json"

// Clone returns a deep copy of g. Implementation goes through JSON
// round-tripping — robust against new fields without per-field plumbing,
// at the cost of some allocation. Acceptable for graph sizes we expect
// (a few thousand nodes/edges max).
func (g *Graph) Clone() *Graph {
	data, err := json.Marshal(g)
	if err != nil {
		// JSON marshal of our own typed structs cannot realistically fail.
		// If it does, return an empty graph rather than panic so callers can
		// continue gracefully.
		return NewGraph()
	}
	out := NewGraph()
	if err := json.Unmarshal(data, out); err != nil {
		return NewGraph()
	}
	if out.Attributes == nil {
		out.Attributes = map[string]*Attribute{}
	}
	if out.Objects == nil {
		out.Objects = map[string]*Object{}
	}
	return out
}
