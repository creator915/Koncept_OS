package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

	// outputs-tests-non-empty (5.1a, refined by Fix 2) — sessions with
	// children should have aggregated tests, BUT only if any of the
	// confirmed objects are in a language with an in-tree test runner.
	// A pure Rust / Java / HTML-without-script project legitimately has
	// no test files (kcpos can't drive those runners), and demanding
	// non-empty output.tests there would be unfair.
	//
	// The check is scoped: read the evidence file of each confirmed
	// object on the graph; if any one has lang in the testable set,
	// require non-empty tests. Otherwise skip the check.
	if len(s.Children) > 0 && len(s.Output.Tests) == 0 {
		if anyConfirmedTestable(graphPath) {
			r.Issues = append(r.Issues, "[outputs-tests-non-empty] session has children and at least one confirmed object is in a testable language, but output.tests is empty — call typecalc_test object_id=<id> for each testable confirmed object before session_aggregate")
		}
	}

	// architecture-non-empty (Fix 4) — root sessions must have an
	// Architecture description set. CLAUDE.md §5.4 path A: "even if a
	// one-shot implementation, first list sub-modules and intermediate
	// variables". The pre-fix run had no Architecture step at all (problem
	// 6 in KonceptOS_kcpos_analysis.md). Now session_set_architecture
	// is the canonical writer; the gate refuses root finish without it.
	if s.Parent == "" {
		if strings.TrimSpace(s.Output.Architecture) == "" {
			r.Issues = append(r.Issues, "[architecture-non-empty] root session has empty output.architecture — call session_set_architecture id="+id+" description=<markdown listing sub-modules + intermediate variables> before finishing")
		}
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
			// produces-or-mutates-non-empty (5.1d + 5.3) — a confirmed
			// object must EITHER produce at least one attribute (fresh
			// output) OR mutate at least one (in-place write). The agent
			// has been observed to remove `produces` edges to break
			// cycles, leaving the object claiming no effects at all;
			// this rule blocks that. With mutates now available, the
			// agent should re-link as graph_link_mutate instead of just
			// deleting the edge.
			if len(obj.Produces) == 0 && len(obj.Mutates) == 0 {
				r.Issues = append(r.Issues, fmt.Sprintf(
					"[produces-or-mutates-non-empty] confirmed object %s has produces=[] AND mutates=[]; "+
						"a confirmed function must declare at least one effect — use graph_link_produce for fresh output OR graph_link_mutate for in-place mutation",
					objID))
			}
			// typecalc evidence — every confirmed object on the root graph
			// must have a passing typecalc-compile/test record on disk. The
			// hook already flags individual merges that skipped evidence;
			// the gate makes this load-bearing for finish.
			ev, ok := readEvidence(cwd, objID)
			if !ok {
				r.Issues = append(r.Issues, fmt.Sprintf(
					"[root-deliver] object %s confirmed but no typecalc evidence at .kcpos/typecalc-evidence/%s.json — run typecalc_compile/typecalc_test with object_id=%q before finishing root",
					objID, objID, objID))
				continue
			}
			// D4: obstacle evidence — agent has explicitly given up on
			// automated convergence for this object. Gate refuses
			// unless paired with a waiver (which captures the human's
			// decision to accept).
			obstaclePath := filepath.Join(cwd, ".kcpos", "typecalc-evidence", objID+".obstacle.json")
			waiverPath := filepath.Join(cwd, ".kcpos", "typecalc-evidence", objID+".waiver.json")
			hasObstacle := false
			hasWaiver := false
			if _, statErr := os.Stat(obstaclePath); statErr == nil {
				hasObstacle = true
			}
			if _, statErr := os.Stat(waiverPath); statErr == nil {
				hasWaiver = true
			}
			if hasObstacle && !hasWaiver {
				r.Issues = append(r.Issues, fmt.Sprintf(
					"[obstacle-needs-waiver] object %s has an obstacle record at .kcpos/typecalc-evidence/%s.obstacle.json — human review required. Resolve by deleting the obstacle file (after fixing the underlying issue) OR by recording typecalc_waive with reason explaining out-of-band acceptance.",
					objID, objID))
			}
			// 2026-05-09 v8.5 — obstacle+waiver as test-evidence
			// substitute. Previously the gate only honored
			// (obstacle, waiver) for `kind=insufficient`. The v8 batch
			// (pong-01) showed agents legitimately reach "tests run
			// but a few cases fail for harness-mock reasons" and have
			// no escape: ev.OK=false on `kind=test` was a hard fail,
			// no matter how thorough the obstacle/waiver pair was.
			// Now: if both an obstacle.json AND a waiver.json exist,
			// AND the waiver is non-trivial (the typecalc_waive tool
			// already enforces ≥30 chars + no hand-wavy phrases), the
			// gate treats the object as having structurally-acceptable
			// evidence and skips the OK/kind/insufficient branches.
			// The accepted-evidence-required check still runs after,
			// so review must still pass — this carve-out only relaxes
			// the kind=test ok=true demand, NOT the reasonableness layer.
			passViaWaiver := hasObstacle && hasWaiver
			_ = passViaWaiver

			// D1: kind=insufficient is the "I cannot verify this"
			// response from the lang invokers. It's NOT a fail — it's
			// a signal that mechanical verification is impossible for
			// this language/situation. The gate accepts it ONLY when a
			// matching waiver evidence exists (typecalc_waive). Without
			// the waiver, this is a structural failure: human decision
			// required before this object can be confirmed.
			if ev.Kind == "insufficient" {
				if !hasWaiver {
					r.Issues = append(r.Issues, fmt.Sprintf(
						"[insufficient-needs-waiver] object %s has kind=insufficient evidence (kcpos cannot mechanically verify language %q) and no waiver at .kcpos/typecalc-evidence/%s.waiver.json — call typecalc_waive object_id=%q reason=<specific out-of-band verification plan>",
						objID, ev.Lang, objID, objID))
				}
			} else if !ev.OK {
				// typecalc-evidence-passing (5.1c) — existence isn't enough;
				// the recorded run must have ok=true. v8.5: an
				// obstacle+waiver pair is an acceptable substitute —
				// agent has explicitly admitted "tests fail for
				// structural reasons, here's manual verification" and
				// the human-equivalent check has been recorded. Without
				// the pair, demand a clean ok=true.
				if !passViaWaiver {
					r.Issues = append(r.Issues, fmt.Sprintf(
						"[typecalc-evidence-passing] object %s evidence file records ok=false — re-run typecalc_compile/typecalc_test until it passes, OR escalate via typecalc_obstacle + typecalc_waive describing why mechanical verification is structurally infeasible",
						objID))
					continue
				}
			} else if requiresTestEvidence(ev.Lang) && ev.Kind != "test" {
				// For languages whose test runner kcpos can drive
				// (Go / TS / JS / Python / HTML), confirmed objects
				// need kind=test evidence, not just kind=compile.
				// v8.5: obstacle+waiver also satisfies this — the
				// agent demonstrated they tried and the harness/code
				// pairing is structurally not testable in this form.
				if !passViaWaiver {
					r.Issues = append(r.Issues, fmt.Sprintf(
						"[typecalc-test-required] object %s has only compile evidence (kind=%q) — language %q has a test runner; run typecalc_test with object_id=%q to attest a passing test, OR escalate via typecalc_obstacle + typecalc_waive describing why this object cannot be tested",
						objID, ev.Kind, ev.Lang, objID))
				}
			} else if !requiresTestEvidence(ev.Lang) && ev.Kind == "compile" {
				// D1: For non-testable langs (Rust / Java / HTML),
				// kind=compile USED to be accepted as fallback. After
				// D1, compile alone is no longer enough — those langs
				// must produce kind=insufficient + waiver, not slip
				// through on a compile pass.
				//
				// v8.6 — obstacle+waiver also satisfies this. The
				// pong-03 v8.5 case wrote 4/4 kind=compile + waiver
				// (synthesizer returned CANNOT_SYNTHESIZE for some
				// objects → no test ever ran, but compile succeeded
				// and the agent paired with obstacle+waiver). With
				// the carve-out, that pattern is now a legitimate
				// finish path.
				if !passViaWaiver {
					r.Issues = append(r.Issues, fmt.Sprintf(
						"[compile-not-enough] object %s has only kind=compile evidence for non-testable language %q — kcpos no longer accepts compile-only as proof. Either restructure the impl into a testable language (kind=test) or escalate via typecalc_obstacle + typecalc_waive describing why this object cannot produce test evidence.",
						objID, ev.Lang))
				}
			}
			// accepted-evidence-required — every confirmed object must
			// also pass the two-tier acceptance check (typecalc_review:
			// static structural filter + LLM reasonableness review).
			// Compile/test evidence proves the code runs; the accepted
			// evidence proves the code does what intent says it should.
			// Without this, "confirmed" carries no fitness-for-purpose
			// signal — the gate would PASS on a syntactically correct
			// but semantically irrelevant impl.
			//
			// 2026-05-09 v8.6 — obstacle+waiver carve-out completion.
			// v8.5 added the pair as a kind=test substitute but
			// missed this branch: when typecalc_review short-circuits
			// (static/runtime issues exist) it writes acc.OK=false
			// with reasons "static or runtime check produced issues"
			// — review never ran the LLM judge. The v8.5 batch
			// (pong-01, pong-04) had complete obstacle+waiver pairs
			// for harness-mock objects, but acc.OK=false kept the
			// gate FAIL forever. Symmetric treatment: if the agent
			// provided both signals (obstacle + waiver) AND the
			// review file exists (the typecalc_review call DID
			// happen), accept it as resolved — the human-equivalent
			// judgment has been captured in the waiver reason.
			// The missing-accepted branch still demands the call.
			acc, accOK := readAcceptedEvidence(cwd, objID)
			if !accOK {
				r.Issues = append(r.Issues, fmt.Sprintf(
					"[accepted-evidence-required] object %s confirmed but no review evidence at .kcpos/typecalc-evidence/%s.accepted.json — run typecalc_describe + typecalc_review with object_id=%q before finishing root",
					objID, objID, objID))
				continue
			}
			if !acc.OK && !passViaWaiver {
				reasons := strings.Join(acc.Reasonableness.Reasons, "; ")
				if reasons == "" {
					reasons = "(no reason recorded)"
				}
				r.Issues = append(r.Issues, fmt.Sprintf(
					"[accepted-evidence-required] object %s review verdict failed: %s — fix and re-run typecalc_review, OR escalate via typecalc_obstacle + typecalc_waive describing why the review issues are not code defects",
					objID, reasons))
			}
		}
		// attrs-backfilled (5.1b) — every attribute produced by a confirmed
		// object must itself be in status=confirmed. The "自下而上回填"
		// principle: when an object's implementation succeeds, the
		// attributes it produces have their value space confirmed. If the
		// object is confirmed but the attribute is still declared, the
		// agent skipped the backfill step.
		producedByConfirmed := map[string]string{} // attrID → producing/mutating objID
		for objID, obj := range g.Objects {
			if obj.Status != graph.StatusConfirmed {
				continue
			}
			for _, attr := range obj.Produces {
				if _, seen := producedByConfirmed[attr]; !seen {
					producedByConfirmed[attr] = objID
				}
			}
			// An attribute mutated by a confirmed object also needs its
			// value space confirmed — the mutation operation is now part
			// of the confirmed surface.
			for _, attr := range obj.Mutates {
				if _, seen := producedByConfirmed[attr]; !seen {
					producedByConfirmed[attr] = objID
				}
			}
		}
		for attrID, producer := range producedByConfirmed {
			a, ok := g.Attributes[attrID]
			if !ok {
				continue
			}
			if a.Status != graph.StatusConfirmed {
				r.Issues = append(r.Issues, fmt.Sprintf(
					"[attrs-backfilled] attribute %s is produced by confirmed object %s but is still status=%s — confirmed objects backfill their produced attributes (graph_merge_attribute id=%q patch='{\"status\":\"confirmed\",\"valueSpace\":...}'); see CLAUDE.md \"自下而上回填\"",
					attrID, producer, a.Status, attrID))
			}
		}
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

// evidenceRecord mirrors the JSON layout written by typecalc_compile /
// typecalc_test (see internal/tools/typecalc.go recordTypecalcEvidence).
// Kept private to this file — the canonical writer is tools/typecalc.go.
type evidenceRecord struct {
	ObjectID  string `json:"objectId"`
	Kind      string `json:"kind"` // "compile" | "test"
	Lang      string `json:"lang"`
	OK        bool   `json:"ok"`
	Timestamp string `json:"timestamp"`
}

// readEvidence loads .kcpos/typecalc-evidence/<objectID>.json. Returns
// (record, true) on success; (zero, false) if the file is missing or
// malformed. Callers should prefer this over typecalcEvidenceExistsAt
// when they need to inspect kind/ok.
func readEvidence(cwd, objectID string) (evidenceRecord, bool) {
	if objectID == "" {
		return evidenceRecord{}, false
	}
	path := filepath.Join(cwd, ".kcpos", "typecalc-evidence", objectID+".json")
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return evidenceRecord{}, false
	}
	var rec evidenceRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return evidenceRecord{}, false
	}
	return rec, true
}

// typecalcEvidenceExistsAt is preserved for callers that just need the
// existence check. New gate code uses readEvidence to also inspect ok/kind.
func typecalcEvidenceExistsAt(cwd, objectID string) bool {
	_, ok := readEvidence(cwd, objectID)
	return ok
}

// acceptedRecord mirrors typecalc.AcceptedEvidence — kept here to avoid
// the gate package importing tools/typecalc (which would create a cycle
// via the agent layer). The shape MUST stay in sync with the canonical
// writer in internal/typecalc/evidence.go.
type acceptedRecord struct {
	ObjectID       string `json:"objectId"`
	Kind           string `json:"kind"`
	OK             bool   `json:"ok"`
	Reasonableness struct {
		Verdict    string   `json:"verdict"`
		Reasons    []string `json:"reasons"`
		Confidence float64  `json:"confidence"`
	} `json:"reasonableness"`
}

// readAcceptedEvidence loads .kcpos/typecalc-evidence/<objectID>.accepted.json
// — the reviewer verdict written by typecalc_review. Returns (rec, true)
// on success.
func readAcceptedEvidence(cwd, objectID string) (acceptedRecord, bool) {
	if objectID == "" {
		return acceptedRecord{}, false
	}
	path := filepath.Join(cwd, ".kcpos", "typecalc-evidence", objectID+".accepted.json")
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return acceptedRecord{}, false
	}
	var rec acceptedRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return acceptedRecord{}, false
	}
	return rec, true
}

// requiresTestEvidence reports whether the language has an in-tree test
// runner kcpos can drive (so test evidence is achievable). Mirrors the
// language switch in internal/typecalc/test.go TestRunInvoker.
func requiresTestEvidence(lang string) bool {
	switch lang {
	case "Go", "TypeScript", "JavaScript", "Python":
		return true
	}
	return false
}

// anyConfirmedTestable reports whether the graph at graphPath has any
// confirmed object whose typecalc evidence records a testable language.
// Used by the outputs-tests-non-empty gate to scope itself fairly.
func anyConfirmedTestable(graphPath string) bool {
	if graphPath == "" {
		return false
	}
	g, err := graph.LoadOrInit(graphPath)
	if err != nil {
		return false
	}
	cwd, _ := os.Getwd()
	for objID, obj := range g.Objects {
		if obj.Status != graph.StatusConfirmed {
			continue
		}
		ev, ok := readEvidence(cwd, objID)
		if !ok {
			continue
		}
		if requiresTestEvidence(ev.Lang) {
			return true
		}
	}
	return false
}
