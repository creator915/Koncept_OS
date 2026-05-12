package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/creator915/Koncept_OS/internal/checkpoint"
	"github.com/creator915/Koncept_OS/internal/graph"
	"github.com/creator915/Koncept_OS/internal/typecalc"
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
	//
	// 2026-05-11 v8.7 — waiver-flood throttle. The v8.6 batch (pong-05)
	// showed that the v8.5/v8.6 obstacle+waiver carve-outs, while
	// individually justified, can be abused at session scale: pong-05
	// rode 4/4 confirmed objects through obstacle+waiver with reasons
	// that the LLM reviewer had confabulated ("HTML cannot be loaded
	// as ES module") — every object escaped via the same path. To
	// detect this pattern we count waiver-share across the root graph
	// and fail-gate when ≥75% of confirmed objects bypass the test
	// pathway via obstacle+waiver. Below threshold the carve-outs
	// remain a legitimate escape valve (pong-02 1/2, pong-03 2/4 still
	// pass cleanly).
	totalConfirmed := 0
	waiveredConfirmed := 0
	var waiverObjects []string
	if s.Parent == "" && err == nil {
		cwd, _ := os.Getwd()
		// v8.8 refactor: delegate per-object checks to CheckObjectGate
		// so the same logic powers both the root finish gate AND the
		// per-object early-feedback path (the new gate_object tool +
		// graph_merge_object hook). The aggregated per-object issues
		// remain part of the root report; cross-object checks
		// (attrs-backfilled, waiver-flood, checkpoint-pass) follow.
		for objID, obj := range g.Objects {
			issues, info := CheckObjectGate(g, objID, cwd)
			r.Issues = append(r.Issues, issues...)
			// Track confirmed/waiver accounting for the post-loop
			// waiver-flood probe. Only confirmed objects with passing
			// evidence existence count; status-issues are reported
			// already inline.
			if obj.Status == graph.StatusConfirmed && info.HasEvidence {
				totalConfirmed++
				// v9.0.1 G — structural waivers don't count toward
				// flood. HTML-single-file projects legitimately need
				// many of those (DOM I/O, Canvas, side-effect-only
				// objects) and the flood gate would otherwise block
				// a correct project. Only pragmatic-or-empty waivers
				// count.
				if info.PassViaWaiver && info.WaiverKind != typecalc.WaiverKindStructural {
					waiveredConfirmed++
					waiverObjects = append(waiverObjects, objID)
				}
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

		// v8.7 — waiver-flood throttle. Applied only at root finish.
		// Threshold: ≥75% of confirmed objects via obstacle+waiver
		// pair (3/4, 4/5, 6/8, ...). Below 75% the carve-out remains
		// a legitimate escape valve. At/above, the pattern crosses
		// into systematic verification-bypass and requires the agent
		// to demonstrate diversity of obstacle reasons + ideally
		// upgrade some objects to real test evidence before retry.
		// Two confirmed objects with 1 waiver = 50% (below) — still
		// passes. Four confirmed with 3 waivers = 75% — blocks.
		const waiverFloodMin = 4
		if totalConfirmed >= waiverFloodMin && waiveredConfirmed*4 >= totalConfirmed*3 {
			// Quick reason-diversity probe: count distinct obstacle
			// reasons (first 60 chars normalized lowercase). When ≤2
			// distinct reasons cover N≥3 objects, the agent is likely
			// pattern-pasting one confabulation across the session.
			reasons := map[string]int{}
			for _, objID := range waiverObjects {
				if r := readObstacleReason(objID); r != "" {
					key := normalizeReasonKey(r)
					reasons[key]++
				}
			}
			r.Issues = append(r.Issues, fmt.Sprintf(
				"[waiver-flood] %d/%d (%d%%) confirmed objects pass via typecalc_obstacle+typecalc_waive — the carve-out is meant for individually-justified harness-mismatch cases, not systematic verification-bypass. Affected: %s. Resolution: (a) convert at least %d of these to real typecalc_test evidence (look for the v8.7 OUTPUT_PORTS-includes-Mutates fix that unblocks mutates-pattern impls), OR (b) demonstrate reason-diversity (each obstacle.reason should be specifically grounded in this object's signature, not a copy-pasted environmental claim); distinct-reason keys observed: %d",
				waiveredConfirmed, totalConfirmed, (waiveredConfirmed*100)/max1(totalConfirmed),
				strings.Join(waiverObjects, ", "),
				waiveredConfirmed-((totalConfirmed*3)/4-1), // how many to remove from waiver path
				len(reasons),
			))
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

// ObjectGateInfo carries the side-data CheckObjectGate produces that
// the caller (typically the session gate) needs for cross-object
// accounting like waiver-flood detection.
type ObjectGateInfo struct {
	// HasEvidence is true when typecalc evidence (kind=test|compile|
	// insufficient) exists on disk for this object. Used by callers
	// to filter waiver-flood denominator to "objects that actually
	// finished the verification pathway".
	HasEvidence bool
	// PassViaWaiver is true when both obstacle.json and waiver.json
	// exist for this object — the v8.5+ escape pathway. Independent
	// of HasEvidence so callers can detect waiver-only configurations.
	PassViaWaiver bool
	// WaiverKind (v9.0.1) is the discriminator on the waiver section
	// (typecalc.WaiverKindStructural / WaiverKindPragmatic / ""). The
	// session-level waiver-flood probe uses this to skip structural
	// waivers (they're a property of the deliverable, not a bypass).
	WaiverKind string
}

// CheckObjectGate runs the per-object portion of the gate: every
// rule that judges a single object's confirmed status (impl exists,
// evidence present, review accepted, obstacle/waiver pair correctness,
// etc.). Pre-v8.8 this logic lived inline in CheckGate; the extraction
// lets the same rules drive (a) root finish, (b) the gate_object tool
// for explicit per-object queries, and (c) the graph_merge_object
// hook that auto-runs on status=confirmed transitions for early
// feedback.
//
// Cross-object rules (attrs-backfilled, waiver-flood, checkpoint-pass,
// architecture-non-empty) remain in CheckGate — they require the full
// graph context, not a single object.
//
// Returns issues plus side-data for the caller's accounting.
func CheckObjectGate(g *graph.Graph, objID string, cwd string) ([]string, ObjectGateInfo) {
	var issues []string
	var info ObjectGateInfo

	obj, present := g.Objects[objID]
	if !present {
		issues = append(issues, fmt.Sprintf("[root-deliver] object %s not in graph", objID))
		return issues, info
	}
	if obj.Status != graph.StatusConfirmed {
		issues = append(issues, fmt.Sprintf("[root-deliver] object %s status=%s (root finish requires every graph object to be confirmed)", objID, obj.Status))
		return issues, info
	}
	if obj.Impl == nil || *obj.Impl == "" {
		issues = append(issues, fmt.Sprintf("[root-deliver] object %s confirmed but no impl path set", objID))
		return issues, info
	}
	if !implFileOK(*obj.Impl, cwd) {
		issues = append(issues, fmt.Sprintf("[root-deliver] object %s impl %q missing or empty on disk", objID, *obj.Impl))
		return issues, info
	}
	// produces-or-mutates-non-empty (5.1d + 5.3) — confirmed object
	// must declare at least one effect (produce a fresh value OR
	// mutate state in place). Re-link as graph_link_mutate instead
	// of deleting produces edges to break cycles.
	if len(obj.Produces) == 0 && len(obj.Mutates) == 0 {
		issues = append(issues, fmt.Sprintf(
			"[produces-or-mutates-non-empty] confirmed object %s has produces=[] AND mutates=[]; "+
				"a confirmed function must declare at least one effect — use graph_link_produce for fresh output OR graph_link_mutate for in-place mutation",
			objID))
	}

	// v9.0: single ObjectState load replaces the v8.x ad-hoc file probes
	// + readEvidence + readAcceptedEvidence sequence. State carries all
	// flags this rule cluster needs.
	st := typecalc.LoadObjectState(objID, cwd)
	if !st.HasCompileEvidence && !st.HasTestEvidence {
		issues = append(issues, fmt.Sprintf(
			"[root-deliver] object %s confirmed but no typecalc evidence at %s — run typecalc_compile/typecalc_test with object_id=%q before finishing root",
			objID, typecalc.BundlePath(objID), objID))
		return issues, info
	}
	info.HasEvidence = true

	if st.HasObstacle && !st.HasWaiver {
		issues = append(issues, fmt.Sprintf(
			"[obstacle-needs-waiver] object %s has an obstacle record in %s — human review required. Resolve by clearing the obstacle (after fixing the underlying issue) OR by recording typecalc_waive with reason explaining out-of-band acceptance.",
			objID, typecalc.BundlePath(objID)))
	}
	passViaWaiver := st.PassViaWaiver()
	info.PassViaWaiver = passViaWaiver
	info.WaiverKind = st.WaiverKind

	// Pick the most-recent kind for downstream rule selection. v9.0
	// stores compile/test as separate sections; the gate's existing
	// "kind=test required for testable langs" rule still maps cleanly:
	// if the Test section is present we use its Kind, else fall back to
	// Compile.
	kind := ""
	lang := st.Lang
	overallOK := false
	switch {
	case st.HasTestEvidence:
		kind = st.TestKind
		overallOK = st.TestOK
	case st.HasCompileEvidence:
		kind = st.CompileKind
		overallOK = st.CompileOK
	}

	switch {
	case kind == "insufficient":
		if !st.HasWaiver {
			issues = append(issues, fmt.Sprintf(
				"[insufficient-needs-waiver] object %s has kind=insufficient evidence (kcpos cannot mechanically verify language %q) — call typecalc_waive object_id=%q reason=<specific out-of-band verification plan>",
				objID, lang, objID))
		}
	case !overallOK:
		if !passViaWaiver {
			issues = append(issues, fmt.Sprintf(
				"[typecalc-evidence-passing] object %s evidence records ok=false — re-run typecalc_compile/typecalc_test until it passes, OR escalate via typecalc_obstacle + typecalc_waive describing why mechanical verification is structurally infeasible",
				objID))
			return issues, info
		}
	case requiresTestEvidence(lang) && kind != "test":
		if !passViaWaiver {
			issues = append(issues, fmt.Sprintf(
				"[typecalc-test-required] object %s has only compile evidence (kind=%q) — language %q has a test runner; run typecalc_test with object_id=%q to attest a passing test, OR escalate via typecalc_obstacle + typecalc_waive describing why this object cannot be tested",
				objID, kind, lang, objID))
		}
	case !requiresTestEvidence(lang) && kind == "compile":
		if !passViaWaiver {
			issues = append(issues, fmt.Sprintf(
				"[compile-not-enough] object %s has only kind=compile evidence for non-testable language %q — kcpos no longer accepts compile-only as proof. Either restructure the impl into a testable language (kind=test) or escalate via typecalc_obstacle + typecalc_waive describing why this object cannot produce test evidence.",
				objID, lang))
		}
	}

	// accepted-evidence-required — every confirmed object must pass
	// reasonableness review (or substitute via obstacle+waiver).
	if !st.HasAccepted {
		issues = append(issues, fmt.Sprintf(
			"[accepted-evidence-required] object %s confirmed but no review evidence in %s — run typecalc_describe + typecalc_review with object_id=%q before finishing root",
			objID, typecalc.BundlePath(objID), objID))
		return issues, info
	}
	if !st.AcceptedOK && !passViaWaiver {
		// Load the bundle once more for the reasons list (cheap; LRU
		// could be added but a single read is fine on the gate path).
		acc, _ := typecalc.ReadAccepted(objID)
		reasons := ""
		if acc != nil {
			reasons = strings.Join(acc.Reasonableness.Reasons, "; ")
		}
		if reasons == "" {
			reasons = "(no reason recorded)"
		}
		issues = append(issues, fmt.Sprintf(
			"[accepted-evidence-required] object %s review verdict failed: %s — fix and re-run typecalc_review, OR escalate via typecalc_obstacle + typecalc_waive describing why the review issues are not code defects",
			objID, reasons))
	}

	return issues, info
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

// readObstacleReason loads the bundle's Obstacle section reason for
// waiver-flood diversity analysis. Returns "" if missing/unreadable.
// v9.0.1: reads the unified bundle at .kcpos/typecalc/<id>.json (the
// previous v8.x path .kcpos/typecalc-evidence/<id>.obstacle.json was
// stale dead code and silently returned "" for every v9.0 object,
// defeating the diversity probe).
func readObstacleReason(objectID string) string {
	rec, ok := typecalc.ReadObstacle(objectID)
	if !ok || rec == nil {
		return ""
	}
	return rec.Reason
}

// normalizeReasonKey produces a coarse signature of an obstacle reason
// for similarity counting. Lowercased, whitespace-collapsed, first 60
// characters. Two obstacles with the same key are very likely
// pattern-pasted; with different keys, the agent at least varied wording.
func normalizeReasonKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

// max1 returns n when n>=1, else 1. Used as denominator-protection in
// percentage formatting so a zero-confirmed root doesn't divide by 0
// (defensive — the surrounding loop only fires when totalConfirmed >= 4).
func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}


// requiresTestEvidence reports whether the language has an in-tree test
// runner kcpos can drive (so test evidence is achievable). Mirrors the
// language switch in internal/typecalc/test.go TestRunInvoker.
//
// v9.0: the gate's per-object rules pull this from
// typecalc.ObjectState.Lang via CheckObjectGate; this helper stays here
// as the shared definition both old and new code paths can call.
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
		st := typecalc.LoadObjectState(objID, cwd)
		if st.Lang == "" {
			continue
		}
		if requiresTestEvidence(st.Lang) {
			return true
		}
	}
	return false
}
