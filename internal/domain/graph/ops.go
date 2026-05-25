package graph

import (
	"fmt"
	"strings"
)

// HasID reports whether id is taken in either the attribute or object namespace.
// Attribute and object IDs share a single global namespace.
func (g *Graph) HasID(id string) bool {
	if _, ok := g.Attributes[id]; ok {
		return true
	}
	if _, ok := g.Objects[id]; ok {
		return true
	}
	return false
}

// AddAttribute inserts a new attribute. Errors if id is already used
// or if its Go-symbol form collides with any existing entity.
func (g *Graph) AddAttribute(id string, attr *Attribute) error {
	if id == "" {
		return fmt.Errorf("attribute id required")
	}
	if g.HasID(id) {
		return fmt.Errorf("id %q already exists in graph", id)
	}
	if other := g.SymbolCollides(id); other != "" {
		return symbolCollisionError("attribute", id, other)
	}
	g.Attributes[id] = attr
	return nil
}

// AddObject inserts a new object. Errors if id is already used or if
// its Go-symbol form collides with any existing entity.
func (g *Graph) AddObject(id string, obj *Object) error {
	if id == "" {
		return fmt.Errorf("object id required")
	}
	if g.HasID(id) {
		return fmt.Errorf("id %q already exists in graph", id)
	}
	if other := g.SymbolCollides(id); other != "" {
		return symbolCollisionError("object", id, other)
	}
	g.Objects[id] = obj
	return nil
}

// DeleteObject removes an object from the graph by id. Errors if id is
// not found. Edge cleanup is NOT performed — Object edges
// (consumes/produces/mutates) live as []string fields ON the object
// being deleted, so they go with it. Other objects that happen to
// share the same attributes as this object are not touched (attributes
// are not owned by objects). Orphan attributes (no remaining object
// references them) are left for explicit DeleteAttribute calls or for
// a future graph-prune pass.
//
// Added 2026-05-25 to support H_repair_graph's IO-boundary repair
// heuristic, which proposes removing graph-tracked IO-bound objects
// after the synth chain proves it can't generate testable cases for
// them. Without a delete primitive the repair proposal had no way to
// be applied.
func (g *Graph) DeleteObject(id string) error {
	if id == "" {
		return fmt.Errorf("object id required")
	}
	if _, ok := g.Objects[id]; !ok {
		return fmt.Errorf("object %q not in graph", id)
	}
	delete(g.Objects, id)
	return nil
}

// SymbolCollides returns the id of any existing object/attribute whose
// Go-style PascalCase symbol form matches that of the given new id, or
// "" if none collides. The check is lang-agnostic — even non-Go projects
// benefit from disjoint identifier namespaces, and being strict here
// prevents the 2026-05-14 walk batch failure where attribute id
// "preview_content" (→ type PreviewContent) and object id
// "PreviewContent" (→ func PreviewContent) both compiled to the same
// Go symbol and broke the multi-file package staging.
//
// Exact-match ids are not flagged here — HasID covers those — so the
// returned collision is always a *different* id that normalizes to the
// same symbol form.
func (g *Graph) SymbolCollides(newID string) string {
	target := toPascalCase(newID)
	if target == "" {
		return ""
	}
	for oid := range g.Objects {
		if oid == newID {
			continue
		}
		if toPascalCase(oid) == target {
			return oid
		}
	}
	for aid := range g.Attributes {
		if aid == newID {
			continue
		}
		if toPascalCase(aid) == target {
			return aid
		}
	}
	return ""
}

// toPascalCase normalises an identifier (snake_case, kebab-case, or
// already-Pascal) to the Go-symbol form a code-generator would emit.
// "preview_content" / "preview-content" / "PreviewContent" all map to
// "PreviewContent". Empty / all-separator input yields "".
func toPascalCase(s string) string {
	if s == "" {
		return ""
	}
	// Split on underscore and hyphen; collapse multiple separators.
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-'
	})
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		first := strings.ToUpper(p[:1])
		b.WriteString(first)
		if len(p) > 1 {
			b.WriteString(p[1:])
		}
	}
	return b.String()
}

func symbolCollisionError(newKind, newID, existingID string) error {
	return fmt.Errorf(
		"%s id %q would generate the same Go-style symbol (%q) as existing entity %q. "+
			"Disjoint symbol names prevent multi-file Go builds from hitting 'X redeclared in this block' (2026-05-14 walk batch class of failure). "+
			"Rename %q (e.g. add a suffix like _value, _result, _kind) or rename %q so the PascalCase forms differ.",
		newKind, newID, toPascalCase(newID), existingID, newID, existingID)
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

// LinkMutate records that object reads AND writes attribute in place
// (mutation semantics — common for JS object property assignment, in-place
// data structure updates, etc.). The §5.3 design distinguishes mutation
// from production: a mutation does not break referential transparency the
// way a fresh produce does, but it IS a write — preflight cycle detection
// must skip these edges (otherwise self-loops and "Foo mutates X →
// consumes X" trip false cycles). Idempotent.
func (g *Graph) LinkMutate(object, attribute string) error {
	o, ok := g.Objects[object]
	if !ok {
		return fmt.Errorf("object %q not found", object)
	}
	if _, ok := g.Attributes[attribute]; !ok {
		return fmt.Errorf("attribute %q not found", attribute)
	}
	if contains(o.Mutates, attribute) {
		return nil
	}
	o.Mutates = append(o.Mutates, attribute)
	return nil
}

// UnlinkMutate removes the attribute from object's mutates list. Idempotent.
func (g *Graph) UnlinkMutate(object, attribute string) error {
	o, ok := g.Objects[object]
	if !ok {
		return fmt.Errorf("object %q not found", object)
	}
	o.Mutates = removeString(o.Mutates, attribute)
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
