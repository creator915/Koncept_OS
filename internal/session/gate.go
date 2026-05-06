package session

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/creator915/Koncept_OS/internal/checkpoint"
	"github.com/creator915/Koncept_OS/internal/graph"
)

// GateReport summarizes the result of a §5.1.1 / §5.5 R5 gate check.
type GateReport struct {
	SessionID string   `json:"sessionId"`
	Status    string   `json:"status"` // "PASS" or "FAIL"
	Issues    []string `json:"issues"`
}

// CheckGate verifies the conditions a session must meet before it can be
// marked finished. Per CLAUDE.md §5.1.1 (apply broadly) plus §5.5 R5 (root
// session only). The convergent variant skips gameplayProof requirements
// (CLAUDE.md §5.1.1#7) since they cannot be mechanically verified.
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

	// §5.1.1#4 — every child finished or deleted
	for _, childID := range s.Children {
		if !Exists(sessionDir, childID) {
			continue // deleted = resolved
		}
		child, err := Load(sessionDir, childID)
		if err != nil {
			r.Issues = append(r.Issues, fmt.Sprintf("§5.1.1#4: failed to load child %s: %v", childID, err))
			continue
		}
		if child.Status != StatusFinished {
			r.Issues = append(r.Issues, fmt.Sprintf("§5.1.1#4: child %s is %s (must be finished or deleted)", childID, child.Status))
		}
	}

	// §5.1.1#1+2 — every object created/modified by this session is confirmed + has impl on disk
	g, err := graph.LoadOrInit(graphPath)
	if err == nil {
		cwd, _ := os.Getwd()
		for objID := range s.Output.GraphDiff.Added.Objects {
			obj, present := g.Objects[objID]
			if !present {
				r.Issues = append(r.Issues, fmt.Sprintf("§5.1.1#1: object %s in graphDiff.added.objects but not in current graph", objID))
				continue
			}
			if obj.Status != graph.StatusConfirmed {
				r.Issues = append(r.Issues, fmt.Sprintf("§5.1.1#1: object %s status=%s (must be confirmed)", objID, obj.Status))
			}
			if obj.Impl == nil || *obj.Impl == "" {
				r.Issues = append(r.Issues, fmt.Sprintf("§5.1.1#1: object %s has no impl set", objID))
			} else if !implFileOK(*obj.Impl, cwd) {
				r.Issues = append(r.Issues, fmt.Sprintf("§5.1.1#2: object %s impl %q missing or empty on disk", objID, *obj.Impl))
			}
		}
	}

	// §5.1.1#8 — sessions with children should aggregate outputs (non-empty
	// implementations list). Defensive: this is a soft signal that the agent
	// forgot to call Aggregate before finishing.
	if len(s.Children) > 0 && len(s.Output.Implementations) == 0 {
		r.Issues = append(r.Issues, "§5.1.1#8: session has children but output.implementations is empty — call session_aggregate before finishing")
	}

	// §5.1.1#6 — checkpoint final verdict (only meaningful when frozen)
	if checkpointPath != "" {
		if c, err := checkpoint.LoadOrInit(checkpointPath); err == nil {
			c.RecomputeSummary()
			if !c.Frozen {
				r.Issues = append(r.Issues, "§5.1.1#6: checkpoint not frozen yet — freeze and fill before gate-check")
			} else if c.Summary.FinalVerdict != checkpoint.VerdictPass {
				r.Issues = append(r.Issues, fmt.Sprintf("§5.1.1#6: checkpoint verdict is %s (need PASS)", c.Summary.FinalVerdict))
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
