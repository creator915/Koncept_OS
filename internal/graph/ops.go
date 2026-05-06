package graph

import (
	"fmt"
)

// HasID reports whether id is taken in either the attribute or object namespace.
// Per AGENT.md §4.2, attribute and object IDs share a single global namespace.
func (g *Graph) HasID(id string) bool {
	if _, ok := g.Attributes[id]; ok {
		return true
	}
	if _, ok := g.Objects[id]; ok {
		return true
	}
	return false
}

// AddAttribute inserts a new attribute. Errors if id is already used.
func (g *Graph) AddAttribute(id string, attr *Attribute) error {
	if id == "" {
		return fmt.Errorf("attribute id required")
	}
	if g.HasID(id) {
		return fmt.Errorf("id %q already exists in graph", id)
	}
	g.Attributes[id] = attr
	return nil
}

// AddObject inserts a new object. Errors if id is already used.
func (g *Graph) AddObject(id string, obj *Object) error {
	if id == "" {
		return fmt.Errorf("object id required")
	}
	if g.HasID(id) {
		return fmt.Errorf("id %q already exists in graph", id)
	}
	g.Objects[id] = obj
	return nil
}

// LinkRefine records that child <: parent in the partial order.
// Both must be existing attributes. Adds at most once (idempotent).
func (g *Graph) LinkRefine(child, parent string) error {
	c, ok := g.Attributes[child]
	if !ok {
		return fmt.Errorf("attribute %q not found", child)
	}
	if _, ok := g.Attributes[parent]; !ok {
		return fmt.Errorf("attribute %q not found", parent)
	}
	if child == parent {
		return fmt.Errorf("attribute cannot refine itself")
	}
	if contains(c.Refines, parent) {
		return nil
	}
	c.Refines = append(c.Refines, parent)
	return nil
}

// LinkConsume records that object reads attribute. Idempotent.
func (g *Graph) LinkConsume(object, attribute string) error {
	o, ok := g.Objects[object]
	if !ok {
		return fmt.Errorf("object %q not found", object)
	}
	if _, ok := g.Attributes[attribute]; !ok {
		return fmt.Errorf("attribute %q not found", attribute)
	}
	if contains(o.Consumes, attribute) {
		return nil
	}
	o.Consumes = append(o.Consumes, attribute)
	return nil
}

// LinkProduce records that object writes attribute. Idempotent.
func (g *Graph) LinkProduce(object, attribute string) error {
	o, ok := g.Objects[object]
	if !ok {
		return fmt.Errorf("object %q not found", object)
	}
	if _, ok := g.Attributes[attribute]; !ok {
		return fmt.Errorf("attribute %q not found", attribute)
	}
	if contains(o.Produces, attribute) {
		return nil
	}
	o.Produces = append(o.Produces, attribute)
	return nil
}

// UnlinkRefine removes the parent from child's refines list. Idempotent.
func (g *Graph) UnlinkRefine(child, parent string) error {
	c, ok := g.Attributes[child]
	if !ok {
		return fmt.Errorf("attribute %q not found", child)
	}
	c.Refines = removeString(c.Refines, parent)
	return nil
}

// UnlinkConsume removes the attribute from object's consumes list. Idempotent.
func (g *Graph) UnlinkConsume(object, attribute string) error {
	o, ok := g.Objects[object]
	if !ok {
		return fmt.Errorf("object %q not found", object)
	}
	o.Consumes = removeString(o.Consumes, attribute)
	return nil
}

// UnlinkProduce removes the attribute from object's produces list. Idempotent.
func (g *Graph) UnlinkProduce(object, attribute string) error {
	o, ok := g.Objects[object]
	if !ok {
		return fmt.Errorf("object %q not found", object)
	}
	o.Produces = removeString(o.Produces, attribute)
	return nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func removeString(xs []string, x string) []string {
	out := xs[:0]
	for _, v := range xs {
		if v != x {
			out = append(out, v)
		}
	}
	// preserve a separate backing array to avoid aliasing issues
	clean := make([]string, len(out))
	copy(clean, out)
	return clean
}
