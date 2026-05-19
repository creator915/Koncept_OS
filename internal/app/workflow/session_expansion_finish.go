package workflow

import (
	"fmt"
	"os"
	"sort"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
)

// Expansion session-finish (KonceptOS_implementation_plan.md §1.3).
//
// session-finish gate for a session that expands a top-graph object:
//   - sub-hypergraph non-empty and ALL its objects confirmed
//   - sub-hypergraph validate passes (produce/consume balance, …)
//   - all child sessions finished
//   - produce/consume correspondence (the doc's 偏序: "只要生产消费对应
//     即可" — every output the parent object declares must be realized
//     by a confirmed object in the sub-hypergraph)
// Pass ⇒ PropagateExpansion sets parent.Expansion=<sid>, status=confirmed.
// Fail ⇒ a LIST of reasons (never a single opaque error).
//
// confirmed stays NON-hand-settable: the parent object's confirmed here
// is conferred by this mechanical gate (which itself requires every
// sub-object confirmed via the verification chain), not by an LLM patch.

// ExpansionFinishReasons returns the gate failure reasons (empty slice =
// pass). It is a no-op (nil) for a session that does not expand an
// object — plain sessions keep their existing finish behaviour.
func ExpansionFinishReasons(sessionDir, graphPath, id string) ([]string, error) {
	s, err := persistence.LoadSession(sessionDir, id)
	if err != nil {
		return nil, err
	}
	if s.ExpandsObject == "" {
		return nil, nil // not an expansion session — unchanged path
	}
	var reasons []string

	for _, c := range s.Children {
		cs, e := persistence.LoadSession(sessionDir, c)
		if e != nil || cs.Status != session.StatusFinished {
			reasons = append(reasons, fmt.Sprintf("child session %s is not finished", c))
		}
	}

	sub, _ := persistence.LoadExpansionGraphOrInit(id)
	if len(sub.Objects) == 0 {
		reasons = append(reasons, "sub-hypergraph has no objects (nothing was expanded)")
	}
	oids := make([]string, 0, len(sub.Objects))
	for oid := range sub.Objects {
		oids = append(oids, oid)
	}
	sort.Strings(oids)
	for _, oid := range oids {
		if sub.Objects[oid].Status != graph.StatusConfirmed {
			reasons = append(reasons, fmt.Sprintf("sub-object %s is %q, not confirmed", oid, sub.Objects[oid].Status))
		}
	}

	cwd, _ := os.Getwd()
	if vr := sub.Validate(cwd); vr.HasErrors() {
		reasons = append(reasons, "sub-hypergraph validate has errors:\n"+vr.String())
	}

	// 偏序 = produce/consume correspondence with the parent object.
	if top, e := persistence.LoadGraph(graphPath); e == nil {
		if po, ok := top.Objects[s.ExpandsObject]; ok {
			for _, want := range po.Produces {
				if !subObjectProduces(sub, want) {
					reasons = append(reasons, fmt.Sprintf(
						"parent output %q is not realized by any confirmed sub-object (产销对应/偏序)", want))
				}
			}
		}
	}
	return reasons, nil
}

// subObjectProduces reports whether some CONFIRMED object in the
// sub-hypergraph produces want, directly or via a refinement of it.
func subObjectProduces(sub *graph.Graph, want string) bool {
	for _, o := range sub.Objects {
		if o.Status != graph.StatusConfirmed {
			continue
		}
		for _, p := range o.Produces {
			if p == want || sub.Refines(p, want) {
				return true
			}
		}
	}
	return false
}

// PropagateExpansion is the success side: the parent object's
// Expansion=<session id> and status=confirmed are conferred on the top
// graph. Call ONLY after ExpansionFinishReasons returned empty.
func PropagateExpansion(sessionDir, graphPath, id string) error {
	s, err := persistence.LoadSession(sessionDir, id)
	if err != nil {
		return err
	}
	if s.ExpandsObject == "" {
		return nil
	}
	top, err := persistence.LoadGraphOrInit(graphPath)
	if err != nil {
		return err
	}
	obj, ok := top.Objects[s.ExpandsObject]
	if !ok {
		return fmt.Errorf("PropagateExpansion: parent object %q vanished from top graph", s.ExpandsObject)
	}
	sid := id
	obj.Expansion = &sid
	obj.Status = graph.StatusConfirmed
	top.Objects[s.ExpandsObject] = obj
	return persistence.SaveGraph(graphPath, top)
}
