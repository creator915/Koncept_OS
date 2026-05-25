package agent

import (
	"strings"
	"testing"
)

// TestDetectIOBoundaryFromReason_FiresOnExplicitProbeMention — the
// canonical entr OsInput case: LLM admits it needs the reference
// probe to drive stdin. This is the LLM's own structural diagnosis
// (vs. derived clause-ratio guesses).
func TestDetectIOBoundaryFromReason_FiresOnExplicitProbeMention(t *testing.T) {
	reason := "CANNOT_SYNTHESIZE: needs the reference implementation ./probe to drive stdin — OsInput reads stdin as a side effect."
	p := detectRepairProposal("OsInput", reason, "")
	if p == nil {
		t.Fatal("expected IO-boundary to fire on explicit ./probe mention")
	}
	if p.Kind != "io-boundary" {
		t.Errorf("kind: got %q want io-boundary", p.Kind)
	}
	if !p.DeleteFromGraph {
		t.Error("io-boundary proposal should suggest deletion")
	}
	if !strings.Contains(p.Diagnosis, "OsInput") {
		t.Error("diagnosis should name target object")
	}
	if !strings.Contains(p.Diagnosis, "./probe to drive stdin") {
		t.Errorf("diagnosis should echo LLM's verbatim reason: %s", p.Diagnosis)
	}
}

// TestDetectIOBoundaryFromReason_StdinAlone — short reason like "this
// reads stdin" without explicit ./probe mention also fires (stdin is
// itself a strong IO signal).
func TestDetectIOBoundaryFromReason_StdinAlone(t *testing.T) {
	reason := "CANNOT_SYNTHESIZE: this function reads stdin, no way to inject input from a test."
	p := detectRepairProposal("ReadStdin", reason, "")
	if p == nil || p.Kind != "io-boundary" {
		t.Fatalf("expected io-boundary, got %+v", p)
	}
}

// TestDetectOverCoarseFromReason_FiresOnSplitFirst — the Step 2
// system prompt told LLM to return "object too coarse, split first"
// when cases > 15. Catch that wording.
func TestDetectOverCoarseFromReason_FiresOnSplitFirst(t *testing.T) {
	reason := "CANNOT_SYNTHESIZE: object too coarse, split into smaller graph objects (current contract needs >15 cases to cover)."
	p := detectRepairProposal("BigObject", reason, "")
	if p == nil || p.Kind != "over-coarse" {
		t.Fatalf("expected over-coarse, got %+v", p)
	}
	if p.DeleteFromGraph {
		t.Error("over-coarse proposal should NOT suggest pure deletion (split-then-replace)")
	}
	if !strings.Contains(p.Action, "SPLIT") {
		t.Errorf("action should call for split: %s", p.Action)
	}
}

// TestDetectLangUnsupportedFromReason — the harness can't drive
// languages outside C / Go / Python / TypeScript.
func TestDetectLangUnsupportedFromReason(t *testing.T) {
	reason := "CANNOT_SYNTHESIZE: no test runner for this language — cannot test in this language."
	p := detectRepairProposal("RustWrapper", reason, "")
	if p == nil || p.Kind != "lang-unsupported" {
		t.Fatalf("expected lang-unsupported, got %+v", p)
	}
}

// TestDetectAmbiguousFromReason — vague contract that the LLM can't
// derive concrete tests from. Fix is re-describe, not graph mutation.
func TestDetectAmbiguousFromReason(t *testing.T) {
	reason := "CANNOT_SYNTHESIZE: spec is too underspecified — no concrete examples to derive deterministic tests from."
	p := detectRepairProposal("VagueObject", reason, "")
	if p == nil || p.Kind != "contract-ambiguous" {
		t.Fatalf("expected contract-ambiguous, got %+v", p)
	}
	if p.DeleteFromGraph {
		t.Error("ambiguous proposal should NOT delete — re-describe is the action")
	}
}

// TestDetectRepairProposal_EmptyReason — no LLM signal → no proposal.
// Caller falls through to Obstacle; we don't guess.
func TestDetectRepairProposal_EmptyReason(t *testing.T) {
	if p := detectRepairProposal("X", "", ""); p != nil {
		t.Errorf("empty reason should yield no proposal, got %+v", p)
	}
	if p := detectRepairProposal("X", "   ", ""); p != nil {
		t.Errorf("whitespace-only reason should yield no proposal, got %+v", p)
	}
}

// TestDetectRepairProposal_UnknownReason — LLM said SOMETHING but it
// doesn't match any of the structural failure categories. Don't fire
// (rather than fire a wrong-category proposal).
func TestDetectRepairProposal_UnknownReason(t *testing.T) {
	reason := "CANNOT_SYNTHESIZE: I'm tired and don't feel like doing this."
	if p := detectRepairProposal("X", reason, ""); p != nil {
		t.Errorf("unknown reason should not match any detector, got %+v", p)
	}
}

// TestReasonContainsAny_CaseInsensitive — detector should be
// case-insensitive on both the reason and the tokens.
func TestReasonContainsAny_CaseInsensitive(t *testing.T) {
	if !reasonContainsAny("Function Reads STDIN as side effect", "stdin") {
		t.Error("case-insensitive match failed")
	}
	if reasonContainsAny("pure data function", "stdin", "fopen") {
		t.Error("should not match when none of the tokens present")
	}
}

// TestDetectIOBoundaryFromReason_NoSubstringFalsePositive (audit #1+#4
// fix) — the previous token list had "tty"/"open("/"input(" which
// matched "pretty"/"reopen("/"sinput(". A reason mentioning the task
// name "tty-clock" or a benign "reopen file context" must NOT trigger
// io-boundary.
func TestDetectIOBoundaryFromReason_NoSubstringFalsePositive(t *testing.T) {
	cases := []string{
		"CANNOT_SYNTHESIZE: the tty-clock object's contract is too vague to derive tests",
		"CANNOT_SYNTHESIZE: function reopens a connection that was closed in a previous call",
		"CANNOT_SYNTHESIZE: validator handles pretty-printed JSON differently from minified",
		"CANNOT_SYNTHESIZE: the terminally underspecified contract has no concrete examples",
	}
	for _, reason := range cases {
		t.Run(reason[:min(40, len(reason))], func(t *testing.T) {
			p := detectRepairProposal("X", reason, "")
			if p != nil && p.Kind == "io-boundary" {
				t.Errorf("substring false-positive on %q → fired io-boundary; reason should NOT trigger IO category", reason)
			}
		})
	}
}

// TestIOSignatureHint_SecondaryOnly — signature hint augments the
// diagnosis but never triggers detection on its own (a pure-data
// function with no IO reason MUST NOT get classified as IO-boundary
// just because its signature happens to have a `char*` argument).
func TestIOSignatureHint_SecondaryOnly(t *testing.T) {
	// reason is non-IO; signature mentions fopen. Should NOT fire
	// io-boundary because the LLM didn't say IO.
	if p := detectRepairProposal("ParseString", "CANNOT_SYNTHESIZE: spec is too underspecified",
		"int parse(char* in, FILE* unused);"); p != nil {
		if p.Kind == "io-boundary" {
			t.Errorf("signature alone must not trigger io-boundary — reason should be the sole primary signal")
		}
	}
}
