package review

import (
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
)

// IsHTMLDeliverable reports whether the object's impl is an HTML
// deliverable (.html / .htm). v9.3.1: several static checks
// (port-observation-required, value-space-empty, runtime-trace-*)
// only make sense for objects verified via the synthesize+test
// harness. HTML objects use the runtime_smoke path instead — they
// have no port-observation extractor, no value-space-driven test
// cases, no runtime trace. Calls to these checks short-circuit
// when this returns true so HTML review can actually pass.
func IsHTMLDeliverable(obj *graph.Object) bool {
	if obj == nil || obj.Impl == nil || *obj.Impl == "" {
		return false
	}
	lower := strings.ToLower(*obj.Impl)
	return strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".htm")
}

// StaticRuleCodes is the canonical list of every rule StaticCheck
// emits a signal for. v9.3.2: review's aggregator checks every code
// in this list against the report's Coverage(); any code that didn't
// emit Pass/Fail/Skipped is treated as a fail (silent-pass guard).
//
// Keep this in sync with the StaticCheck body. Adding a rule means
// adding both the emit and the code here.
var StaticRuleCodes = []string{
	"object-not-found",
	"effects-empty",
	"intent-empty",
	"impl-missing",
	"impl-on-disk",
	"port-observation-required",
	"port-observation-orphan-key",
	"value-space-empty",
	"value-space-non-canonical-enum",
	"base-evidence-missing",
	"base-evidence-failed",
	"evidence-stale",
	"runtime-trace-stale",
	"spec-missing",
	"spec-stale",
	"defs-must-throw",
	"frags-non-trivial",
	"defs-entity-1to1",
	"frags-content-matches-def",
}

// StaticCheck runs the mechanical (non-LLM) "what must be wrong" filter
// described in the user's design proposal: it judges nothing positively,
// only surfaces structural deficits that make the object provably
// unacceptable.
//
// v9.3.2: returns a core.CheckReport (every rule emits Pass / Fail / Skipped
// explicitly) instead of a bare []core.StaticIssue. The aggregator
// (typecalc.AggregateOK) treats "no emission for a rule" as fail-closed,
// killing the silent-pass class that v9.3 P0 was an instance of. Use
// `report.Issues()` for the human-readable issue list (back-compat
// with the pre-v9.3.2 caller shape).
//
// HTML carve-outs: port-observation-required, value-space-empty,
// value-space-non-canonical-enum, runtime-trace-stale are SKIPPED for
// HTML deliverables (they're test-harness specific). The skip is
// EXPLICIT — the rule registers StatusSkipped with reason="html-branch"
// rather than not emitting at all, so the aggregator sees the rule
// considered.
//
// cwd is the project root used to resolve impl paths. Pass "" to skip
// filesystem-touching checks.
func StaticCheck(cwd string, g *graph.Graph, objID string) core.CheckReport {
	rb := core.NewReportBuilder()
	mkIssue := func(code, where, msg string) core.StaticIssue {
		return core.StaticIssue{Code: code, Where: where, Message: msg}
	}

	obj, ok := g.Objects[objID]
	if !ok {
		rb.Fail("object-not-found", mkIssue("object-not-found", objID,
			fmt.Sprintf("object %q is not in K/graph.json", objID)))
		// Can't run subsequent rules without an object; mark all skipped
		// so the aggregator's expected-rule list still matches.
		for _, code := range StaticRuleCodes {
			if code == "object-not-found" {
				continue
			}
			rb.Skip(code, "object-not-found")
		}
		return rb.Build()
	}
	rb.Pass("object-not-found")
	htmlDeliverable := IsHTMLDeliverable(obj)

	// 1. Effect declaration.
	if len(obj.Produces) == 0 && len(obj.Mutates) == 0 {
		rb.Fail("effects-empty", mkIssue("effects-empty", objID,
			"object declares no effects: produces=[] AND mutates=[] — confirmed objects must produce a fresh value (graph_link_produce) or mutate state in place (graph_link_mutate)"))
	} else {
		rb.Pass("effects-empty")
	}

	// 2. Intent non-empty.
	if obj.Intent == "" {
		rb.Fail("intent-empty", mkIssue("intent-empty", objID, "object has empty intent"))
	} else {
		rb.Pass("intent-empty")
	}

	// 3. Impl resolved (impl-missing) and on-disk (impl-on-disk).
	if obj.Impl == nil || *obj.Impl == "" {
		rb.Fail("impl-missing", mkIssue("impl-missing", objID, "object has no impl path set"))
		rb.Skip("impl-on-disk", "impl-missing")
	} else {
		rb.Pass("impl-missing")
		if cwd == "" {
			rb.Skip("impl-on-disk", "no-cwd")
		} else {
			path := *obj.Impl
			if !filepath.IsAbs(path) {
				path = filepath.Join(cwd, path)
			}
			info, err := os.Stat(path)
			switch {
			case err != nil:
				rb.Fail("impl-on-disk", mkIssue("impl-on-disk", objID,
					fmt.Sprintf("impl %q not found on disk", *obj.Impl)))
			case info.Size() == 0:
				rb.Fail("impl-on-disk", mkIssue("impl-on-disk", objID,
					fmt.Sprintf("impl %q is empty", *obj.Impl)))
			default:
				rb.Pass("impl-on-disk")
			}
		}
	}

	// 4.5 D2: port_observation must cover every produced / mutated port.
	//     Without it the harness has no defined way to read the port at
	//     runtime — historically that meant guessing globalThis[port],
	//     which only worked for one impl style. Confirmed objects MUST
	//     declare an extractor per port (or "side_effect" for ports
	//     that genuinely have no observable runtime value, in which
	//     case typecalc_waive will be needed for the test step).
	// v9.3.1: portObservation REQUIREMENT is consumed by the
	// synthesize+test harness. HTML deliverables verify via runtime_smoke
	// (browser) — harness never runs — so "you must set portObservation"
	// becomes noise that blocks review. Skip the "required" emission
	// for HTML. The orphan-key check (below, outside this guard) still
	// runs because it catches a real consistency bug (agent wrote a key
	// that doesn't match a produces port) regardless of which
	// verification branch is used.
	need := append([]string{}, obj.Produces...)
	need = append(need, obj.Mutates...)
	needSet := map[string]bool{}
	for _, p := range need {
		needSet[p] = true
	}
	if htmlDeliverable {
		rb.Skip("port-observation-required", "html-branch")
	} else {
		var portFails []core.StaticIssue
		for _, port := range need {
			if obj.PortObservation == nil {
				portFails = append(portFails, mkIssue("port-observation-required", objID, fmt.Sprintf(
					"object has no portObservation; declare how each port reads at runtime via graph_merge_object id=%q patch='{\"portObservation\":{\"<port>\":\"<extractor>\"}}'",
					objID)))
				break
			}
			if _, ok := obj.PortObservation[port]; !ok {
				portFails = append(portFails, mkIssue("port-observation-required", objID, fmt.Sprintf(
					"port %q has no extractor — add to portObservation (allowed: \"global\", \"return.<path>\", \"args.<n>.<path>\", \"side_effect\")",
					port)))
			}
		}
		if len(portFails) > 0 {
			rb.Fail("port-observation-required", portFails...)
		} else {
			rb.Pass("port-observation-required")
		}
	}

	// 4.6 F (v9.0.1): port-observation-orphan-key. Every KEY in
	//     portObservation must be a declared output (produces ∪
	//     mutates). v9.0 pong-01 burned 70 minutes on the inverse
	//     blindspot: the agent wrote portObservation keys in
	//     camelCase ("gameStatus") while the attribute id was
	//     snake_case ("game_status"). port-observation-required
	//     fired correctly ("game_status missing extractor"), but
	//     the orphan key sat there silently and the harness
	//     returned undefined for the lookup — agent went down 3
	//     wrong inference paths before stumbling on the real cause.
	//     Catching it here turns a silent ~5-call retry loop into a
	//     write-time error with a concrete suggestion.
	// v9.3.1: orphan-key still runs for HTML because it catches a
	// consistency bug regardless of verification branch.
	var orphanFails []core.StaticIssue
	for key := range obj.PortObservation {
		if needSet[key] {
			continue
		}
		msg := fmt.Sprintf(
			"portObservation key %q is not in this object's outputs (produces ∪ mutates = [%s]) — the harness will silently return undefined for this key",
			key, strings.Join(need, ", "))
		if guess := suggestPortKey(key, need); guess != "" {
			msg += fmt.Sprintf("; did you mean %q? (graph attribute IDs are snake_case; portObservation KEYS must match those IDs, only the EXTRACTOR value tracks the JS-side identifier — e.g. portObservation:{%q:\"return.%s\"})",
				guess, guess, key)
		}
		orphanFails = append(orphanFails, mkIssue("port-observation-orphan-key", objID, msg))
	}
	if len(orphanFails) > 0 {
		rb.Fail("port-observation-orphan-key", orphanFails...)
	} else {
		rb.Pass("port-observation-orphan-key")
	}

	// 4. ValueSpace on every produced / mutated attribute. An attribute
	//    that's "confirmed" but whose valueSpace is still null means the
	//    autovalue-backfill step from CLAUDE.md was skipped.
	//
	// v9.3.1: skip for HTML deliverables. Non-HTML objects need valueSpace
	// at chain time because synthesize_tests uses it to generate test
	// cases. HTML objects verify via runtime_smoke (browser), which
	// doesn't read valueSpace — so missing valueSpace mid-chain isn't
	// load-bearing. The gate's [attrs-backfilled] rule still enforces
	// the backfill at root-finish, so the structural requirement is
	// preserved, just deferred. Without this skip, every HTML object's
	// review hit value-space-empty on every produced attribute (v93-05
	// retro: 3 of 5 staticIssues per object), preventing any HTML
	// project from passing review.
	if htmlDeliverable {
		rb.Skip("value-space-empty", "html-branch")
		rb.Skip("value-space-non-canonical-enum", "html-branch")
	} else {
		var valueSpaceEmptyFails []core.StaticIssue
		var enumFails []core.StaticIssue
		for _, attrID := range append([]string{}, append(obj.Produces, obj.Mutates...)...) {
			a, ok := g.Attributes[attrID]
			if !ok {
				continue // already covered by reference-integrity in checker
			}
			if a.ValueSpace == nil || len(a.ValueSpace) == 0 {
				valueSpaceEmptyFails = append(valueSpaceEmptyFails, mkIssue("value-space-empty", attrID, fmt.Sprintf(
					"attribute %s has no valueSpace declared — confirmed attributes must record their value structure (graph_merge_attribute id=%q patch='{\"valueSpace\":...}')",
					attrID, attrID)))
			} else if t, ok := a.ValueSpace["type"].(string); ok && t == "enum" {
				// 2026-05-11 v8.7 — one-shot schema hint instead of per-value
				// runtime-type-mismatch noise. type="enum" isn't canonical
				// JSON-Schema; canonical is {type:"string", enum:[...]} or
				// just {enum:[...]}.
				enumFails = append(enumFails, mkIssue("value-space-non-canonical-enum", attrID, fmt.Sprintf(
					"attribute %s has valueSpace.type=\"enum\" which is not a JSON-Schema type — prefer {type:\"string\", enum:[\"v1\",\"v2\"]} or just {enum:[\"v1\",\"v2\"]}; the runtime check accepts the non-canonical form via the values/enum list but the canonical form clears this hint",
					attrID)))
			}
		}
		if len(valueSpaceEmptyFails) > 0 {
			rb.Fail("value-space-empty", valueSpaceEmptyFails...)
		} else {
			rb.Pass("value-space-empty")
		}
		if len(enumFails) > 0 {
			rb.Fail("value-space-non-canonical-enum", enumFails...)
		} else {
			rb.Pass("value-space-non-canonical-enum")
		}
	}

	// 5. Compile/test evidence. The accepted record only makes sense on
	//    top of a passing test (compile alone for non-testable langs).
	rec, hasRec := readBaseEvidence(cwd, objID)
	if !hasRec {
		rb.Fail("base-evidence-missing", mkIssue("base-evidence-missing", objID, fmt.Sprintf(
			"no compile/test evidence at %s — run typecalc_compile or typecalc_test with object_id=%q before review",
			filepath.Join(core.EvidenceDir, objID+".json"), objID)))
		rb.Skip("base-evidence-failed", "no-base-evidence")
	} else {
		rb.Pass("base-evidence-missing")
		if !rec.OK && rec.Kind != "insufficient" {
			// kind=insufficient legitimately has ok=false — it's a "we
			// can't verify" signal, not a "test failed" signal.
			rb.Fail("base-evidence-failed", mkIssue("base-evidence-failed", objID,
				"latest compile/test evidence has ok=false — fix the underlying failure before review"))
		} else {
			rb.Pass("base-evidence-failed")
		}
	}

	// 5.5 D3: evidence-stale — if the current impl hash differs from
	//         what the evidence recorded, the evidence is invalid.
	//         The ONLY fix is to re-run typecalc_compile or
	//         typecalc_test (which captures the new hash). Any attempt
	//         to make the gate pass without re-running fails.
	// v9.0.2: per-object SymbolHash takes precedence over file-level
	// SourceHash. Single-file HTML projects use SymbolHash so editing
	// one object's function doesn't invalidate all other objects'
	// evidence (4.3 spec-stale storm). Fall back to SourceHash when
	// SymbolHash isn't available (non-HTML impl, symbol extraction
	// failure, evidence from a pre-v9.0.2 run).
	// v10: when implContent is set, the chain reads from graph (not files),
	// so evidence-stale doesn't apply — skip it.
	switch {
	case obj.ImplContent != "":
		rb.Skip("evidence-stale", "v10-impl-content")
	case obj.Impl == nil || *obj.Impl == "":
		rb.Skip("evidence-stale", "impl-missing")
	case rec == nil:
		rb.Skip("evidence-stale", "no-base-evidence")
	default:
		path := *obj.Impl
		if cwd != "" && !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		body, ferr := os.ReadFile(path)
		if ferr != nil {
			rb.Skip("evidence-stale", "impl-not-readable")
		} else {
			fileHash := core.HashSource(string(body))
			b, hasBundle := core.ReadBundle(objID)
			stored := rec.ImplHash
			current := fileHash
			scope := "file"
			if hasBundle && b.SymbolHash != "" {
				symbol := obj.ImplSymbol
				if symbol == "" {
					symbol = objID
				}
				if curSymbolHash, ok := core.SymbolFragmentHash(string(body), *obj.Impl, symbol); ok {
					stored = b.SymbolHash
					current = curSymbolHash
					scope = "symbol"
				}
			}
			if stored != "" && stored != current {
				rb.Fail("evidence-stale", mkIssue("evidence-stale", objID, fmt.Sprintf(
					"compile/test evidence is for %s hash %s but the current impl %s hash is %s — re-run typecalc_compile or typecalc_test to refresh",
					scope, shortHash(stored), scope, shortHash(current))))
			} else {
				rb.Pass("evidence-stale")
			}
		}
	}

	// 5.6 D3: runtime-trace-stale. v9.3.1: HTML deliverables don't produce
	// runtime traces (chain skips synthesize+test); skip explicitly.
	if htmlDeliverable {
		rb.Skip("runtime-trace-stale", "html-branch")
	} else {
		t, hasTrace := core.ReadRuntimeTrace(objID)
		switch {
		case !hasTrace:
			rb.Skip("runtime-trace-stale", "no-trace")
		case obj.Impl == nil || *obj.Impl == "":
			rb.Skip("runtime-trace-stale", "impl-missing")
		default:
			path := *obj.Impl
			if cwd != "" && !filepath.IsAbs(path) {
				path = filepath.Join(cwd, path)
			}
			body, ferr := os.ReadFile(path)
			if ferr != nil {
				rb.Skip("runtime-trace-stale", "impl-not-readable")
			} else {
				currentHash := core.HashSource(string(body))
				if t.ImplHash != "" && t.ImplHash != currentHash {
					rb.Fail("runtime-trace-stale", mkIssue("runtime-trace-stale", objID, fmt.Sprintf(
						"runtime trace was recorded for impl hash %s but current impl is %s — re-run typecalc_test (the harness will rewrite the trace)",
						shortHash(t.ImplHash), shortHash(currentHash))))
				} else {
					rb.Pass("runtime-trace-stale")
				}
			}
		}
	}

	// 6. Spec description present and not stale.
	// v9.0.2: stale = the per-object fragment hash drift if SymbolHash
	// is recorded, else fall back to whole-file SourceHash drift. This
	// is the staleness-storm fix from 4.3 — single-file HTML projects
	// won't re-describe every object on every edit.
	spec, specOK := core.ReadSpec(objID)
	if !specOK {
		rb.Fail("spec-missing", mkIssue("spec-missing", objID, fmt.Sprintf(
			"no auto-generated description at %s — call typecalc_describe object_id=%q first",
			core.SpecEvidencePath(objID), objID)))
		rb.Skip("spec-stale", "spec-missing")
	} else {
		rb.Pass("spec-missing")
		// v10: implContent means chain reads impl from graph, not from file.
		// Spec staleness (does description still match impl?) is irrelevant
		// in v10 mode because evidence comparison uses content hash.
		if obj.ImplContent != "" {
			rb.Skip("spec-stale", "v10-impl-content")
		} else if obj.Impl == nil {
			rb.Skip("spec-stale", "impl-missing")
		} else {
			path := *obj.Impl
			if !filepath.IsAbs(path) && cwd != "" {
				path = filepath.Join(cwd, path)
			}
			body, ferr := os.ReadFile(path)
			if ferr != nil {
				rb.Skip("spec-stale", "impl-not-readable")
			} else {
				stale := false
				if spec.SymbolHash != "" {
					symbol := obj.ImplSymbol
					if symbol == "" {
						symbol = objID
					}
					if curSym, ok := core.SymbolFragmentHash(string(body), *obj.Impl, symbol); ok {
						stale = curSym != spec.SymbolHash
					} else {
						stale = core.HashSource(string(body)) != spec.SourceHash
					}
				} else {
					stale = core.HashSource(string(body)) != spec.SourceHash
				}
				if stale {
					rb.Fail("spec-stale", mkIssue("spec-stale", objID,
						"description was generated against an earlier version of impl — re-run typecalc_describe to regenerate"))
				} else {
					rb.Pass("spec-stale")
				}
			}
		}
	}

	// 7. v9.0.5 defs-must-throw — JavaScript signature files at
	//    `<obj.Def>` must NOT contain real implementations. TS defs
	//    use declaration-only syntax that can't carry executable
	//    bodies; JS defs HAVE to give every function a body (parse
	//    error otherwise), so the only safe pattern is a stub that
	//    `throw new Error(...)`-s immediately. Without this rule,
	//    agents can write `function Foo(){return 0;}` in defs/ and
	//    use that as fake "impl" evidence (the kcpos chain would
	//    happily compile-pass it). The rule fires when defs/<id>.js
	//    contains any function body whose first non-comment statement
	//    is not `throw`.
	switch {
	case cwd == "":
		rb.Skip("defs-must-throw", "no-cwd")
	case obj.Def == "":
		rb.Skip("defs-must-throw", "no-def")
	default:
		defPath := obj.Def
		if !filepath.IsAbs(defPath) {
			defPath = filepath.Join(cwd, defPath)
		}
		if ext := strings.ToLower(filepath.Ext(defPath)); ext != ".js" {
			rb.Skip("defs-must-throw", "non-js-def")
		} else {
			body, ferr := os.ReadFile(defPath)
			if ferr != nil {
				rb.Skip("defs-must-throw", "def-not-readable")
			} else {
				nonThrow := findNonThrowFunctionBodies(string(body))
				if len(nonThrow) > 0 {
					rb.Fail("defs-must-throw", mkIssue("defs-must-throw", objID, fmt.Sprintf(
						"JS def file %s contains %d function(s) whose body does not immediately `throw new Error(...)`: %s — defs are contract-only stubs in JS (TS uses declaration syntax instead). Replace each body with `throw new Error(\"<name>: contract-only; implement in K/frags/<ObjectId>.js\")` so the def cannot be mistaken for a real implementation.",
						obj.Def, len(nonThrow), strings.Join(nonThrow, ", "))))
				} else {
					rb.Pass("defs-must-throw")
				}
			}
		}
	}

	// 8. v9.0.5 frags-non-trivial — the fragment file an object
	//    declares via ImplFragment must contain a non-trivial body for
	//    each function it defines. Trivial = body is one line of
	//    `return <literal>` / empty / pure-return-of-arg with no
	//    branching, no loops, no side effects. The check is approximate
	//    but catches the common "agent wrote `function Foo(){return 0;}`
	//    in frags/ to pass compile" cheat. Real implementations have
	//    AT LEAST one `if` / `for` / `while` / multi-statement body or
	//    a `return` with a non-literal expression.
	if obj.ImplFragment == nil || *obj.ImplFragment == "" {
		rb.Skip("frags-non-trivial", "no-implFragment")
	} else {
		fragPath := *obj.ImplFragment
		if cwd != "" && !filepath.IsAbs(fragPath) {
			fragPath = filepath.Join(cwd, fragPath)
		}
		body, ferr := os.ReadFile(fragPath)
		if ferr != nil {
			rb.Skip("frags-non-trivial", "fragment-not-readable")
		} else {
			trivial := findTrivialFunctionBodies(string(body))
			if len(trivial) > 0 {
				rb.Fail("frags-non-trivial", mkIssue("frags-non-trivial", objID, fmt.Sprintf(
					"fragment file %s contains %d function(s) with a trivial body: %s — agents must not satisfy the frags requirement with `return 0` / `return {}` stubs. Implement the real logic per the def's @param / @returns / @example.",
					*obj.ImplFragment, len(trivial), strings.Join(trivial, ", "))))
			} else {
				rb.Pass("frags-non-trivial")
			}
		}
	}

	// 9. v9.0.6 defs-entity-1to1 — every function declared inside
	//    `K/defs/<id>.js` must have a name that is either the object
	//    id itself or its declared ImplSymbol. This blocks the v9.0.5
	//    bypass where an agent writes an unmodeled helper function
	//    inside a def file (so it gets shipped into index.html via
	//    fragment concat but never reaches confirm_object). When the
	//    function set in the def doesn't 1:1 match the graph entity,
	//    declare it as a separate graph object first.
	switch {
	case cwd == "":
		rb.Skip("defs-entity-1to1", "no-cwd")
	case obj.Def == "":
		rb.Skip("defs-entity-1to1", "no-def")
	default:
		defPath := obj.Def
		if !filepath.IsAbs(defPath) {
			defPath = filepath.Join(cwd, defPath)
		}
		if !strings.HasSuffix(strings.ToLower(defPath), ".js") {
			rb.Skip("defs-entity-1to1", "non-js-def")
		} else {
			body, ferr := os.ReadFile(defPath)
			if ferr != nil {
				rb.Skip("defs-entity-1to1", "def-not-readable")
			} else {
				allowed := map[string]bool{objID: true}
				if obj.ImplSymbol != "" {
					allowed[obj.ImplSymbol] = true
				}
				extras := findExtraFunctionNames(string(body), allowed)
				if len(extras) > 0 {
					suffix := ""
					if obj.ImplSymbol != "" {
						suffix = " or " + obj.ImplSymbol
					}
					rb.Fail("defs-entity-1to1", mkIssue("defs-entity-1to1", objID, fmt.Sprintf(
						"def file %s declares function(s) outside the object's id/implSymbol mapping: %s — each function in a JS def must correspond to a graph object (its name must equal %q%s). Either model these as separate graph objects (graph_create_object), or move them out of the def file into a properly-modeled location.",
						obj.Def, strings.Join(extras, ", "), objID, suffix)))
				} else {
					rb.Pass("defs-entity-1to1")
				}
			}
		}
	}

	// 10. v9.0.6 frags-content-matches-def — the set of function
	//     names in `K/frags/<id>.js` must equal the set in
	//     `K/defs/<id>.js`.
	switch {
	case obj.ImplFragment == nil || *obj.ImplFragment == "":
		rb.Skip("frags-content-matches-def", "no-implFragment")
	case cwd == "":
		rb.Skip("frags-content-matches-def", "no-cwd")
	case obj.Def == "":
		rb.Skip("frags-content-matches-def", "no-def")
	default:
		defPath := obj.Def
		fragPath := *obj.ImplFragment
		if !filepath.IsAbs(defPath) {
			defPath = filepath.Join(cwd, defPath)
		}
		if !filepath.IsAbs(fragPath) {
			fragPath = filepath.Join(cwd, fragPath)
		}
		defLow := strings.ToLower(defPath)
		fragLow := strings.ToLower(fragPath)
		if !strings.HasSuffix(defLow, ".js") || !strings.HasSuffix(fragLow, ".js") {
			rb.Skip("frags-content-matches-def", "non-js-pair")
		} else {
			defNames := readFunctionNames(defPath)
			fragNames := readFunctionNames(fragPath)
			if len(defNames) == 0 || len(fragNames) == 0 {
				// Either def or fragment failed to parse / is empty —
				// can't compare meaningfully. Skip rather than silently
				// pass: that's the v9.3.2 principle.
				rb.Skip("frags-content-matches-def", "empty-or-unparseable")
			} else {
				missing := setDiff(defNames, fragNames)
				extra := setDiff(fragNames, defNames)
				var issues []core.StaticIssue
				if len(missing) > 0 {
					issues = append(issues, mkIssue("frags-content-matches-def", objID, fmt.Sprintf(
						"fragment %s is missing %d function(s) declared in def %s: %s — implement every function the def documents, or remove the unused declarations from the def file.",
						*obj.ImplFragment, len(missing), obj.Def, strings.Join(missing, ", "))))
				}
				if len(extra) > 0 {
					issues = append(issues, mkIssue("frags-content-matches-def", objID, fmt.Sprintf(
						"fragment %s declares %d function(s) not in def %s: %s — every function shipped to the deliverable must be documented in the def with @param / @returns / @example. Either add the def entries, or move helpers into another modelled graph object.",
						*obj.ImplFragment, len(extra), obj.Def, strings.Join(extra, ", "))))
				}
				if len(issues) > 0 {
					rb.Fail("frags-content-matches-def", issues...)
				} else {
					rb.Pass("frags-content-matches-def")
				}
			}
		}
	}

	return rb.Build()
}

// findExtraFunctionNames returns the names of function declarations
// in src that are NOT in the allowed set. Used by defs-entity-1to1.
func findExtraFunctionNames(src string, allowed map[string]bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, body := range scanFunctionBodies(src) {
		if allowed[body.name] {
			continue
		}
		if seen[body.name] {
			continue
		}
		seen[body.name] = true
		out = append(out, body.name)
	}
	return out
}

// readFunctionNames returns the set of function declaration names in
// the file at path. Returns nil on read error so caller can decide
// whether to report.
func readFunctionNames(path string) []string {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, fb := range scanFunctionBodies(string(body)) {
		if seen[fb.name] {
			continue
		}
		seen[fb.name] = true
		out = append(out, fb.name)
	}
	return out
}

// setDiff returns elements in `a` that are not in `b`.
func setDiff(a, b []string) []string {
	bSet := map[string]bool{}
	for _, v := range b {
		bSet[v] = true
	}
	var out []string
	for _, v := range a {
		if !bSet[v] {
			out = append(out, v)
		}
	}
	return out
}

// fnDeclRe matches a `function Name(...)` declaration. The argument
// list (m[2]) is captured but unused; m[1] is the function name. We
// use this to walk the source and locate brace-balanced bodies.
var fnDeclRe = regexp.MustCompile(`function\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*\(([^)]*)\)\s*\{`)

// findNonThrowFunctionBodies returns the names of functions whose
// body's first non-comment, non-blank statement is NOT `throw`.
// Used by the defs-must-throw rule.
func findNonThrowFunctionBodies(src string) []string {
	var out []string
	for _, body := range scanFunctionBodies(src) {
		stmt := firstNonCommentStatement(body.body)
		if !strings.HasPrefix(stmt, "throw") {
			out = append(out, body.name)
		}
	}
	return out
}

// findTrivialFunctionBodies returns the names of functions whose body
// is "trivial" by the rule above (single return-of-literal, empty,
// pass-through). Used by the frags-non-trivial rule.
func findTrivialFunctionBodies(src string) []string {
	var out []string
	for _, body := range scanFunctionBodies(src) {
		if isTrivialBody(body.body) {
			out = append(out, body.name)
		}
	}
	return out
}

type fnBody struct {
	name string
	body string // text between the opening { and matching }
}

// scanFunctionBodies parses `function Name(...){...}` declarations and
// returns each one's body text. Naive: doesn't handle method
// definitions inside classes or arrow functions — defs/frags files in
// kcpos are conventionally script-style function declarations, so
// that's the only shape we need to inspect.
func scanFunctionBodies(src string) []fnBody {
	var out []fnBody
	matches := fnDeclRe.FindAllStringSubmatchIndex(src, -1)
	for _, m := range matches {
		name := src[m[2]:m[3]]
		// Body starts at the `{` we matched; find its balanced `}`.
		openIdx := m[1] - 1 // position of `{`
		depth := 0
		end := -1
		for i := openIdx; i < len(src); i++ {
			switch src[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					end = i
					goto found
				}
			}
		}
		continue
	found:
		body := src[openIdx+1 : end]
		out = append(out, fnBody{name: name, body: body})
	}
	return out
}

// firstNonCommentStatement returns the first non-whitespace,
// non-comment line of body (stripped). Empty string if body is all
// whitespace / comments.
func firstNonCommentStatement(body string) string {
	lines := strings.Split(body, "\n")
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*") {
			continue
		}
		return t
	}
	return ""
}

// trivialBodyRe matches function bodies that consist of a single
// `return <literal>` statement (with optional surrounding whitespace).
// Literals checked: number, string, bool, null, undefined, [], {},
// or simple identifier (which catches `return x;` pass-throughs).
var trivialBodyRe = regexp.MustCompile(`^\s*(?:return\s+(?:-?[0-9]+(?:\.[0-9]+)?|"[^"]*"|'[^']*'|true|false|null|undefined|\[\s*\]|\{\s*\}|[A-Za-z_$][A-Za-z0-9_$]*)\s*;?\s*|return\s*;?\s*)$`)

// isTrivialBody returns true when the function body matches one of
// the well-known stub shapes that should not satisfy frags-non-trivial.
func isTrivialBody(body string) bool {
	// Strip comments line-by-line so `// docstring` inside a body
	// doesn't fool the regex.
	var cleaned []string
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*") {
			continue
		}
		cleaned = append(cleaned, t)
	}
	if len(cleaned) == 0 {
		return true // empty body
	}
	if len(cleaned) > 1 {
		return false // multi-statement bodies are non-trivial by length
	}
	return trivialBodyRe.MatchString(cleaned[0])
}

// suggestPortKey returns the best candidate output for an orphan
// portObservation key by comparing case-folded / non-alnum-stripped
// normalizations ("gameStatus" → "gamestatus" matches "game_status").
// Returns "" when nothing is close enough.
func suggestPortKey(orphan string, candidates []string) string {
	target := normalizePortName(orphan)
	if target == "" {
		return ""
	}
	for _, c := range candidates {
		if normalizePortName(c) == target {
			return c
		}
	}
	return ""
}

func normalizePortName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// shortHash truncates a SHA-256 hex string for human-readable error
// messages. Full hashes are 64 chars; 8 is enough to disambiguate.
func shortHash(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// readBaseEvidence is a thin wrapper around core.ReadEvidence (v9.0: bundle
// reader). Kept as a function for the cwd parameter — callers pass cwd
// so they can run static checks against fixture trees that aren't the
// process's working directory. We chdir-in, core.ReadEvidence, chdir-back.
func readBaseEvidence(cwd, objectID string) (*core.EvidenceRecord, bool) {
	if objectID == "" {
		return nil, false
	}
	if cwd == "" || cwd == "." {
		return core.ReadEvidence(objectID)
	}
	prev, err := os.Getwd()
	if err != nil {
		return nil, false
	}
	defer func() { _ = os.Chdir(prev) }()
	if err := os.Chdir(cwd); err != nil {
		return nil, false
	}
	return core.ReadEvidence(objectID)
}
