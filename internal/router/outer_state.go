package router

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/creator915/Koncept_OS/internal/domain/checkpoint"
	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
)

// InferOuterState reads K/sessions/<rootID>.json + K/graph.json +
// K/checkpoint.json and returns the most-advanced OuterType that the
// on-disk artifacts justify. Used by:
//
//  1. The outer Router's startup to figure out where to resume (lets
//     `kcpos run-routed` pick up an interrupted run mid-pipeline
//     without restarting from Task).
//
//  2. Handler unit tests / the typeflow fidelity test to assert that
//     state-as-disk matches state-as-claimed.
//
// The function returns the CURRENT state (what's already done), not
// what to do next. The Router decides "next" by reading
// OuterFlowSuccessors of the current state.
//
// On any I/O / parse error the function returns Outer.Task — the most
// conservative state, meaning "start from scratch". Callers needing a
// strict mode should call the lower-level *Detail helpers.
func InferOuterState(rootSessionID string) string {
	det, _ := InferOuterStateDetail(rootSessionID)
	return det.Type
}

// OuterStateDetail breaks down the inputs that drove the inference,
// for diagnostics and Handler payload construction.
type OuterStateDetail struct {
	Type                string
	RootSessionID       string
	DeclaredObjectIDs   []string
	RemainingObjectIDs  []string // objects still NOT confirmed (used by SomeConfirmed loop)
	ConfirmedObjectIDs  []string
	ArchitectureNonEmpty bool
	AggregateDone       bool
	BuildDeliverable    string // non-empty means H_build produced this path
	CheckpointFrozen    bool
	CheckpointAllFilled bool
	CheckpointVerdict   string
	GateLastVerdict     string // "" until H_gate has run at least once
	SessionStatus       session.Status
}

// InferOuterStateDetail performs the inference and also returns the
// raw inputs. Errors are surfaced only for I/O issues that should
// halt execution; "graph absent" / "session absent" are treated as
// "earlier state", not errors.
func InferOuterStateDetail(rootSessionID string) (OuterStateDetail, error) {
	det := OuterStateDetail{Type: OuterTypeTask, RootSessionID: rootSessionID}

	if rootSessionID == "" {
		return det, nil
	}

	sess, err := persistence.LoadSession(persistence.SessionDefaultDir, rootSessionID)
	if err != nil {
		// Session not yet created — caller is at Outer.Task.
		return det, nil
	}
	det.SessionStatus = sess.Status

	// Architecture set?
	if sess.Output.Architecture != "" {
		det.ArchitectureNonEmpty = true
		det.Type = OuterTypeArchitecture
	}

	// Session output.aggregate-marker?
	det.AggregateDone = inferAggregateDone(sess)

	// Graph existence + object-status tally.
	g, gerr := persistence.LoadGraph(persistence.GraphDefaultPath)
	if gerr != nil || g == nil || len(g.Objects) == 0 {
		// No graph yet — Architecture is the furthest we can be.
		return det, nil
	}
	for id, obj := range g.Objects {
		if obj == nil {
			continue
		}
		det.DeclaredObjectIDs = append(det.DeclaredObjectIDs, id)
		if obj.Status == graph.StatusConfirmed {
			det.ConfirmedObjectIDs = append(det.ConfirmedObjectIDs, id)
		} else {
			det.RemainingObjectIDs = append(det.RemainingObjectIDs, id)
		}
	}

	switch {
	case len(det.ConfirmedObjectIDs) == 0:
		det.Type = OuterTypeGraphDeclared
	case len(det.RemainingObjectIDs) > 0:
		det.Type = OuterTypeSomeConfirmed
	default:
		det.Type = OuterTypeAllConfirmed
	}

	if det.AggregateDone && det.Type == OuterTypeAllConfirmed {
		det.Type = OuterTypeAggregated
	}

	// Build marker — if every object has implFragment + the shared
	// impl file is non-empty on disk, treat as built.
	if det.Type == OuterTypeAggregated && inferBuiltOnDisk(g) {
		det.Type = OuterTypeBuilt
	}

	// Checkpoint. Compute verdict from items directly — Summary on
	// disk is a snapshot but its in-memory recompute would clobber
	// the stored value if items are empty. Trust items as source.
	c, cerr := persistence.LoadCheckpoint(persistence.CheckpointDefaultPath)
	if cerr == nil && c != nil {
		det.CheckpointFrozen = c.Frozen
		det.CheckpointAllFilled = checkpointAllFilled(c)
		det.CheckpointVerdict = inferCheckpointVerdict(c)
		if det.CheckpointFrozen && det.CheckpointAllFilled &&
			(det.Type == OuterTypeBuilt || det.Type == OuterTypeAggregated) {
			det.Type = OuterTypeCheckpointed
		}
	}

	// GatePassed / Finished are only assertable when the session
	// transitions to finished AND the checkpoint summary is PASS.
	if det.SessionStatus == session.StatusFinished {
		if det.CheckpointVerdict == checkpoint.VerdictPass {
			det.Type = OuterTypeFinished
		} else {
			// Finished without PASS = treat as GatePassed (gate ran
			// but a downstream consumer marked finished without rechecking
			// — defensive; should rarely happen with the new outer loop).
			det.Type = OuterTypeGatePassed
		}
	} else if det.CheckpointVerdict == checkpoint.VerdictPass {
		det.Type = OuterTypeGatePassed
	}

	return det, nil
}

// inferAggregateDone heuristic: H_aggregate is the only producer that
// writes BOTH outputs.Implementations (a slice with at least one entry)
// AND output.Tests-or-NewSignatures alongside Architecture, so we use
// that signature. The session_aggregate tool fills these in atomically.
func inferAggregateDone(sess *session.Session) bool {
	if sess == nil {
		return false
	}
	if len(sess.Output.Implementations) == 0 {
		return false
	}
	// Any of these auxiliary aggregates being non-zero indicates
	// session_aggregate has run at least once.
	if len(sess.Output.Tests) > 0 || len(sess.Output.NewSignatures) > 0 ||
		len(sess.Output.NewAttributes) > 0 {
		return true
	}
	// Lone Implementations slice with no other aggregates can also
	// be valid for very small projects; trust it.
	return len(sess.Output.Implementations) > 0
}

// inferBuiltOnDisk reports whether the deliverable file(s) referenced
// by the graph exist and are non-empty. For multi-file projects this
// is satisfied trivially (every confirmed object's impl file exists
// because confirm_object required it).
func inferBuiltOnDisk(g *graph.Graph) bool {
	if g == nil {
		return false
	}
	seen := map[string]bool{}
	for _, obj := range g.Objects {
		if obj == nil || obj.Impl == nil || *obj.Impl == "" {
			continue
		}
		path := *obj.Impl
		if seen[path] {
			continue
		}
		seen[path] = true
		fi, err := os.Stat(path)
		if err != nil || fi.Size() == 0 {
			return false
		}
	}
	return len(seen) > 0
}

// inferCheckpointVerdict derives the verdict from items directly so a
// loaded checkpoint with stored Summary is not clobbered by
// RecomputeSummary's "empty items → PENDING" behavior. Rules mirror
// the checkpoint package's RecomputeSummary contract:
//   - !Frozen                        → PENDING (unless caller pre-set Summary on an empty-items fixture)
//   - Frozen + every must filled     → PASS
//   - Frozen + at least one unfilled → FAIL
//
// Empty-items fixtures with a manually-set Summary.FinalVerdict are
// honoured (resume scenarios and test fixtures both use this shape).
func inferCheckpointVerdict(c *checkpoint.Checkpoint) string {
	if c == nil {
		return ""
	}
	if len(c.Items) == 0 && c.Summary.FinalVerdict != "" {
		return c.Summary.FinalVerdict
	}
	if !c.Frozen {
		return checkpoint.VerdictPending
	}
	if checkpointAllFilled(c) {
		return checkpoint.VerdictPass
	}
	return checkpoint.VerdictFail
}

// checkpointAllFilled reports whether every MUST item has a non-empty
// codeProof. Mirrors the rule the gate enforces.
func checkpointAllFilled(c *checkpoint.Checkpoint) bool {
	if c == nil || len(c.Items) == 0 {
		return false
	}
	for i := range c.Items {
		it := &c.Items[i]
		if it.Severity != "must" {
			continue
		}
		if !it.Filled() {
			return false
		}
	}
	return true
}

// MarshalPayload is a small helper for Handlers building the next
// TypedValue. It produces a TypedValue with the given Type and the
// payload marshalled as Content. Mirrors NewTypedValue but with a
// shorter call site for the common (Type, struct{...}) pattern.
func MarshalPayload(typeTag string, payload interface{}) TypedValue {
	if payload == nil {
		return TypedValue{Type: typeTag}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return TypedValue{Type: typeTag, Content: json.RawMessage(fmt.Sprintf(`{"_marshal_error":%q}`, err.Error()))}
	}
	return TypedValue{Type: typeTag, Content: raw}
}
