package graph

import (
	"fmt"
	"sort"
	"strings"
)

// RenderMermaid returns Mermaid `graph LR` syntax representing the graph.
//
// Visual conventions:
//   - Attributes: rectangles  id["name (status)"]
//   - Objects:    rounded     id("name (status)")
//   - consume:    attribute → object
//   - produce:    object → attribute
//   - refine:     dashed, child ─.─> parent  with label "refines"
//
// Status drives the fill color via Mermaid classDef:
//   declared (red-tint) · implementing (amber) · confirmed (green-tint)
//
// Output is deterministic: ids are sorted before emission so two runs on
// the same graph produce byte-identical output (good for git diffs and
// hashed tests).
func (g *Graph) RenderMermaid() string {
	var b strings.Builder
	b.WriteString("graph LR\n")

	attrIDs := sortedKeys(g.Attributes)
	objIDs := sortedKeys(g.Objects)

	// Nodes
	for _, id := range attrIDs {
		a := g.Attributes[id]
		fmt.Fprintf(&b, "  %s[\"%s<br/><i>%s</i>\"]:::%s\n",
			id, escapeMermaid(id), a.Status, classFor(a.Status))
	}
	for _, id := range objIDs {
		o := g.Objects[id]
		fmt.Fprintf(&b, "  %s([\"%s<br/><i>%s</i>\"]):::%s\n",
			id, escapeMermaid(id), o.Status, classFor(o.Status))
	}

	// Edges
	for _, id := range attrIDs {
		for _, parent := range g.Attributes[id].Refines {
			fmt.Fprintf(&b, "  %s -.->|refines| %s\n", id, parent)
		}
	}
	for _, id := range objIDs {
		o := g.Objects[id]
		for _, attr := range o.Consumes {
			fmt.Fprintf(&b, "  %s --> %s\n", attr, id)
		}
		for _, attr := range o.Produces {
			fmt.Fprintf(&b, "  %s --> %s\n", id, attr)
		}
	}

	// Status classes (always emit, harmless if unused)
	b.WriteString("\n")
	b.WriteString("  classDef declared fill:#fee,stroke:#c66,color:#000\n")
	b.WriteString("  classDef implementing fill:#fec,stroke:#c80,color:#000\n")
	b.WriteString("  classDef confirmed fill:#cfc,stroke:#080,color:#000\n")
	return b.String()
}

// RenderDot returns Graphviz DOT syntax. Same semantics as RenderMermaid;
// useful when you want to pipe to `dot -Tsvg` or `dot -Tpng`.
func (g *Graph) RenderDot() string {
	var b strings.Builder
	b.WriteString("digraph hypergraph {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [fontname=\"Arial\"];\n")
	b.WriteString("  edge [fontname=\"Arial\"];\n\n")

	attrIDs := sortedKeys(g.Attributes)
	objIDs := sortedKeys(g.Objects)

	// Attribute nodes — boxes
	for _, id := range attrIDs {
		a := g.Attributes[id]
		fmt.Fprintf(&b, "  %q [shape=box, style=filled, fillcolor=%q, label=%q];\n",
			id, dotFill(a.Status), fmt.Sprintf("%s\\n%s", id, a.Status))
	}
	// Object nodes — ellipses (default)
	for _, id := range objIDs {
		o := g.Objects[id]
		fmt.Fprintf(&b, "  %q [shape=ellipse, style=filled, fillcolor=%q, label=%q];\n",
			id, dotFill(o.Status), fmt.Sprintf("%s\\n%s", id, o.Status))
	}

	// Edges
	for _, id := range attrIDs {
		for _, parent := range g.Attributes[id].Refines {
			fmt.Fprintf(&b, "  %q -> %q [style=dashed, label=\"refines\"];\n", id, parent)
		}
	}
	for _, id := range objIDs {
		o := g.Objects[id]
		for _, attr := range o.Consumes {
			fmt.Fprintf(&b, "  %q -> %q;\n", attr, id)
		}
		for _, attr := range o.Produces {
			fmt.Fprintf(&b, "  %q -> %q;\n", id, attr)
		}
	}

	b.WriteString("}\n")
	return b.String()
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func classFor(status string) string {
	switch status {
	case StatusImplementing:
		return "implementing"
	case StatusConfirmed:
		return "confirmed"
	default:
		return "declared"
	}
}

func dotFill(status string) string {
	switch status {
	case StatusImplementing:
		return "#ffeecc"
	case StatusConfirmed:
		return "#ccffcc"
	default:
		return "#ffeeee"
	}
}

// escapeMermaid escapes characters that confuse Mermaid label parsing.
// Our id convention is alphanumeric + underscore so this is a defensive
// no-op for valid graphs; we keep the function so future label extensions
// (e.g. embedding intent) don't break the renderer.
func escapeMermaid(s string) string {
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
