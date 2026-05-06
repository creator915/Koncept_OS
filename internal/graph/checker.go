package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Severity classifies validation issues. Errors block "graph PASS"; warnings
// surface concerns the user should review but don't fail validation.
type Severity string

const (
	Error Severity = "error"
	Warn  Severity = "warn"
)

// Issue is one finding from validation.
type Issue struct {
	Severity Severity
	Rule     string // short name, e.g. "produce-consume-balance"
	Where    string // graph element id (or "<global>" if not tied to one)
	Message  string
}

// ValidationReport collects all issues from a Validate call.
type ValidationReport struct {
	Issues []Issue
}

// HasErrors reports whether any issue is severity Error.
func (r *ValidationReport) HasErrors() bool {
	for _, i := range r.Issues {
		if i.Severity == Error {
			return true
		}
	}
	return false
}

// Counts returns the number of errors and warnings.
func (r *ValidationReport) Counts() (errs, warns int) {
	for _, i := range r.Issues {
		switch i.Severity {
		case Error:
			errs++
		case Warn:
			warns++
		}
	}
	return
}

// String produces a human-readable report. Issues are sorted by severity
// (errors first), then rule, then where, for stable output.
func (r *ValidationReport) String() string {
	if len(r.Issues) == 0 {
		return "validate: PASS (no issues)"
	}
	sorted := make([]Issue, len(r.Issues))
	copy(sorted, r.Issues)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Severity != sorted[j].Severity {
			return sorted[i].Severity == Error
		}
		if sorted[i].Rule != sorted[j].Rule {
			return sorted[i].Rule < sorted[j].Rule
		}
		return sorted[i].Where < sorted[j].Where
	})
	errs, warns := r.Counts()
	var b strings.Builder
	verdict := "PASS"
	if errs > 0 {
		verdict = "FAIL"
	}
	fmt.Fprintf(&b, "validate: %s (%d error, %d warn)\n\n", verdict, errs, warns)
	for _, i := range sorted {
		fmt.Fprintf(&b, "  [%s] %s · %s · %s\n", i.Severity, i.Rule, i.Where, i.Message)
	}
	return b.String()
}

// Validate runs all checker rules against g. cwd is used for filesystem-
// touching rules (impl-existence); pass "" to skip those.
//
// Rules implemented (CLAUDE.md §8.2):
//
//	ERROR  1 produce-consume-balance
//	ERROR  2 refines-dag (no cycles)
//	ERROR  4 temporal-consistency
//	ERROR  5 naming-uniqueness (cross-namespace)
//	ERROR  6 reference-integrity
//	WARN   7 orphan-attribute
//	WARN   8 impl-existence (skipped when cwd is "")
//	WARN   9 metadata-completeness
//
// Skipped: 3 (refinement coverage — needs valueSpace semantics) and
// 10 (laws consistency — needs deeper laws spec).
func (g *Graph) Validate(cwd string) *ValidationReport {
	r := &ValidationReport{}

	// Order matters: integrity before structural rules; DAG before any rule
	// that walks the refines partial order.
	checkReferenceIntegrity(g, r)
	checkNamingUniqueness(g, r)
	checkRefinesDAG(g, r)

	checkProduceConsumeBalance(g, r)
	checkTemporalConsistency(g, r)
	checkOrphanAttributes(g, r)
	checkMetadataCompleteness(g, r)
	if cwd != "" {
		checkImplExistence(g, r, cwd)
	}

	return r
}

// --- Rule 6: reference integrity ---

func checkReferenceIntegrity(g *Graph, r *ValidationReport) {
	const rule = "reference-integrity"
	for id, a := range g.Attributes {
		for _, parent := range a.Refines {
			if _, ok := g.Attributes[parent]; !ok {
				r.Issues = append(r.Issues, Issue{Error, rule, id, fmt.Sprintf("refines unknown attribute %q", parent)})
			}
		}
	}
	for id, o := range g.Objects {
		for _, attr := range o.Consumes {
			if _, ok := g.Attributes[attr]; !ok {
				r.Issues = append(r.Issues, Issue{Error, rule, id, fmt.Sprintf("consumes unknown attribute %q", attr)})
			}
		}
		for _, attr := range o.Produces {
			if _, ok := g.Attributes[attr]; !ok {
				r.Issues = append(r.Issues, Issue{Error, rule, id, fmt.Sprintf("produces unknown attribute %q", attr)})
			}
		}
		if o.Temporal != nil {
			for i, fr := range o.Temporal.Consumes {
				if _, ok := g.Attributes[fr.Attribute]; !ok {
					r.Issues = append(r.Issues, Issue{Error, rule, id, fmt.Sprintf("temporal.consumes[%d] references unknown attribute %q", i, fr.Attribute)})
				}
			}
			for i, fr := range o.Temporal.Produces {
				if _, ok := g.Attributes[fr.Attribute]; !ok {
					r.Issues = append(r.Issues, Issue{Error, rule, id, fmt.Sprintf("temporal.produces[%d] references unknown attribute %q", i, fr.Attribute)})
				}
			}
		}
	}
}

// --- Rule 5: naming uniqueness across the global namespace ---

func checkNamingUniqueness(g *Graph, r *ValidationReport) {
	const rule = "naming-uniqueness"
	for id := range g.Attributes {
		if _, ok := g.Objects[id]; ok {
			r.Issues = append(r.Issues, Issue{Error, rule, id, "id appears in both attributes and objects (global namespace conflict)"})
		}
	}
}

// --- Rule 2: refines DAG (no cycles) ---

func checkRefinesDAG(g *Graph, r *ValidationReport) {
	const rule = "refines-dag"
	const (
		unseen  = 0
		inStack = 1
		done    = 2
	)
	state := map[string]int{}
	var visit func(id string) bool
	visit = func(id string) bool {
		if state[id] == inStack {
			return true
		}
		if state[id] == done {
			return false
		}
		state[id] = inStack
		if a, ok := g.Attributes[id]; ok {
			for _, parent := range a.Refines {
				if visit(parent) {
					return true
				}
			}
		}
		state[id] = done
		return false
	}
	cyclic := map[string]bool{}
	for id := range g.Attributes {
		if state[id] == unseen {
			if visit(id) {
				cyclic[id] = true
			}
		}
	}
	for id := range cyclic {
		r.Issues = append(r.Issues, Issue{Error, rule, id, "refines cycle reachable from this attribute"})
	}
}

// --- Rule 1: produce-consume balance (with subtype substitution) ---

func checkProduceConsumeBalance(g *Graph, r *ValidationReport) {
	const rule = "produce-consume-balance"
	// Build the inverse refines index: children[parent] = ids that refine parent.
	children := map[string][]string{}
	for id, a := range g.Attributes {
		for _, parent := range a.Refines {
			children[parent] = append(children[parent], id)
		}
	}
	produced := map[string]bool{}
	for _, o := range g.Objects {
		for _, p := range o.Produces {
			produced[p] = true
		}
	}
	// satisfied: attr is produced directly OR some descendant (subtype) is produced.
	satisfied := func(attr string) bool {
		if produced[attr] {
			return true
		}
		stack := []string{attr}
		seen := map[string]bool{attr: true}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, child := range children[cur] {
				if seen[child] {
					continue
				}
				seen[child] = true
				if produced[child] {
					return true
				}
				stack = append(stack, child)
			}
		}
		return false
	}
	for id, o := range g.Objects {
		for _, attr := range o.Consumes {
			if _, ok := g.Attributes[attr]; !ok {
				continue // already reported by reference-integrity
			}
			if !satisfied(attr) {
				r.Issues = append(r.Issues, Issue{Error, rule, id, fmt.Sprintf("consumes %q but no object produces it or any subtype", attr)})
			}
		}
	}
}

// --- Rule 4: temporal frame causality ---

func checkTemporalConsistency(g *Graph, r *ValidationReport) {
	const rule = "temporal-consistency"
	for id, o := range g.Objects {
		if o.Temporal == nil {
			continue
		}
		t := o.Temporal
		if t.FrameVar == "" {
			r.Issues = append(r.Issues, Issue{Error, rule, id, "temporal.frameVar is empty"})
			continue
		}
		maxIn := -1
		for i, fr := range t.Consumes {
			d, ok := parseFrameDepth(fr.Frame, t.FrameVar)
			if !ok {
				r.Issues = append(r.Issues, Issue{Error, rule, id, fmt.Sprintf("temporal.consumes[%d].frame %q has invalid syntax (expected %s or %s.succ()…)", i, fr.Frame, t.FrameVar, t.FrameVar)})
				continue
			}
			if d > maxIn {
				maxIn = d
			}
		}
		for i, fr := range t.Produces {
			d, ok := parseFrameDepth(fr.Frame, t.FrameVar)
			if !ok {
				r.Issues = append(r.Issues, Issue{Error, rule, id, fmt.Sprintf("temporal.produces[%d].frame %q has invalid syntax", i, fr.Frame)})
				continue
			}
			if maxIn >= 0 && d < maxIn {
				r.Issues = append(r.Issues, Issue{Error, rule, id, fmt.Sprintf("temporal.produces[%d] depth %d < max input depth %d (output to past)", i, d, maxIn)})
			}
		}
	}
}

// parseFrameDepth parses an expression of the form
// `<frameVar>(.succ())*` and returns the number of `.succ()` calls.
// Returns false for any syntactic deviation.
func parseFrameDepth(expr, frameVar string) (int, bool) {
	s := strings.TrimSpace(expr)
	if s == frameVar {
		return 0, true
	}
	if !strings.HasPrefix(s, frameVar+".") {
		return 0, false
	}
	rest := strings.TrimPrefix(s, frameVar)
	depth := 0
	for rest != "" {
		if !strings.HasPrefix(rest, ".succ()") {
			return 0, false
		}
		rest = rest[len(".succ()"):]
		depth++
	}
	return depth, true
}

// --- Rule 7: orphan attribute (produced but never consumed) ---

func checkOrphanAttributes(g *Graph, r *ValidationReport) {
	const rule = "orphan-attribute"
	consumed := map[string]bool{}
	produced := map[string]bool{}
	for _, o := range g.Objects {
		for _, a := range o.Consumes {
			consumed[a] = true
		}
		for _, a := range o.Produces {
			produced[a] = true
		}
	}
	for id := range g.Attributes {
		if produced[id] && !consumed[id] {
			r.Issues = append(r.Issues, Issue{Warn, rule, id, "produced but never consumed (terminal output, or dead)"})
		}
	}
}

// --- Rule 8: impl file existence on disk ---

func checkImplExistence(g *Graph, r *ValidationReport, cwd string) {
	const rule = "impl-existence"
	for id, o := range g.Objects {
		if o.Impl == nil || *o.Impl == "" {
			continue
		}
		path := *o.Impl
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		if _, err := os.Stat(path); err != nil {
			r.Issues = append(r.Issues, Issue{Warn, rule, id, fmt.Sprintf("impl %q does not exist on disk", *o.Impl)})
		}
	}
}

// --- Rule 9: metadata completeness ---

func checkMetadataCompleteness(g *Graph, r *ValidationReport) {
	const rule = "metadata-completeness"
	for id, a := range g.Attributes {
		if a.Def == "" {
			r.Issues = append(r.Issues, Issue{Warn, rule, id, "attribute has empty def"})
		}
		if a.Intent == "" {
			r.Issues = append(r.Issues, Issue{Warn, rule, id, "attribute has empty intent"})
		}
	}
	for id, o := range g.Objects {
		if o.Def == "" {
			r.Issues = append(r.Issues, Issue{Warn, rule, id, "object has empty def"})
		}
		if o.Intent == "" {
			r.Issues = append(r.Issues, Issue{Warn, rule, id, "object has empty intent"})
		}
	}
}
