package synthesize

import (
	"context"
	"strings"
	"testing"
)

// TestParseDescribeReply_LegacyProseOnly — a reply with no sentinel is
// treated as pure prose. Contract is nil, no error. This preserves the
// pre-Step-2 behavior so old LLM checkpoints / cached replies still work.
func TestParseDescribeReply_LegacyProseOnly(t *testing.T) {
	desc, contract, err := parseDescribeReply("Adds two ints, returns the sum. No I/O.")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if contract != nil {
		t.Errorf("legacy reply should have nil Contract, got %+v", contract)
	}
	if desc != "Adds two ints, returns the sum. No I/O." {
		t.Errorf("description mangled: %q", desc)
	}
}

// TestParseDescribeReply_StructuredContract — sentinel + JSON yields
// description split + parsed clauses. The three kinds round-trip with
// their fields.
func TestParseDescribeReply_StructuredContract(t *testing.T) {
	reply := `Adds two ints. Panics on overflow.

---CONTRACT---
{"clauses":[
  {"id":"c1","kind":"example","body":"Add(2,3)=5","source":"spec:S§1"},
  {"id":"c2","kind":"invariant","body":"commutative","source":"intent"},
  {"id":"c3","kind":"characterization","body":"Add(MAX,1) panics","source":"char:probe_5"}
]}`
	desc, contract, err := parseDescribeReply(reply)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.HasPrefix(desc, "Adds two ints") || strings.Contains(desc, "---") {
		t.Errorf("description didn't split cleanly: %q", desc)
	}
	if len(contract) != 3 {
		t.Fatalf("expected 3 clauses, got %d", len(contract))
	}
	if contract[0].Kind != "example" || contract[1].Kind != "invariant" || contract[2].Kind != "characterization" {
		t.Errorf("kind misorder: %+v", contract)
	}
	if contract[2].Source != "char:probe_5" {
		t.Errorf("source lost: %q", contract[2].Source)
	}
}

// TestParseDescribeReply_DropsMalformedClauses — clauses missing Kind
// or Body are silently dropped (sloppy LLM tolerated); clauses missing
// only ID get a synthetic c<idx>. Empty array is allowed.
func TestParseDescribeReply_DropsMalformedClauses(t *testing.T) {
	reply := `Desc.

---CONTRACT---
{"clauses":[
  {"id":"c1","kind":"example","body":"good","source":"spec"},
  {"id":"c2","body":"no kind"},
  {"id":"c3","kind":"invariant","body":""},
  {"kind":"invariant","body":"missing id"}
]}`
	_, contract, err := parseDescribeReply(reply)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(contract) != 2 {
		t.Fatalf("expected 2 surviving clauses (c1 + synthetic-id one), got %d: %+v", len(contract), contract)
	}
	if contract[0].ID != "c1" {
		t.Errorf("first survivor should be c1, got %q", contract[0].ID)
	}
	// The "missing id" clause was at index 3 in the input, so synthetic id = "c4".
	if contract[1].ID != "c4" {
		t.Errorf("missing-id clause should get c4 (1-indexed input position), got %q", contract[1].ID)
	}
}

// TestParseDescribeReply_EmptyClausesArray — explicit empty array is a
// LEGITIMATE outcome (system prompt allows it when nothing testable
// can be extracted). Not an error; Contract is a non-nil empty slice.
func TestParseDescribeReply_EmptyClausesArray(t *testing.T) {
	reply := `Trivial.

---CONTRACT---
{"clauses":[]}`
	_, contract, err := parseDescribeReply(reply)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(contract) != 0 {
		t.Errorf("expected 0 clauses, got %d", len(contract))
	}
}

// TestParseDescribeReply_BadJSONIsErrorNotSilent — a sentinel followed
// by invalid JSON must error rather than silently writing an empty
// contract; otherwise the Step 4 gate would fail every test for "no
// clause to cite" and the agent wouldn't know to re-describe.
func TestParseDescribeReply_BadJSONIsErrorNotSilent(t *testing.T) {
	reply := `Desc.

---CONTRACT---
{"clauses": [missing-quotes-everywhere]`
	_, _, err := parseDescribeReply(reply)
	if err == nil {
		t.Fatal("expected parse error on malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "parse contract JSON") {
		t.Errorf("error message should identify the parser failure, got: %v", err)
	}
}

// TestParseDescribeReply_CodeFencedJSON — defensive: LLMs sometimes
// wrap the contract block in ```json fences despite the "no fences"
// instruction. stripCodeFences handles it.
func TestParseDescribeReply_CodeFencedJSON(t *testing.T) {
	reply := "Desc.\n\n---CONTRACT---\n```json\n{\"clauses\":[{\"id\":\"c1\",\"kind\":\"example\",\"body\":\"x\"}]}\n```"
	_, contract, err := parseDescribeReply(reply)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(contract) != 1 {
		t.Fatalf("expected 1 clause, got %d", len(contract))
	}
}

// TestDescribeWithInvoker_PopulatesContract — end-to-end via the
// Invoker seam: a structured reply lands on DescribeOutput with both
// fields set.
func TestDescribeWithInvoker_PopulatesContract(t *testing.T) {
	out, err := DescribeWithInvoker(context.Background(),
		DescribeInputs{ObjectID: "X", Impl: "noop"},
		func(ctx context.Context, prompt string) (string, error) {
			return `X is a no-op.

---CONTRACT---
{"clauses":[{"id":"c1","kind":"invariant","body":"idempotent","source":"intent"}]}`, nil
		})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.Description != "X is a no-op." {
		t.Errorf("description: %q", out.Description)
	}
	if len(out.Contract) != 1 || out.Contract[0].Kind != "invariant" {
		t.Errorf("contract: %+v", out.Contract)
	}
}

// TestDescribeWithInvoker_PromptMentionsContractSentinel — the prompt
// builder must instruct the LLM to emit the sentinel. Guards against
// future refactor accidentally dropping the instruction line.
func TestDescribeWithInvoker_PromptMentionsContractSentinel(t *testing.T) {
	captured := ""
	_, _ = DescribeWithInvoker(context.Background(),
		DescribeInputs{ObjectID: "X", Impl: "noop"},
		func(ctx context.Context, prompt string) (string, error) {
			captured = prompt
			return "desc", nil
		})
	if !strings.Contains(captured, "---CONTRACT---") {
		t.Errorf("prompt missing sentinel instruction, agent will skip contract emission")
	}
}
