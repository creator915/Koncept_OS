package workflow

import (
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/creator915/Koncept_OS/internal/domain/checkpoint"
	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
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
	s, err := persistence.LoadSession(sessionDir, id)
	if err != nil {
		return nil, err
	}

	// children-finished — every child finished or deleted
	for _, childID := range s.Children {
		if !persistence.ExistsSession(sessionDir, childID) {
			continue // deleted = resolved
		}
		child, err := persistence.LoadSession(sessionDir, childID)
		if err != nil {
			r.Issues = append(r.Issues, fmt.Sprintf("[children-finished] failed to load child %s: %v", childID, err))
			continue
		}
		if child.Status != session.StatusFinished {
			r.Issues = append(r.Issues, fmt.Sprintf("[children-finished] child %s is %s (must be finished or deleted)", childID, child.Status))
		}
	}

	// session-objects-confirmed — every object created/modified by this session is confirmed + has impl on disk
	g, err := persistence.LoadGraphOrInit(graphPath)
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
	// v9.2 history note: pre-v9.2 a waiver-flood probe ran here to
	// detect "≥75% confirmed objects ride obstacle+waiver" — the
	// canonical fail mode where the agent pattern-pasted one
	// confabulated obstacle reason across every object. With waivers
	// removed entirely there's no flood to detect; every confirmed
	// object now has to clear the per-object gate with real evidence.
	totalConfirmed := 0
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
			// Track confirmed-with-evidence count. v9.2 removed the
			// waiver-flood probe (along with waivers themselves) — every
			// confirmed object now stands on its own real evidence.
			_ = info
			_ = obj
			if obj.Status == graph.StatusConfirmed && info.HasEvidence {
				totalConfirmed++
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

		// v9.2 — waiver-flood probe removed. The whole concept of
		// waivered-confirmed objects is gone; every confirmed object
		// stands on real compile/test/runtime evidence. If a project
		// can't produce real evidence for some object, the per-object
		// gate fails it — there's no "many objects on waiver" failure
		// mode left to detect.
	}

	// checkpoint-pass — checkpoint final verdict (only meaningful when frozen)
	if checkpointPath != "" {
		if c, err := persistence.LoadCheckpointOrInit(checkpointPath); err == nil {
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
// accounting. v9.2 simplified this — the waiver flood probe is gone,
// so only the evidence-presence flag remains.
type ObjectGateInfo struct {
	// HasEvidence is true when typecalc evidence (kind=test|compile|
	// insufficient) exists on disk for this object. Used by the
	// confirmed-object count denominator.
	HasEvidence bool
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
	st := core.LoadObjectState(objID, cwd)
	if !st.HasCompileEvidence && !st.HasTestEvidence {
		issues = append(issues, fmt.Sprintf(
			"[root-deliver] object %s confirmed but no typecalc evidence at %s — run typecalc_compile/typecalc_test with object_id=%q before finishing root",
			objID, core.BundlePath(objID), objID))
		return issues, info
	}
	info.HasEvidence = true

	// v9.2: obstacle/waiver escape paths removed. Gate is now binary
	// pass/fail — every check below is a hard requirement.

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
	default:
		// v9.3.2: neither test nor compile evidence — the earlier
		// HasCompileEvidence || HasTestEvidence guard already returned
		// from this case (root-deliver "no typecalc evidence"). This
		// default exists for documentation: if that guard above is ever
		// removed, the silent-pass case becomes a fail-closed
		// "[unhandled-evidence-shape]" rather than slipping through.
		issues = append(issues, fmt.Sprintf(
			"[unhandled-evidence-shape] object %s has neither HasTestEvidence nor HasCompileEvidence reaching the kind/overallOK selector — kcpos invariant violation, this branch should have early-returned via the no-evidence check above. Report as a kcpos bug.",
			objID))
		return issues, info
	}

	// v9.2 — HTML deliverables are exempted from the typecalc_test
	// branch entirely: their gate is "compile + runtime_smoke", per the
	// D2 decision (Terraria v9.0.6 retro). The vm.Script harness can't
	// reliably exercise browser-only code, so demanding kind=test for
	// HTML produces theater (5/5 instances of the old batch). When the
	// impl is .html, the rule branches below skip; the
	// [runtime-smoke-required] rule (later in this function) is the
	// canonical HTML verification path. Compile evidence for fragments
	// often comes back as lang="JavaScript" because the fragment file
	// is .js — but the OBJECT's deliverable is HTML, so the test-runner
	// rules don't apply.
	isHTMLDeliverable := requiresRuntimeSmoke(lang, *obj.Impl)

	switch {
	case kind == "insufficient":
		issues = append(issues, fmt.Sprintf(
			"[insufficient-not-confirmable] object %s has kind=insufficient evidence — kcpos cannot mechanically verify language %q (no in-tree runner). The waiver escape was removed in v9.2; the only paths forward are: (a) refactor this object into a verifiable language (extract the algorithm into a JS/TS/Go/Python helper kcpos can drive), OR (b) extend kcpos by adding a CompileLanguageInvoker / TestRunInvoker for %q in internal/typecalc/lang/. Hand-wavy 'manual verification' is no longer accepted.",
			objID, lang, lang))
	case !overallOK:
		issues = append(issues, fmt.Sprintf(
			"[typecalc-evidence-passing] object %s evidence records ok=false — fix the impl until typecalc_compile/typecalc_test reports ok=true. There is no waiver escape; the test must actually pass.",
			objID))
		return issues, info
	case requiresTestEvidence(lang) && kind != "test" && !isHTMLDeliverable:
		issues = append(issues, fmt.Sprintf(
			"[typecalc-test-required] object %s has only compile evidence (kind=%q) — language %q has an in-tree test runner; run typecalc_test object_id=%q. If the synthesized tests can't drive this object, declare portObservation so the harness knows how to observe outputs, OR restructure the impl so its effects ARE observable through declared ports.",
			objID, kind, lang, objID))
	case !requiresTestEvidence(lang) && kind == "compile" && !isHTMLDeliverable:
		issues = append(issues, fmt.Sprintf(
			"[compile-not-enough] object %s has only kind=compile evidence for non-testable language %q — kcpos cannot accept compile-only as proof of correctness. Restructure the impl into a language with a test runner (typically JS/TS/Go/Python) so typecalc_test can verify behaviour.",
			objID, lang))
	case isHTMLDeliverable && overallOK:
		// HTML branch: typecalc-test-required and compile-not-enough do
		// not apply (browser smoke is the verification). The
		// [runtime-smoke-required] check below handles HTML-specific
		// requirements. This case exists explicitly so the default
		// branch below treats only true "unhandled" combinations as
		// fail-closed.
	case kind == "test" && overallOK && !isHTMLDeliverable:
		// Non-HTML happy path: test evidence with ok=true and the lang
		// requires testing — all clear.
	case kind == "compile" && overallOK && requiresTestEvidence(lang) && !isHTMLDeliverable:
		// Caught by case requiresTestEvidence above; unreachable but
		// listed for clarity in the truth table.
	default:
		// v9.3.2: previously this branch silently let evidence through
		// when none of the cases matched (e.g. lang outside the testable
		// whitelist AND not HTML AND kind not in expected enum).
		// Conservatively fail-closed: every unhandled combination must
		// be diagnosed and added as an explicit case rather than
		// implicitly passed.
		issues = append(issues, fmt.Sprintf(
			"[unhandled-gate-case] object %s has lang=%q kind=%q overallOK=%v isHTML=%v — no gate rule matched this combination, treating as fail-closed (v9.3.2 silent-pass guard). Either fix the evidence shape (re-run typecalc_compile/typecalc_test) or extend gate.go with a case for this lang/kind.",
			objID, lang, kind, overallOK, isHTMLDeliverable))
	}

	// runtime-smoke-required (v9.1) — HTML deliverables MUST be booted
	// in a real browser. The vm.Script harness used by typecalc_test
	// cannot see browser-only failure modes (ESM exports in non-module
	// <script>, load-order races, canvas all-black). The 2026-05-12
	// Terraria batch retro confirmed this: 4/5 instances passed kcpos
	// gate but black-screened on open. Gate rule fires only for HTML
	// impls — non-HTML languages keep their existing typecalc_test
	// requirement.
	if requiresRuntimeSmoke(lang, *obj.Impl) {
		if !st.HasRuntimeSmoke {
			issues = append(issues, fmt.Sprintf(
				"[runtime-smoke-required] object %s impl is an HTML deliverable but no kind=runtime evidence at %s — run `runtime_smoke object_id=%q` to boot the page in headless Chromium and capture browser-level diagnostics (page errors, console errors, canvas render). If playwright is missing, discover existing installs via bash/find/glob + call runtime_link, or call runtime_install as last resort. There is no waiver escape; the smoke test must actually run.",
				objID, core.BundlePath(objID), objID))
		} else if !st.RuntimeSmokeOK {
			issues = append(issues, fmt.Sprintf(
				"[runtime-smoke-required] object %s runtime_smoke recorded ok=false — fix page-level load failures (uncaught errors, request failures, blank canvas) and re-run runtime_smoke. Read the recorded section at %s for the specific pageErrors / canvas state.",
				objID, core.BundlePath(objID)))
		}
	}

	// accepted-evidence-required — every confirmed object must pass
	// reasonableness review.
	if !st.HasAccepted {
		issues = append(issues, fmt.Sprintf(
			"[accepted-evidence-required] object %s confirmed but no review evidence in %s — run typecalc_describe + typecalc_review with object_id=%q before finishing root",
			objID, core.BundlePath(objID), objID))
		return issues, info
	}
	if !st.AcceptedOK {
		// persistence.Load the bundle once more for the reasons list (cheap; LRU
		// could be added but a single read is fine on the gate path).
		acc, _ := core.ReadAccepted(objID)
		reasons := ""
		if acc != nil {
			reasons = strings.Join(acc.Reasonableness.Reasons, "; ")
		}
		if reasons == "" {
			reasons = "(no reason recorded)"
		}
		issues = append(issues, fmt.Sprintf(
			"[accepted-evidence-required] object %s review verdict failed: %s — fix the impl until typecalc_review reports ok=true. There is no waiver escape; address each review issue at the code level.",
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

// readObstacleReason / normalizeReasonKey / max1 — all removed in v9.2
// along with the waiver-flood probe. Kept this empty block as a tombstone
// for git-blame archaeology: these helpers existed to detect agents
// pattern-pasting one obstacle reason across many objects. With waivers
// gone, there's no pattern to detect.

// requiresTestEvidence reports whether the language has an in-tree test
// runner kcpos can drive (so test evidence is achievable). Mirrors the
// language switch in internal/typecalc/test.go TestRunInvoker.
//
// v9.0: the gate's per-object rules pull this from
// core.ObjectState.Lang via CheckObjectGate; this helper stays here
// as the shared definition both old and new code paths can call.
//
// Note: HTML is intentionally NOT in this list. Per the v9.1 decision
// (Terraria-v9.0.6 retro D2), HTML deliverables need `kind=runtime`
// evidence from runtime_smoke instead of `kind=test` from the vm.Script
// harness. requiresRuntimeSmoke below carries that constraint.
func requiresTestEvidence(lang string) bool {
	switch lang {
	case "Go", "TypeScript", "JavaScript", "Python":
		return true
	}
	return false
}

// requiresRuntimeSmoke reports whether the object needs `kind=runtime`
// (browser smoke) evidence to clear the gate. True iff the impl is an
// HTML deliverable — the only category where typecalc_test cannot
// observe real-browser failure modes.
//
// We check both the declared lang and the file extension because
// language detection can degrade through routing (HTML→JS via the
// HasInlineScript heuristic), and we want the rule to fire reliably on
// any .html / .htm file. False positives (an HTML file used only as a
// fixture) are tolerable — the agent can waive once.
func requiresRuntimeSmoke(lang string, implPath string) bool {
	if lang == "HTML" {
		return true
	}
	lower := strings.ToLower(implPath)
	return strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".htm")
}

// anyConfirmedTestable reports whether the graph at graphPath has any
// confirmed object whose typecalc evidence records a testable language.
// Used by the outputs-tests-non-empty gate to scope itself fairly.
func anyConfirmedTestable(graphPath string) bool {
	if graphPath == "" {
		return false
	}
	g, err := persistence.LoadGraphOrInit(graphPath)
	if err != nil {
		return false
	}
	cwd, _ := os.Getwd()
	for objID, obj := range g.Objects {
		if obj.Status != graph.StatusConfirmed {
			continue
		}
		st := core.LoadObjectState(objID, cwd)
		if st.Lang == "" {
			continue
		}
		if requiresTestEvidence(st.Lang) {
			return true
		}
	}
	return false
}
