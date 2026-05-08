package typecalc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/creator915/Koncept_OS/internal/graph"
)

// StaticCheck runs the mechanical (non-LLM) "what must be wrong" filter
// described in the user's design proposal: it judges nothing positively,
// only surfaces structural deficits that make the object provably
// unacceptable. Examples:
//
//   - confirmed object with no produces and no mutates (declares no effect)
//   - produced/mutated attribute with no valueSpace (was the type ever
//     thought through?)
//   - description hash drift vs current impl (description is stale)
//   - missing test or compile evidence (run typecalc_test first)
//
// The check is a backstop, not a positive judgement. A clean run
// (zero StaticIssue values) means "nothing definitively broken" — it
// does NOT mean "the implementation is correct". That second judgement
// is the reasonableness review's job (review.go).
//
// cwd is the project root used to resolve impl paths. Pass "" to skip
// filesystem-touching checks.
func StaticCheck(cwd string, g *graph.Graph, objID string) []StaticIssue {
	var issues []StaticIssue
	push := func(code, where, msg string) {
		issues = append(issues, StaticIssue{Code: code, Where: where, Message: msg})
	}

	obj, ok := g.Objects[objID]
	if !ok {
		push("object-not-found", objID,
			fmt.Sprintf("object %q is not in K/graph.json", objID))
		return issues
	}

	// 1. Effect declaration. Mirrors the gate's
	//    [produces-or-mutates-non-empty] but fired earlier so the agent
	//    must address it before reaching root-deliver.
	if len(obj.Produces) == 0 && len(obj.Mutates) == 0 {
		push("effects-empty", objID,
			"object declares no effects: produces=[] AND mutates=[] — confirmed objects must produce a fresh value (graph_link_produce) or mutate state in place (graph_link_mutate)")
	}

	// 2. Intent non-empty. A confirmed object without an intent is a hole
	//    in the project's design memory.
	if obj.Intent == "" {
		push("intent-empty", objID, "object has empty intent")
	}

	// 3. Impl resolved and non-empty on disk.
	if obj.Impl == nil || *obj.Impl == "" {
		push("impl-missing", objID, "object has no impl path set")
	} else if cwd != "" {
		path := *obj.Impl
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		info, err := os.Stat(path)
		if err != nil {
			push("impl-on-disk", objID,
				fmt.Sprintf("impl %q not found on disk", *obj.Impl))
		} else if info.Size() == 0 {
			push("impl-on-disk", objID,
				fmt.Sprintf("impl %q is empty", *obj.Impl))
		}
	}

	// 4.5 D2: port_observation must cover every produced / mutated port.
	//     Without it the harness has no defined way to read the port at
	//     runtime — historically that meant guessing globalThis[port],
	//     which only worked for one impl style. Confirmed objects MUST
	//     declare an extractor per port (or "side_effect" for ports
	//     that genuinely have no observable runtime value, in which
	//     case typecalc_waive will be needed for the test step).
	{
		need := append([]string{}, obj.Produces...)
		need = append(need, obj.Mutates...)
		for _, port := range need {
			if obj.PortObservation == nil {
				push("port-observation-required", objID, fmt.Sprintf(
					"object has no portObservation; declare how each port reads at runtime via graph_merge_object id=%q patch='{\"portObservation\":{\"<port>\":\"<extractor>\"}}'",
					objID))
				break
			}
			if _, ok := obj.PortObservation[port]; !ok {
				push("port-observation-required", objID, fmt.Sprintf(
					"port %q has no extractor — add to portObservation (allowed: \"global\", \"return.<path>\", \"args.<n>.<path>\", \"side_effect\")",
					port))
			}
		}
	}

	// 4. ValueSpace on every produced / mutated attribute. An attribute
	//    that's "confirmed" but whose valueSpace is still null means the
	//    autovalue-backfill step from CLAUDE.md was skipped.
	for _, attrID := range append([]string{}, append(obj.Produces, obj.Mutates...)...) {
		a, ok := g.Attributes[attrID]
		if !ok {
			continue // already covered by reference-integrity in checker
		}
		if a.ValueSpace == nil || len(a.ValueSpace) == 0 {
			push("value-space-empty", attrID, fmt.Sprintf(
				"attribute %s has no valueSpace declared — confirmed attributes must record their value structure (graph_merge_attribute id=%q patch='{\"valueSpace\":...}')",
				attrID, attrID))
		}
	}

	// 5. Compile/test evidence. The accepted record only makes sense on
	//    top of a passing test (compile alone for non-testable langs).
	rec, ok := readBaseEvidence(cwd, objID)
	if !ok {
		push("base-evidence-missing", objID, fmt.Sprintf(
			"no compile/test evidence at %s — run typecalc_compile or typecalc_test with object_id=%q before review",
			filepath.Join(EvidenceDir, objID+".json"), objID))
	} else if !rec.OK && rec.Kind != "insufficient" {
		// kind=insufficient legitimately has ok=false — it's a "we
		// can't verify" signal, not a "test failed" signal. Skip the
		// failure rule for it. (D1 introduced kind=insufficient.)
		push("base-evidence-failed", objID,
			"latest compile/test evidence has ok=false — fix the underlying failure before review")
	}

	// 5.5 D3: evidence-stale — if the current impl hash differs from
	//         what the evidence recorded, the evidence is invalid.
	//         The ONLY fix is to re-run typecalc_compile or
	//         typecalc_test (which captures the new hash). Any attempt
	//         to make the gate pass without re-running fails.
	if obj.Impl != nil && *obj.Impl != "" && rec != nil {
		path := *obj.Impl
		if cwd != "" && !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		if body, err := os.ReadFile(path); err == nil {
			currentHash := HashSource(string(body))
			if rec.ImplHash != "" && rec.ImplHash != currentHash {
				push("evidence-stale", objID, fmt.Sprintf(
					"compile/test evidence is for impl hash %s but the current impl is %s — re-run typecalc_compile or typecalc_test to refresh",
					shortHash(rec.ImplHash), shortHash(currentHash)))
			}
		}
	}

	// 5.6 D3: runtime trace must also be for the current impl hash.
	if t, ok := ReadRuntimeTrace(objID); ok && obj.Impl != nil && *obj.Impl != "" {
		path := *obj.Impl
		if cwd != "" && !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		if body, err := os.ReadFile(path); err == nil {
			currentHash := HashSource(string(body))
			if t.ImplHash != "" && t.ImplHash != currentHash {
				push("runtime-trace-stale", objID, fmt.Sprintf(
					"runtime trace was recorded for impl hash %s but current impl is %s — re-run typecalc_test (the harness will rewrite the trace)",
					shortHash(t.ImplHash), shortHash(currentHash)))
			}
		}
	}

	// 6. Spec description present and not stale. Stale = SHA-256 of impl
	//    content differs from the spec's recorded SourceHash.
	spec, specOK := ReadSpec(objID)
	if !specOK {
		push("spec-missing", objID, fmt.Sprintf(
			"no auto-generated description at %s — call typecalc_describe object_id=%q first",
			SpecEvidencePath(objID), objID))
	} else if obj.Impl != nil {
		path := *obj.Impl
		if !filepath.IsAbs(path) && cwd != "" {
			path = filepath.Join(cwd, path)
		}
		if body, err := os.ReadFile(path); err == nil {
			if HashSource(string(body)) != spec.SourceHash {
				push("spec-stale", objID,
					"description was generated against an earlier version of impl — re-run typecalc_describe to regenerate")
			}
		}
	}

	return issues
}

// shortHash truncates a SHA-256 hex string for human-readable error
// messages. Full hashes are 64 chars; 8 is enough to disambiguate.
func shortHash(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// readBaseEvidence is a lightweight reader for the existing
// <id>.json compile/test record (kind=compile|test). Returns
// (rec, ok) — false if the file is missing or unparseable.
func readBaseEvidence(cwd, objectID string) (*EvidenceRecord, bool) {
	if objectID == "" {
		return nil, false
	}
	path := filepath.Join(EvidenceDir, objectID+".json")
	if cwd != "" && !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	var rec EvidenceRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, false
	}
	return &rec, true
}
