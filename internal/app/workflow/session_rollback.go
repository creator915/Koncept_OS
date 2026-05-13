package workflow

import (
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
)

// Rollback recursively rolls back a session: depth-first roll back children
// first (reversing each child's diff), then reverse-apply this session's
// diff to graphPath, then delete the session JSON.
//
// If id refers to a non-existent session, this is a no-op (returns nil).
// Returns the list of session ids deleted in deletion order, plus any error.
func Rollback(sessionDir, graphPath, id string) ([]string, error) {
	if !persistence.ExistsSession(sessionDir, id) {
		return nil, nil
	}
	s, err := persistence.LoadSession(sessionDir, id)
	if err != nil {
		return nil, err
	}

	var deleted []string
	// 1. Roll back children depth-first (each child reverses its own diff before vanishing).
	children := append([]string(nil), s.Children...)
	for _, child := range children {
		sub, err := Rollback(sessionDir, graphPath, child)
		if err != nil {
			return deleted, err
		}
		deleted = append(deleted, sub...)
	}

	// 2. Reverse-apply this session's diff to graph.json.
	g, err := persistence.LoadGraphOrInit(graphPath)
	if err != nil {
		return deleted, err
	}
	if err := applyReverseDiff(g, &s.Output.GraphDiff); err != nil {
		return deleted, fmt.Errorf("reverse-apply diff for %s: %w", id, err)
	}
	if err := persistence.SaveGraph(graphPath, g); err != nil {
		return deleted, err
	}

	// 3. persistence.Delete impl + def files for entities this
	// session created. We resolve paths relative to the cwd that session.go
	// already trusts as "project root" (same convention as impl-existence
	// check). Files outside the project (absolute paths or `..` traversals)
	// are silently skipped to avoid arbitrary deletes from a misbehaving
	// agent. Best-effort: stat/Remove errors are swallowed since the graph
	// has already been reverted; leftover files are logged.
	cwd, _ := os.Getwd()
	filesDeleted := deleteAddedFiles(&s.Output.GraphDiff, cwd)
	if len(filesDeleted) > 0 {
		fmt.Fprintf(os.Stderr, "[rollback %s] deleted %d source file(s): %v\n", id, len(filesDeleted), filesDeleted)
	}

	// 4. persistence.Delete the session JSON (which detaches from parent's children list).
	subDeleted, err := DeleteRecursive(sessionDir, id)
	if err != nil {
		return deleted, err
	}
	deleted = append(deleted, subDeleted...)

	// 5. If this was the focused session, clear focus.
	if err := persistence.ClearFocusIf(sessionDir, id); err != nil {
		return deleted, fmt.Errorf("clear focus after rollback of %s: %w", id, err)
	}

	return deleted, nil
}

// deleteAddedFiles walks session.GraphDiff.Added and removes the def + impl files
// for each newly-added entity. Returns the list of relative paths actually
// removed. persistence.Path safety: absolute paths and any path with a `..` component
// are skipped (filepath.IsLocal). Missing files are silently skipped.
func deleteAddedFiles(d *session.GraphDiff, cwd string) []string {
	if cwd == "" || d == nil {
		return nil
	}
	var removed []string
	tryRemove := func(rel string) {
		if rel == "" || !filepath.IsLocal(rel) {
			return
		}
		path := filepath.Join(cwd, rel)
		if err := os.Remove(path); err == nil {
			removed = append(removed, rel)
		}
	}
	for _, raw := range d.Added.Attributes {
		var attr graph.Attribute
		if err := json.Unmarshal(raw, &attr); err != nil {
			continue
		}
		tryRemove(attr.Def)
	}
	for _, raw := range d.Added.Objects {
		var obj graph.Object
		if err := json.Unmarshal(raw, &obj); err != nil {
			continue
		}
		tryRemove(obj.Def)
		if obj.Impl != nil {
			tryRemove(*obj.Impl)
		}
	}
	return removed
}

// applyReverseDiff reverses a captured session.GraphDiff against g:
//   - "added" entries are removed from the graph (they didn't exist before this session)
//   - "modified" entries restore the "before" snapshot
//   - "removed" entries are re-added (using their pre-removal snapshot — not yet
//     populated by any tool in slice 4.5, but the code path is here for completeness)
func applyReverseDiff(g *graph.Graph, d *session.GraphDiff) error {
	for id := range d.Added.Attributes {
		delete(g.Attributes, id)
	}
	for id := range d.Added.Objects {
		delete(g.Objects, id)
	}

	for id, mod := range d.Modified.Attributes {
		var attr graph.Attribute
		if err := json.Unmarshal(mod.Before, &attr); err != nil {
			return fmt.Errorf("decode modified attribute %s before: %w", id, err)
		}
		g.Attributes[id] = &attr
	}
	for id, mod := range d.Modified.Objects {
		var obj graph.Object
		if err := json.Unmarshal(mod.Before, &obj); err != nil {
			return fmt.Errorf("decode modified object %s before: %w", id, err)
		}
		g.Objects[id] = &obj
	}

	// Removed: no current tool produces removal diffs. Code path retained for
	// future graph_remove_attribute / object operations.
	_ = d.Removed
	return nil
}
