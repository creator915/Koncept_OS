package graph

import (
	"fmt"
	"sort"
	"strings"
)

// Show returns a multi-line description of the node identified by id and its
// immediate neighbors in the graph. Useful for the agent to reason about a
// node's local topology without dumping the whole graph.
func (g *Graph) Show(id string) (string, error) {
	if attr, ok := g.Attributes[id]; ok {
		return g.showAttribute(id, attr), nil
	}
	if obj, ok := g.Objects[id]; ok {
		return g.showObject(id, obj), nil
	}
	return "", fmt.Errorf("id %q not found", id)
}

func (g *Graph) showAttribute(id string, a *Attribute) string {
	var b strings.Builder
	fmt.Fprintf(&b, "attribute %s\n", id)
	fmt.Fprintf(&b, "  status: %s\n", a.Status)
	fmt.Fprintf(&b, "  intent: %s\n", a.Intent)
	if a.Def != "" {
		fmt.Fprintf(&b, "  def: %s\n", a.Def)
	}
	fmt.Fprintf(&b, "  refines: %s\n", listOrEmpty(a.Refines))
	if children := refinedBy(g, id); len(children) > 0 {
		fmt.Fprintf(&b, "  refined by: %s\n", strings.Join(children, ", "))
	}
	if consumers := consumedBy(g, id); len(consumers) > 0 {
		fmt.Fprintf(&b, "  consumed by: %s\n", strings.Join(consumers, ", "))
	}
	if producers := producedBy(g, id); len(producers) > 0 {
		fmt.Fprintf(&b, "  produced by: %s\n", strings.Join(producers, ", "))
	}
	if a.ValueSpace != nil {
		fmt.Fprintf(&b, "  valueSpace: %v\n", a.ValueSpace)
	}
	if len(a.ConfirmedOps) > 0 {
		fmt.Fprintf(&b, "  confirmedOps: %s\n", strings.Join(a.ConfirmedOps, ", "))
	}
	if len(a.Laws) > 0 {
		fmt.Fprintf(&b, "  laws: %s\n", strings.Join(a.Laws, "; "))
	}
	return b.String()
}

func (g *Graph) showObject(id string, o *Object) string {
	var b strings.Builder
	fmt.Fprintf(&b, "object %s\n", id)
	fmt.Fprintf(&b, "  status: %s\n", o.Status)
	fmt.Fprintf(&b, "  intent: %s\n", o.Intent)
	if o.Def != "" {
		fmt.Fprintf(&b, "  def: %s\n", o.Def)
	}
	if o.Impl != nil {
		fmt.Fprintf(&b, "  impl: %s\n", *o.Impl)
	}
	fmt.Fprintf(&b, "  consumes: %s\n", listOrEmpty(o.Consumes))
	fmt.Fprintf(&b, "  produces: %s\n", listOrEmpty(o.Produces))
	if o.Temporal != nil {
		fmt.Fprintf(&b, "  temporal frameVar: %s\n", o.Temporal.FrameVar)
	}
	if o.Preconditions != "" {
		fmt.Fprintf(&b, "  preconditions: %s\n", o.Preconditions)
	}
	if o.Postconditions != "" {
		fmt.Fprintf(&b, "  postconditions: %s\n", o.Postconditions)
	}
	return b.String()
}

func refinedBy(g *Graph, parentID string) []string {
	var out []string
	for id, a := range g.Attributes {
		if contains(a.Refines, parentID) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func consumedBy(g *Graph, attrID string) []string {
	var out []string
	for id, o := range g.Objects {
		if contains(o.Consumes, attrID) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func producedBy(g *Graph, attrID string) []string {
	var out []string
	for id, o := range g.Objects {
		if contains(o.Produces, attrID) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func listOrEmpty(xs []string) string {
	if len(xs) == 0 {
		return "[]"
	}
	return strings.Join(xs, ", ")
}
