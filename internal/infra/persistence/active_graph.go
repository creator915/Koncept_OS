package persistence

import (
	"fmt"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/domain/session"
)

// Active-graph resolution (KonceptOS_implementation_plan.md §1.1/§1.3).
//
// A session's writes target exactly one graph file:
//   - a session that EXPANDS an object (ExpandsObject != "") owns an
//     isolated sub-hypergraph at K/expansions/<sessionID>/graph.json;
//   - EVERY OTHER session — root AND ordinary spawn_subagent child
//     sessions — writes the top-level K/graph.json.
//
// 2026-05-19 fix: the previous keying on `Parent != ""` was wrong — it
// treated every ordinary subagent child session (the pervasive kcpos
// pattern) as an expansion, diverting its graph writes to a phantom
// empty K/expansions/ layer while reads stayed top-level, dead-locking
// real multi-agent runs ("object X not found" though X is in
// K/graph.json). Expansion is keyed STRICTLY on ExpandsObject.
//
// A nil session safely degrades to the top-level path, so callers with
// no active session keep today's flat-graph behaviour (P0.2 取舍#2).
//
// Dependency direction note: persistence (infra) importing
// domain/session is the normal direction — session_store.go already
// does. persistence MUST NOT import internal/tools/* (P0.2 风险#3,
// verified by grep in P1.1.2 acceptance).

// ActiveGraphPath returns the graph file the session's writes target.
// It never errors and never escapes: root/nil → top-level; a
// well-formed sub-session → its expansion path. A MALFORMED sub-session
// id falls back to the top-level path here for safety, but callers
// should use LoadActiveGraph/SaveActiveGraph which fail closed with a
// diagnosable error instead of silently polluting the top-level graph.
func ActiveGraphPath(sess *session.Session) string {
	if sess == nil || sess.ExpandsObject == "" {
		return GraphDefaultPath
	}
	if session.ValidateID(sess.ID) != nil {
		return GraphDefaultPath
	}
	return ExpansionGraphPath(sess.ID)
}

// activeGraphPathChecked is the fail-closed guard behind Load/Save: a
// sub-session whose id is malformed cannot derive a path at all (a
// hostile id like "../../x" would otherwise escape K/expansions/ — or,
// worse, silently land in the top-level graph). Root/nil → top-level,
// never errors.
func activeGraphPathChecked(sess *session.Session) (string, error) {
	if sess == nil || sess.ExpandsObject == "" {
		return GraphDefaultPath, nil
	}
	if err := session.ValidateID(sess.ID); err != nil {
		return "", fmt.Errorf("ActiveGraph: refusing to derive a graph path "+
			"for malformed expansion-session id %q (fail-closed, not polluting "+
			"top-level): %w", sess.ID, err)
	}
	return ExpansionGraphPath(sess.ID), nil
}

// ActiveGraphPathFromFocus is the single source of truth for "which
// graph layer does the currently-focused session write to". It resolves
// the focus pointer → session → ActiveGraphPath. No focus, unreadable
// session, or root session ⇒ GraphDefaultPath (byte-identical to
// pre-layered behaviour, P0.2 取舍#2). Used by every write chokepoint
// (graphtools.mutateGraph, services.mutateGraph) so layered routing has
// exactly one implementation.
func ActiveGraphPathFromFocus() string {
	fid, _ := GetFocus(SessionDefaultDir)
	if fid == "" {
		return GraphDefaultPath
	}
	sess, err := LoadSession(SessionDefaultDir, fid)
	if err != nil || sess == nil {
		return GraphDefaultPath
	}
	return ActiveGraphPath(sess)
}

// LoadActiveGraph loads the graph for the session's active layer,
// init-empty if absent. Fails closed on a malformed sub-session id.
func LoadActiveGraph(sess *session.Session) (*graph.Graph, error) {
	p, err := activeGraphPathChecked(sess)
	if err != nil {
		return nil, err
	}
	return LoadGraphOrInit(p)
}

// SaveActiveGraph writes the graph to the session's active layer,
// creating K/expansions/<sid>/ as needed. Fails closed on a malformed
// sub-session id (so sub-session content never silently pollutes the
// top-level K/graph.json).
func SaveActiveGraph(sess *session.Session, g *graph.Graph) error {
	p, err := activeGraphPathChecked(sess)
	if err != nil {
		return err
	}
	return SaveGraph(p, g)
}
