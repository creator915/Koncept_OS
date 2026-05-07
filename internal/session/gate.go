package session

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/creator915/Koncept_OS/internal/checkpoint"
	"github.com/creator915/Koncept_OS/internal/graph"
)

// GateReport summarizes the result of a session-finish gate check.
type GateReport struct {
	SessionID string   `json:"sessionId"`
	Status    string   `json:"status"` // "PASS" or "FAIL"
	Issues    []string `json:"issues"`
}

// CheckGate verifies the conditions a session must meet before it can be
// marked finished. Mechanical verification only: skips any
// gameplayProof / runtime-simulation requirements since those cannot be
// reliably checked by an agent.
//
// Args:
//   - sessionDir: K/sessions/
//   - graphPath: K/graph.json
//   - checkpointPath: K/checkpoint.json (pass "" to skip checkpoint check)
//   - id: session id to gate
func CheckGate(sessionDir, graphPath, checkpointPath, id string) (*GateReport, error) {
	r := &GateReport{SessionID: id, Status: "PASS"}
	s, err := Load(sessionDir, id)
	if err != nil {
		return nil, err
	}

	// children-finished — every child finished or deleted
	for _, childID := range s.Children {
		if !Exists(sessionDir, childID) {
			continue // deleted = resolved
		}
		child, err := Load(sessionDir, childID)
		if err != nil {
			r.Issues = append(r.Issues, fmt.Sprintf("[children-finished] failed to load child %s: %v", childID, err))
			continue
		}
		if child.Status != StatusFinished {
			r.Issues = append(r.Issues, fmt.Sprintf("[children-finished] child %s is %s (must be finished or deleted)", childID, child.Status))
		}
	}

	// session-objects-confirmed — every object created/modified by this session is confirmed + has impl on disk
	g, err := graph.LoadOrInit(graphPath)
	if err == nil {
		cwd, _ := os.Getwd()
		for objID := range s.Output.GraphDiff.Added.Objects {
			obj, present := g.Objects[objID]
			if !present {
				r.Issues = append(r.Issues, fmt.Sprintf("[session-objects-confirmed] object %s in graphDiff.added.objects but not in current graph", objID))
				continue
			}
			if obj.Status != graph.StatusConfirmed {
				r.Issues = append(r.Issues, fmt.Sprintf("[session-objects-confirmed] object %s status=%s (must be confirmed)", objID, obj.Status))
			}
			if obj.Impl == nil || *obj.Impl == "" {
				r.Issues = append(r.Issues, fmt.Sprintf("[session-objects-confirmed] object %s has no impl set", objID))
			} else if !implFileOK(*obj.Impl, cwd) {
				r.Issues = append(r.Issues, fmt.Sprintf("[impl-on-disk] object %s impl %q missing or empty on disk", objID, *obj.Impl))
			}
		}
	}

	// outputs-aggregated — sessions with children should aggregate outputs (non-empty
	// implementations list). Defensive: this is a soft signal that the agent
	// forgot to call Aggregate before finishing.
	if len(s.Children) > 0 && len(s.Output.Implementations) == 0 {
		r.Issues = append(r.Issues, "[outputs-aggregated] session has children but output.implementations is empty — call session_aggregate before finishing")
	}

	// root-deliver — root sessions deliver a fully-implemented graph. Even
	// if individual sessions' graphDiff captures were missed (e.g. focus
	// was set late), the root cannot finish until every object on the graph
	// is confirmed, has an impl path set, and that file exists & is non-empty.
	// This rule does NOT depend on graphDiff capture; it inspects the graph
	// state directly. Non-root sessions skip this check.
	if s.Parent == "" && err == nil {
		cwd, _ := os.Getwd()
		// Check every object the graph contains, regardless of which session created it.
		for objID, obj := range g.Objects {
			if obj.Status != graph.StatusConfirmed {
				r.Issues = append(r.Issues, fmt.Sprintf("[root-deliver] object %s status=%s (root finish requires every graph object to be confirmed)", objID, obj.Status))
				continue
			}
			if obj.Impl == nil || *obj.Impl == "" {
				r.Issues = append(r.Issues, fmt.Sprintf("[root-deliver] object %s confirmed but no impl path set", objID))
				continue
			}
			if !implFileOK(*obj.Impl, cwd) {
				r.Issues = append(r.Issues, fmt.Sprintf("[root-deliver] object %s impl %q missing or empty on disk", objID, *obj.Impl))
				continue
			}
			// typecalc evidence — every confirmed object on the root graph
			// must have a typecalc-compile/test record on disk. The agent-
			// side `typecalc-use` hook already flags individual merges that
			// skipped evidence, but a root session that ignored or missed
			// those warnings would otherwise still finish. This gate makes
			// the evidence load-bearing: no evidence → no finish.
			if !typecalcEvidenceExistsAt(cwd, objID) {
				r.Issues = append(r.Issues, fmt.Sprintf(
					"[root-deliver] object %s confirmed but no typecalc evidence at .kcpos/typecalc-evidence/%s.json — run typecalc_compile/typecalc_test with object_id=%q before finishing root",
					objID, objID, objID))
			}
		}
		// Also surface attribute orphans-of-truth: an attribute that was
		// declared but never advanced is fine on a non-root, but on root
		// it suggests the type was named and forgotten. Warn (not error).
		// Currently we only error on objects since attributes don't have
		// impl semantics in the same way.
	}

	// checkpoint-pass — checkpoint final verdict (only meaningful when frozen)
	if checkpointPath != "" {
		if c, err := checkpoint.LoadOrInit(checkpointPath); err == nil {
			c.RecomputeSummary()
			if !c.Frozen {
				r.Issues = append(r.Issues, "[checkpoint-pass] checkpoint not frozen yet — freeze and fill before gate-check")
			} else if c.Summary.FinalVerdict != checkpoint.VerdictPass {
				r.Issues = append(r.Issues, fmt.Sprintf("[checkpoint-pass] checkpoint verdict is %s (need PASS)", c.Summary.FinalVerdict))
			}
		}
	}

	if len(r.Issues) > 0 {
		r.Status = "FAIL"
	}
	return r, nil
}

func implFileOK(implPath, cwd string) bool {
	path := implPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Size() > 0
}

// typecalcEvidenceExistsAt mirrors the agent-package helper of the same
// name (we duplicate the path constant rather than introduce a circular
// import — agent imports session, not the other way around).
func typecalcEvidenceExistsAt(cwd, objectID string) bool {
	if objectID == "" {
		return false
	}
	path := filepath.Join(cwd, ".kcpos", "typecalc-evidence", objectID+".json")
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Size() > 0
}
