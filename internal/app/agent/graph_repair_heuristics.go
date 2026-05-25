package agent

import (
	"fmt"
	"strings"
)

// RepairProposal is the structured output of a graph-repair detector.
// H_repair_graph turns this into a sub-agent task — the agent gets a
// concrete action to perform (delete this object, split into these
// pieces, etc.) rather than a generic "try something else" lesson.
//
// The 中步 design uses REASON-DRIVEN detectors: the LLM's own
// CANNOT_SYNTHESIZE explanation (persisted to
// TestsSection.LastSynthFailure) is the AUTHORITATIVE structural
// diagnosis. Detectors classify the reason text into proposal
// categories. This replaces the original clause-kind-ratio heuristic
// (rejected 2026-05-25 as not principled — LLM dutifully writes
// plausible-but-uncoverable example/invariant clauses for any object,
// so ratios lie).
//
// Big-step's LLM-driven proposer would fall back here when no
// reason-token-pattern matches — currently those cases pass through
// to Obstacle.
type RepairProposal struct {
	Kind            string   // matched detector name: "io-boundary" | "over-coarse" | "contract-ambiguous" | "lang-unsupported"
	TargetObject    string   // the object the detector fired on
	Diagnosis       string   // human-readable WHY this object is wrong-shape
	Action          string   // human-readable WHAT to do; rendered as the sub-agent task
	GraphMutations  []string // concrete graph_* tool calls the agent should execute
	ExpectedShape   string   // post-condition checker: what the graph should look like after
	DeleteFromGraph bool     // shorthand: should the object be removed entirely
}

// detectRepairProposal inspects what the LLM said about why it
// couldn't synthesize tests, classifies the reason, and returns a
// matching proposal. Returns nil when no detector matches (caller
// falls through to Obstacle — we have evidence the object is bad,
// but no actionable repair in our heuristic library).
//
// failureReason is the verbatim text the LLM wrote after the
// CANNOT_SYNTHESIZE token. Empty reason means synth never failed
// or wasn't called yet — also returns nil (we have no evidence to
// repair on; the confirm-exhaustion might be impl issues, not
// graph issues).
//
// objID and signature carry the object's identity + def-file
// contents for diagnostic context; not used for detection (the LLM's
// reason is the sole signal).
func detectRepairProposal(objID string, failureReason string, signature string) *RepairProposal {
	reason := strings.TrimSpace(failureReason)
	if reason == "" {
		return nil
	}
	if p := detectIOBoundaryFromReason(objID, reason, signature); p != nil {
		return p
	}
	if p := detectOverCoarseFromReason(objID, reason); p != nil {
		return p
	}
	if p := detectLangUnsupportedFromReason(objID, reason); p != nil {
		return p
	}
	if p := detectAmbiguousFromReason(objID, reason); p != nil {
		return p
	}
	return nil
}

// reasonContainsAny returns true when reason (case-insensitive)
// contains ANY of the given substrings. Used by every detector to
// pattern-match the LLM's text.
func reasonContainsAny(reason string, tokens ...string) bool {
	low := strings.ToLower(reason)
	for _, t := range tokens {
		if strings.Contains(low, strings.ToLower(t)) {
			return true
		}
	}
	return false
}

// detectIOBoundaryFromReason fires when the LLM's CANNOT_SYNTHESIZE
// reason mentions IO primitives or specifically calls out that the
// object can only be exercised by the ./probe reference. The agent's
// own words ("needs stdin", "reads files", "requires probe to drive")
// ARE the structural evidence — no derived ratios.
//
// Proposal: REMOVE this object from the graph. The IO behavior is
// already covered by equiv_oracle (./probe vs ./executable) when it
// lives in main() as plain glue. If pure-data sub-functions exist
// (parse the line, classify the byte, format the buffer), the agent
// is encouraged to declare THOSE.
func detectIOBoundaryFromReason(objID, reason, signature string) *RepairProposal {
	// Reason tokens the LLM uses when synth genuinely can't drive IO.
	// Observed in PB-30 batch 0522 entr's OsInput synth:
	//   "needs the reference implementation ./probe to drive stdin"
	//   "reads stdin as a side effect"
	//   "no way to inject input"
	// Tokens are intentionally unambiguous multi-char strings or
	// punctuation-bearing patterns. Avoid short common-English
	// substrings (audit 2026-05-25): "tty" matches "pretty"/"atty";
	// "open(" matches "reopen("; "input(" matches "sinput("; "terminal"
	// matches "terminally". Each removed token's semantic is already
	// covered by a more specific neighbor (stdin/FILE*/io.Reader/etc.).
	ioTokens := []string{
		// IO surface mentions — unique multi-char identifiers only
		"stdin", "stdout", "stderr",
		"FILE*", "fopen", "fread", "fgets", "fwrite", "scanf",
		"io.reader", "io.writer", "bufio", "os.stdin", "sys.stdin",
		// File system surface — phrases, not bare words
		"reads files", "writes files", "directory walk",
		"opens a file", "reads a file",
		// Process / socket surface — full phrases
		"http server", "listens on", "tcp socket", "udp socket",
		// Explicit "needs probe" admissions (PB-30 observed wording)
		"needs the reference", "needs ./probe", "needs probe",
		"requires ./probe", "requires the reference",
		"can only be exercised by", "must be observed via probe",
		// Side-effect-as-input shapes
		"side effect", "side-effect input", "implicit input",
	}
	if !reasonContainsAny(reason, ioTokens...) {
		return nil
	}
	sigHint := ""
	if signature != "" {
		sigHint = ioSignatureHint(signature)
	}
	return &RepairProposal{
		Kind:         "io-boundary",
		TargetObject: objID,
		Diagnosis: fmt.Sprintf(
			"LLM's CANNOT_SYNTHESIZE reason for %q explicitly cites IO / probe-dependency as the blocker. "+
				"kcpos's testCode harness compiles tests + impl into a single binary; there's no stdin pipe, "+
				"no tempfile fixture, no fs mock. The contract IS verifiable via equiv_oracle (./probe vs "+
				"./executable) when this code lives in main() as plain glue — not as a graph-tracked object "+
				"whose synth chain has no way to drive it.\n\nLLM's reason verbatim:\n  %s%s",
			objID, clipShort(reason, 300), sigHint),
		Action: fmt.Sprintf(
			"REMOVE %q from the graph via graph_delete_object. The behavioral check is already covered by "+
				"equiv_oracle when ./probe is present. If %q decomposes into pure-data sub-functions "+
				"(e.g. ParseLine(string)→ParsedInput, ClassifyByte(byte)→Kind, FormatBuffer(Data)→string), "+
				"declare THOSE as new graph objects instead — their contracts have no IO surface and synth "+
				"can drive them through testCode.",
			objID, objID),
		GraphMutations: []string{
			fmt.Sprintf("graph_delete_object id=%q", objID),
			"(optional) graph_create_object for each pure-data sub-function discovered while reading impl/README",
		},
		ExpectedShape: fmt.Sprintf(
			"After repair: object %q removed from K/graph.json. Optionally ≥1 new pure-data object(s) "+
				"declared in its place.", objID),
		DeleteFromGraph: true,
	}
}

// detectOverCoarseFromReason fires when the LLM explicitly admits
// the object is too large / has too many cases / exceeds output
// budget. The Step 2 system prompt told the LLM to return
// "object too coarse, split first" exactly when this happens —
// catch that wording here.
//
// Proposal: do NOT delete; instead the agent should graph_split_object
// or re-declare into smaller pieces. H_repair_graph's sub-agent
// gets the message and is expected to do the split via graph_create
// + graph_link + graph_delete on the old one.
func detectOverCoarseFromReason(objID, reason string) *RepairProposal {
	tokens := []string{
		"too coarse", "too large", "too complex",
		"too many cases", "too many clauses", "split first",
		"object too coarse", "decompose into smaller",
		"exceeds output budget", "cases > 15", ">15 cases",
		"split into smaller graph objects",
	}
	if !reasonContainsAny(reason, tokens...) {
		return nil
	}
	return &RepairProposal{
		Kind:         "over-coarse",
		TargetObject: objID,
		Diagnosis: fmt.Sprintf(
			"LLM's CANNOT_SYNTHESIZE reason for %q explicitly admits the object is too large to cover with "+
				"a single test suite (Step 2 output-budget rule fired). The H_graph_declare granularity gate "+
				"already enforces ≤4 produces+mutates at declare-time, so this means the LLM is hitting a "+
				"clause-volume ceiling even within structural limits — the contract has too many independent "+
				"invariants to test in one piece.\n\nLLM's reason verbatim:\n  %s",
			objID, clipShort(reason, 300)),
		Action: fmt.Sprintf(
			"SPLIT %q into smaller graph objects. Read the spec / impl to find natural decomposition seams "+
				"(per-flag handler, per-input-format branch, per-output-section formatter). Use graph_create_object "+
				"for each piece + graph_link_* to wire them + graph_delete_object on the old monolithic one. "+
				"Each new object should have a contract small enough to fit ≤15 test cases.",
			objID),
		GraphMutations: []string{
			fmt.Sprintf("graph_create_object for each split piece, wire via graph_link_*, then graph_delete_object id=%q", objID),
		},
		ExpectedShape: fmt.Sprintf(
			"After repair: object %q removed; ≥2 new smaller objects declared covering the same contract surface.", objID),
		DeleteFromGraph: false, // delete + replace, not just delete
	}
}

// detectLangUnsupportedFromReason fires when the LLM says the
// language has no harness / no test runner / can't be tested in the
// declared lang. The fix is usually to switch the impl language or
// drop the object (if it's a wrapper for an unsupported runtime).
func detectLangUnsupportedFromReason(objID, reason string) *RepairProposal {
	tokens := []string{
		"language is not supported", "no harness for", "no test runner for",
		"language has no in-tree", "no in-tree compile", "no in-tree test",
		"can't compile this language", "cannot test in this language",
		"unsupported language", "no runtime eval",
	}
	if !reasonContainsAny(reason, tokens...) {
		return nil
	}
	return &RepairProposal{
		Kind:         "lang-unsupported",
		TargetObject: objID,
		Diagnosis: fmt.Sprintf(
			"LLM's CANNOT_SYNTHESIZE reason for %q identifies the impl language as lacking harness support. "+
				"kcpos's testCode runner is C / Go / Python / TypeScript. If the agent declared an impl in "+
				"a different language (or in a language whose harness isn't installed), there's no way to "+
				"drive tests.\n\nLLM's reason verbatim:\n  %s",
			objID, clipShort(reason, 300)),
		Action: fmt.Sprintf(
			"Either (a) change the impl language for %q via graph_merge_object patch='{\"implLang\":\"<C|Go|Python|TypeScript>\"}' "+
				"AND rewrite the impl file in that language; OR (b) if %q is a wrapper for an external runtime "+
				"that kcpos can't drive, REMOVE %q via graph_delete_object and let the external behavior be "+
				"covered by equiv_oracle.",
			objID, objID, objID),
		GraphMutations: []string{
			fmt.Sprintf("graph_merge_object id=%q patch='{\"implLang\":\"...\"}' (choose C / Go / Python / TypeScript) — OR — graph_delete_object id=%q", objID, objID),
		},
		ExpectedShape: fmt.Sprintf(
			"After repair: object %q's implLang is one of the supported set, OR the object is removed.", objID),
		DeleteFromGraph: false,
	}
}

// detectAmbiguousFromReason fires when the LLM explicitly says the
// spec/description is too underspecified or ambiguous to derive
// tests. The fix is NOT to mutate the graph but to re-describe with
// concrete examples. Since H_repair_graph runs at confirm-exhausted
// time, the proposal here is: demote the object, force re-describe
// (via the existing static_check stale-spec rule + a fresh
// typecalc_describe).
func detectAmbiguousFromReason(objID, reason string) *RepairProposal {
	tokens := []string{
		"too underspecified", "ambiguous", "underspecified",
		"contract is unclear", "spec is unclear", "vague",
		"no concrete examples", "cannot derive",
		"need more context", "insufficient detail",
	}
	if !reasonContainsAny(reason, tokens...) {
		return nil
	}
	return &RepairProposal{
		Kind:         "contract-ambiguous",
		TargetObject: objID,
		Diagnosis: fmt.Sprintf(
			"LLM's CANNOT_SYNTHESIZE reason for %q says the contract is too vague / underspecified to derive "+
				"tests. The graph object exists but its spec.Contract clauses don't carry enough concrete "+
				"detail (e.g. all clauses are vague invariants without examples). The fix is to enrich the "+
				"description, not to mutate the graph structure.\n\nLLM's reason verbatim:\n  %s",
			objID, clipShort(reason, 300)),
		Action: fmt.Sprintf(
			"Demote %q via graph_merge_object patch='{\"status\":\"declared\"}', then the agent's next "+
				"confirm cycle will re-run typecalc_describe which has a fresh chance to emit concrete "+
				"clauses. If re-describe also returns vague clauses, the SPEC.md likely lacks the concrete "+
				"examples needed for this object — agent should consider widening the spec read or splitting "+
				"the object differently.",
			objID),
		GraphMutations: []string{
			fmt.Sprintf("graph_merge_object id=%q patch='{\"status\":\"declared\"}'", objID),
		},
		ExpectedShape: fmt.Sprintf(
			"After repair: object %q is status=declared (description will regenerate on next confirm); "+
				"graph structure unchanged.", objID),
		DeleteFromGraph: false,
	}
}

// clipShort trims s to maxLen with ellipsis. Used for diagnostic
// messages where the full LLM reason might be many sentences.
func clipShort(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

// ioSignatureHint surfaces specific signature tokens (stdin, FILE*,
// io.Reader, etc.) so the diagnosis is concrete enough for the agent
// to recognize. Empty when nothing matches. Used as a SECONDARY
// signal alongside the LLM's reason — never as the primary trigger.
func ioSignatureHint(sig string) string {
	low := strings.ToLower(sig)
	var hits []string
	// Signature-side tokens — same audit applied: dropped open(/input(
	// (substring false-positive in reopen()/sinput()) and bare
	// "readline" (matches "preadline"). Kept the unique fully-qualified
	// forms (sys.stdin, bufio.scanner, etc.).
	tokens := []string{"stdin", "stdout", "stderr", "fopen", "fread", "fgets", "fwrite", "scanf", "printf",
		"io.reader", "io.writer", "*os.file", "bufio.scanner", "ioutil.read", "os.open", "sys.stdin",
		"readline(", "file*", "file *", "char**"}
	for _, t := range tokens {
		if strings.Contains(low, t) {
			hits = append(hits, t)
		}
	}
	if len(hits) == 0 {
		return ""
	}
	return fmt.Sprintf("\n\nSignature also mentions IO primitives: %s.", strings.Join(hits, ", "))
}
