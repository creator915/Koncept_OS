package agent

import (
	"fmt"
	"strings"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// RepairProposal is the structured output of a graph-repair heuristic.
// H_repair_graph turns this into a sub-agent task — the agent gets a
// concrete action to perform (delete this object, split into these
// pieces, etc.) rather than a generic "try something else" lesson.
//
// The 中步 (medium-step) design intentionally uses HEURISTIC detectors
// only — LLM-driven proposal synthesis is reserved for big-step. The
// trade-off: 中步 covers the common-shape failures (IO-boundary,
// over-coarse) with predictable proposals; big-step would cover the
// long tail at the cost of a much larger surface.
type RepairProposal struct {
	Kind            string   // matched heuristic name: "io-boundary" | "over-coarse" | …
	TargetObject    string   // the object the heuristic fired on
	Diagnosis       string   // human-readable WHY this object's shape is wrong
	Action          string   // human-readable WHAT to do; rendered as the sub-agent task
	GraphMutations  []string // concrete graph_* tool calls the agent should execute
	ExpectedShape   string   // post-condition checker: what the graph should look like after
	DeleteFromGraph bool     // shorthand: should the object be removed entirely
}

// detectRepairProposal inspects an object's contract clauses and
// signature, returns the first matching heuristic's proposal, or nil
// when no heuristic applies. Order of detectors matters — earlier
// matches preempt later ones. Currently a single detector ships
// (IO-boundary); add more as PB-30 failure modes surface.
//
// signature is the def-file content (may be empty if the agent never
// wrote a def). Pass "" to skip signature-driven heuristics.
func detectRepairProposal(objID string, clauses []core.ContractClause, signature string) *RepairProposal {
	if p := detectIOBoundary(objID, clauses, signature); p != nil {
		return p
	}
	return nil
}

// detectIOBoundary fires when an object's contract is dominated by
// characterization clauses (probe-derived observations) with zero
// example or invariant clauses. Diagnostic: the object's behavior is
// observable but not expressible in terms of structured test cases —
// kcpos's testCode harness can't drive IO-bound C functions like
// OsInput / ReadStdin / ProbeFs without IO fixture support, so synth
// loops on CANNOT_SYNTHESIZE and confirm exhausts.
//
// Proposal: REMOVE this object from the graph. The IO behavior is
// already verified via equiv_oracle (./probe vs ./executable) when
// it lives inside main() as plain glue code. The agent should
// re-declare the testable pure-data sub-functions instead — e.g.
// instead of OsInput, declare ParseInputLine(line string) → ParsedInput.
//
// Conditions (ALL must hold to fire):
//   - ≥1 characterization clause
//   - 0 example clauses
//   - 0 invariant clauses
//
// The all-or-nothing nature is deliberate: 1 example or 1 invariant
// means the LLM found SOMETHING testable about the object's
// contract — leave it alone, the synth chain will find a way.
func detectIOBoundary(objID string, clauses []core.ContractClause, signature string) *RepairProposal {
	if len(clauses) == 0 {
		return nil
	}
	var ex, inv, char int
	for _, c := range clauses {
		switch c.Kind {
		case "example":
			ex++
		case "invariant":
			inv++
		case "characterization":
			char++
		}
	}
	if char < 1 || ex > 0 || inv > 0 {
		return nil
	}
	hint := ""
	if signature != "" {
		hint = ioSignatureHint(signature)
	}
	return &RepairProposal{
		Kind:         "io-boundary",
		TargetObject: objID,
		Diagnosis: fmt.Sprintf(
			"Object %q has %d characterization clause(s) but 0 example and 0 invariant clauses. "+
				"This shape means the contract IS observable (via probe) but NOT expressible as structured "+
				"test cases — kcpos's testCode harness has no IO fixture layer (no stdin pipe, no tempfile, "+
				"no fs mock), so the synth chain loops on CANNOT_SYNTHESIZE and confirm exhausts.%s",
			objID, char, hint),
		Action: fmt.Sprintf(
			"REMOVE %q from the graph (graph_merge_object can't delete, use the agent-visible delete tool "+
				"or — if no delete tool — split into smaller testable objects and leave the IO surface as "+
				"plain glue code in main()). The behavioral check that was driving %q is already covered by "+
				"equiv_oracle when ./probe is present (the reconstruction-mode oracle compares the whole "+
				"./executable against ./probe). If the object's IO behavior decomposes into pure-data "+
				"sub-functions (e.g. ParseLine(string)→ParsedInput, ClassifyChar(byte)→CharKind), declare "+
				"THOSE as new graph objects and wire them. The IO call sites stay outside the graph.",
			objID, objID),
		GraphMutations: []string{
			fmt.Sprintf("graph_delete_object id=%q (or: graph_create_object for the pure-data sub-functions then graph_delete_object on %s)", objID, objID),
		},
		ExpectedShape: fmt.Sprintf(
			"After repair: object %q removed from K/graph.json. Optionally ≥1 new pure-data object(s) "+
				"declared in its place with example/invariant clauses (run typecalc_describe on the new ones "+
				"to populate spec.Contract).", objID),
		DeleteFromGraph: true,
	}
}

// ioSignatureHint surfaces specific signature tokens (stdin, FILE*,
// io.Reader, etc.) so the diagnosis is concrete enough for the agent
// to recognize. Empty when nothing matches.
func ioSignatureHint(sig string) string {
	low := strings.ToLower(sig)
	var hits []string
	tokens := []string{"stdin", "stdout", "stderr", "fopen", "fread", "fgets", "fwrite", "scanf", "printf",
		"io.reader", "io.writer", "*os.file", "bufio.scanner", "ioutil.read", "os.open", "sys.stdin",
		"open(", "input(", "readline",
		// C IO types — match common pointer forms.
		"file*", "file *", "char**"}
	for _, t := range tokens {
		if strings.Contains(low, t) {
			hits = append(hits, t)
		}
	}
	if len(hits) == 0 {
		return ""
	}
	return fmt.Sprintf(" Signature mentions IO primitives: %s.", strings.Join(hits, ", "))
}
