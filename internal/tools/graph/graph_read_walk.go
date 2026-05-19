package graphtools

import (
	"fmt"
	"sort"
	"strings"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
)

// Graph-walk helpers for the graph-read family (graph_read.go). All
// read-only: they LoadExpansionGraphOrInit and never Save. Recursion is
// bounded by a seen-set keyed on expansion session id.

func sortedObjectIDs(g *graph.Graph) []string {
	ids := make([]string, 0, len(g.Objects))
	for id := range g.Objects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// renderExpanded shows node id in g, then if it is an object with an
// expansion hyperlink, descends into K/expansions/<sid>/graph.json and
// renders every node there (recursively). seenSID bounds the recursion.
func renderExpanded(g *graph.Graph, id string, seenSID map[string]bool, depth int) string {
	pad := strings.Repeat("  ", depth)
	body, err := g.Show(id)
	if err != nil {
		return pad + fmt.Sprintf("(%s: %v)\n", id, err)
	}
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		b.WriteString(pad + line + "\n")
	}
	obj, ok := g.Objects[id]
	if !ok || obj.Expansion == nil {
		return b.String()
	}
	sid := *obj.Expansion
	fmt.Fprintf(&b, "%s  ↳ expansion: %s → %s\n", pad, sid, persistence.ExpansionGraphPath(sid))
	if seenSID[sid] {
		return b.String()
	}
	seenSID[sid] = true
	sub, _ := persistence.LoadExpansionGraphOrInit(sid)
	for _, cid := range sortedObjectIDs(sub) {
		b.WriteString(renderExpanded(sub, cid, seenSID, depth+1))
	}
	return b.String()
}

// validateDeep validates g, then recurses into every expansion present
// in g and validates those sub-hypergraphs too.
func validateDeep(g *graph.Graph, label string, seenSID map[string]bool, cwd string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s\n", label, g.Validate(cwd).String())
	for _, oid := range sortedObjectIDs(g) {
		obj := g.Objects[oid]
		if obj.Expansion == nil || seenSID[*obj.Expansion] {
			continue
		}
		seenSID[*obj.Expansion] = true
		sub, _ := persistence.LoadExpansionGraphOrInit(*obj.Expansion)
		b.WriteString(validateDeep(sub, "expansion "+*obj.Expansion, seenSID, cwd))
	}
	return b.String()
}

// downstreamObjects: BFS over the current layer — objects that consume
// or mutate attr, then objects consuming what those produce, transitively.
func downstreamObjects(g *graph.Graph, attr string) []string {
	attrQ, seenA := []string{attr}, map[string]bool{attr: true}
	var out []string
	seenO := map[string]bool{}
	for len(attrQ) > 0 {
		cur := attrQ[0]
		attrQ = attrQ[1:]
		for _, oid := range sortedObjectIDs(g) {
			o := g.Objects[oid]
			if !contains(o.Consumes, cur) && !contains(o.Mutates, cur) {
				continue
			}
			if !seenO[oid] {
				seenO[oid] = true
				out = append(out, oid)
			}
			for _, p := range o.Produces {
				if !seenA[p] {
					seenA[p] = true
					attrQ = append(attrQ, p)
				}
			}
		}
	}
	return out
}

func queryDownstreamRec(layer string, g *graph.Graph, attr string, seenSID map[string]bool) string {
	objs := downstreamObjects(g, attr)
	var b strings.Builder
	listed := "(none)"
	if len(objs) > 0 {
		listed = strings.Join(objs, ", ")
	}
	fmt.Fprintf(&b, "downstream@%s of %q: %s\n", layer, attr, listed)
	for _, oid := range objs {
		o := g.Objects[oid]
		if o.Expansion == nil || seenSID[*o.Expansion] {
			continue
		}
		seenSID[*o.Expansion] = true
		sub, _ := persistence.LoadExpansionGraphOrInit(*o.Expansion)
		b.WriteString(queryDownstreamRec(*o.Expansion, sub, attr, seenSID))
	}
	return b.String()
}
