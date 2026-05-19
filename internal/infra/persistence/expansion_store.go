package persistence

import (
	"path/filepath"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
)

// Layered hypergraph storage (KonceptOS_implementation_plan.md §1.1).
//
// The top-level hypergraph stays at GraphDefaultPath ("K/graph.json").
// When a node is expanded, its child sub-hypergraph lives in its own
// isolated file: K/expansions/<sessionID>/graph.json. Rolling a session
// back means deleting that directory — the top-level graph is never
// touched. These helpers REUSE the top-level Load/Save unchanged (their
// signatures are not modified), so an empty Expansion just degrades to
// today's flat-graph behaviour.

// ExpansionGraphPath returns the on-disk location of the sub-hypergraph
// owned by session sid.
func ExpansionGraphPath(sid string) string {
	return filepath.Join("K", "expansions", sid, "graph.json")
}

// LoadExpansionGraphOrInit returns the sub-hypergraph for sid, creating
// an empty graph in memory if the file does not exist yet (not written
// until SaveExpansionGraph is called).
func LoadExpansionGraphOrInit(sid string) (*graph.Graph, error) {
	return LoadGraphOrInit(ExpansionGraphPath(sid))
}

// SaveExpansionGraph writes the sub-hypergraph for sid, creating
// K/expansions/<sid>/ as needed (SaveGraph mkdir's parent dirs).
func SaveExpansionGraph(sid string, g *graph.Graph) error {
	return SaveGraph(ExpansionGraphPath(sid), g)
}
