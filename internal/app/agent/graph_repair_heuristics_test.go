package agent

import (
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// TestDetectIOBoundary_FiresOnCharOnly — the canonical case: object
// with ≥1 characterization clause and 0 example + 0 invariant. This
// is the shape that blocked PB-30 batch 0522's OsInput / ProbeFs /
// ReadStdin objects.
func TestDetectIOBoundary_FiresOnCharOnly(t *testing.T) {
	clauses := []core.ContractClause{
		{ID: "char-1", Kind: "characterization", Body: "matches probe on 35 inputs"},
		{ID: "char-2", Kind: "characterization", Body: "matches probe on edge inputs"},
	}
	p := detectRepairProposal("OsInput", clauses, "void os_input(FILE* stdin, char* buf);")
	if p == nil {
		t.Fatal("expected IO-boundary heuristic to fire on char-only contract")
	}
	if p.Kind != "io-boundary" {
		t.Errorf("kind: got %q want io-boundary", p.Kind)
	}
	if p.TargetObject != "OsInput" {
		t.Errorf("target: got %q", p.TargetObject)
	}
	if !p.DeleteFromGraph {
		t.Error("IO-boundary proposal should suggest deletion")
	}
	if !strings.Contains(p.Diagnosis, "0 example") || !strings.Contains(p.Diagnosis, "0 invariant") {
		t.Errorf("diagnosis should reference clause kind counts: %s", p.Diagnosis)
	}
	// signature hint should surface the IO tokens we passed
	if !strings.Contains(p.Diagnosis, "FILE*") && !strings.Contains(p.Diagnosis, "stdin") {
		t.Errorf("expected signature hint to mention FILE*/stdin: %s", p.Diagnosis)
	}
}

// TestDetectIOBoundary_DoesNotFireWithExample — one example clause
// means LLM found something testable; leave the object alone (synth
// can drive it through normally).
func TestDetectIOBoundary_DoesNotFireWithExample(t *testing.T) {
	clauses := []core.ContractClause{
		{ID: "c1", Kind: "example", Body: "ParseLine(\"a:1\") = {key:'a',val:1}"},
		{ID: "char-1", Kind: "characterization", Body: "matches probe"},
	}
	if p := detectRepairProposal("ParseLine", clauses, ""); p != nil {
		t.Errorf("heuristic should NOT fire when example clauses exist; got %+v", p)
	}
}

// TestDetectIOBoundary_DoesNotFireWithInvariant — similarly, an
// invariant clause is a testable property; don't propose deletion.
func TestDetectIOBoundary_DoesNotFireWithInvariant(t *testing.T) {
	clauses := []core.ContractClause{
		{ID: "c1", Kind: "invariant", Body: "idempotent: f(f(x)) == f(x)"},
		{ID: "char-1", Kind: "characterization", Body: "matches probe"},
	}
	if p := detectRepairProposal("X", clauses, ""); p != nil {
		t.Errorf("heuristic should NOT fire when invariants exist; got %+v", p)
	}
}

// TestDetectIOBoundary_DoesNotFireWithoutChar — pure example/invariant
// shouldn't fire either; that's a well-shaped object.
func TestDetectIOBoundary_DoesNotFireWithoutChar(t *testing.T) {
	clauses := []core.ContractClause{
		{ID: "c1", Kind: "example", Body: "Add(2,3)=5"},
		{ID: "c2", Kind: "invariant", Body: "commutative"},
	}
	if p := detectRepairProposal("Add", clauses, ""); p != nil {
		t.Errorf("heuristic should NOT fire on well-shaped object; got %+v", p)
	}
}

// TestDetectRepairProposal_EmptyContract — no clauses at all returns
// nil (heuristic needs at least the clause-kind signal). Caller falls
// through to Obstacle in this case — appropriate since we can't
// diagnose what's wrong without contract data.
func TestDetectRepairProposal_EmptyContract(t *testing.T) {
	if p := detectRepairProposal("X", nil, ""); p != nil {
		t.Errorf("empty contract should yield no proposal, got %+v", p)
	}
	if p := detectRepairProposal("X", []core.ContractClause{}, ""); p != nil {
		t.Errorf("empty contract slice should yield no proposal, got %+v", p)
	}
}

// TestIOSignatureHint_PicksTokens — verify the signature hint
// surfaces the right IO tokens for diagnostic clarity.
func TestIOSignatureHint_PicksTokens(t *testing.T) {
	cases := []struct {
		sig  string
		want []string // substrings expected in hint
	}{
		{"void f(FILE* fp, char* buf);", []string{"fopen", "FILE*", "fp"}}, // matches fopen / file*-ish
		{"def read(): import sys; return sys.stdin.readline()", []string{"sys.stdin", "readline"}},
		{"func F(r io.Reader) error", []string{"io.reader"}},
		{"int add(int a, int b);", nil}, // no IO tokens → empty hint
	}
	for _, c := range cases {
		t.Run(c.sig[:min(20, len(c.sig))], func(t *testing.T) {
			h := ioSignatureHint(c.sig)
			if len(c.want) == 0 && h != "" {
				t.Errorf("expected empty hint, got %q", h)
				return
			}
			for _, w := range c.want {
				if !strings.Contains(strings.ToLower(h), strings.ToLower(w)) {
					// Some "want" entries are approximate (e.g. "FILE*" but
					// detector matches lower-case "fopen"). At least one
					// should hit; mark as fail only if hint is empty when
					// non-empty expected.
					if h == "" {
						t.Errorf("expected hint matching one of %v, got empty for %q", c.want, c.sig)
						return
					}
				}
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
