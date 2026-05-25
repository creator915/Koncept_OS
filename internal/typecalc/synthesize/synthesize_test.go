package synthesize

import (
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
	"context"
	"strings"
	"testing"
)

// TestSynthesizeWithInvoker_LegacyTestCode — when the LLM returns a
// non-JSON reply, the helper falls back to the legacy raw-test-code
// path. (This was the only mode in the original design; preserving it
// for languages without a harness.)
func TestSynthesizeWithInvoker_LegacyTestCode(t *testing.T) {
	captured := ""
	out, err := SynthesizeWithInvoker(context.Background(),
		SynthesizeInputs{
			ObjectID:    "Reverse",
			Lang:        "Go",
			Intent:      "reverse runes preserving UTF-8",
			Description: "in-place rune swap",
			Signature:   "func Reverse(s string) string",
			Consumes:    []string{"input_string"},
			Produces:    []string{"output_string"},
		},
		func(ctx context.Context, prompt string) (string, error) {
			captured = prompt
			return `package x
import "testing"
func TestReverse(t *testing.T) {}
`, nil
		})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out.Cases) != 0 {
		t.Fatalf("expected 0 cases for legacy reply, got %d", len(out.Cases))
	}
	if !strings.Contains(out.TestCode, "TestReverse") {
		t.Fatalf("expected raw test code, got: %q", out.TestCode)
	}
	if !strings.Contains(captured, "input_string") || !strings.Contains(captured, "output_string") {
		t.Fatalf("port names not in prompt: %s", captured)
	}
}

// TestSynthesizeWithInvoker_SchemaCases — when the LLM returns the
// new structured-cases JSON, we get parsed Cases back.
func TestSynthesizeWithInvoker_SchemaCases(t *testing.T) {
	out, err := SynthesizeWithInvoker(context.Background(),
		SynthesizeInputs{ObjectID: "X", Lang: "JavaScript"},
		func(ctx context.Context, prompt string) (string, error) {
			return `{"objectId":"X","cases":[
				{"name":"happy","call":"X()","expect":[{"port":"y","equals":1}]},
				{"name":"edge","call":"X()","expect":[{"port":"y","between":[0,10]}]}
			]}`, nil
		})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out.Cases) != 2 {
		t.Fatalf("expected 2 cases, got %d", len(out.Cases))
	}
	if out.Cases[0].Name != "happy" {
		t.Fatalf("first case name: %q", out.Cases[0].Name)
	}
	if out.Cases[1].Expect[0].Between == nil {
		t.Fatalf("between comparator lost")
	}
}

// TestSynthesizeWithInvoker_StripsCodeFences — even with the new
// JSON output, defensive fence-stripping still works for sloppy LLMs.
func TestSynthesizeWithInvoker_StripsCodeFences(t *testing.T) {
	out, err := SynthesizeWithInvoker(context.Background(),
		SynthesizeInputs{ObjectID: "X"},
		func(ctx context.Context, prompt string) (string, error) {
			return "```json\n{\"objectId\":\"X\",\"cases\":[{\"name\":\"a\",\"call\":\"f()\"}]}\n```", nil
		})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out.Cases) != 1 {
		t.Fatalf("expected 1 case, got %d", len(out.Cases))
	}
}

func TestSynthesizeWithInvoker_RejectsEmpty(t *testing.T) {
	_, err := SynthesizeWithInvoker(context.Background(),
		SynthesizeInputs{ObjectID: "X"},
		func(ctx context.Context, prompt string) (string, error) {
			return "   ", nil
		})
	if err == nil {
		t.Fatal("expected error for empty test code")
	}
}

func TestSynthesizeWithInvoker_PassesThroughCannotSynthesize(t *testing.T) {
	out, err := SynthesizeWithInvoker(context.Background(),
		SynthesizeInputs{ObjectID: "X"},
		func(ctx context.Context, prompt string) (string, error) {
			return "CANNOT_SYNTHESIZE\nsignature too underspecified", nil
		})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.HasPrefix(out.TestCode, "CANNOT_SYNTHESIZE") {
		t.Fatalf("token not preserved: %q", out.TestCode)
	}
	if len(out.Cases) != 0 {
		t.Fatalf("CANNOT_SYNTHESIZE should not produce cases")
	}
}

// TestSynthesizeWithInvoker_FixModeRendersPriorCodeAndFailure — when
// PreviousTestCode + PreviousFailure are set, the prompt must include a
// FIX MODE section carrying both verbatim so the LLM can diff in place.
// Empty pair means cold synth — no FIX MODE section.
func TestSynthesizeWithInvoker_FixModeRendersPriorCodeAndFailure(t *testing.T) {
	captured := ""
	_, _ = SynthesizeWithInvoker(context.Background(),
		SynthesizeInputs{
			ObjectID:         "Reverse",
			Lang:             "Go",
			Intent:           "reverse runes",
			Description:      "in-place",
			PreviousTestCode: "func TestReverse(t *testing.T){ /* prior body */ }",
			PreviousFailure:  "FAIL TestReverse: expected \"cba\", got \"abc\"",
		},
		func(ctx context.Context, prompt string) (string, error) {
			captured = prompt
			return `{"objectId":"Reverse","testCode":"func TestReverse(t *testing.T){ /* fixed */ }"}`, nil
		})
	if !strings.Contains(captured, "FIX MODE") {
		t.Fatalf("FIX MODE header missing from prompt:\n%s", captured)
	}
	if !strings.Contains(captured, "prior body") {
		t.Fatalf("prior testCode body missing from FIX MODE section")
	}
	if !strings.Contains(captured, "expected \"cba\"") {
		t.Fatalf("prior failure missing from FIX MODE section")
	}
}

func TestSynthesizeWithInvoker_NoFixModeWhenPriorEmpty(t *testing.T) {
	captured := ""
	_, _ = SynthesizeWithInvoker(context.Background(),
		SynthesizeInputs{ObjectID: "X", Lang: "Go"},
		func(ctx context.Context, prompt string) (string, error) {
			captured = prompt
			return `func TestX(t *testing.T){}`, nil
		})
	if strings.Contains(captured, "FIX MODE") {
		t.Fatalf("cold synth must not include FIX MODE section")
	}
}

// TestFixModeOn_RequiresBothFields — partial pair (only code, only
// failure, or whitespace-only) must NOT trigger fix mode; otherwise the
// LLM would see "minimize changes" while having nothing to anchor to.
func TestFixModeOn_RequiresBothFields(t *testing.T) {
	cases := []struct {
		name string
		in   SynthesizeInputs
		want bool
	}{
		{"both empty", SynthesizeInputs{}, false},
		{"only code", SynthesizeInputs{PreviousTestCode: "x"}, false},
		{"only failure", SynthesizeInputs{PreviousFailure: "y"}, false},
		{"both whitespace", SynthesizeInputs{PreviousTestCode: "  ", PreviousFailure: "\t"}, false},
		{"both set", SynthesizeInputs{PreviousTestCode: "x", PreviousFailure: "y"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fixModeOn(c.in); got != c.want {
				t.Errorf("fixModeOn(%+v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestEvidenceRoundTrip_SpecContract (Step 1 of contract landing) —
// the new Contract field on SpecEvidence must round-trip through
// WriteSpec → ReadSpec without loss, including all three clause kinds
// and the optional flag. Legacy specs (no Contract) must still load.
func TestEvidenceRoundTrip_SpecContract(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	rec := &core.SpecEvidence{
		ObjectID:    "Adder",
		Description: "Adds two ints, panics on overflow.",
		SourceHash:  core.HashSource("src v1"),
		Contract: []core.ContractClause{
			{ID: "c1", Kind: "example", Body: "Add(2,3) = 5", Source: "spec:S§1"},
			{ID: "c2", Kind: "invariant", Body: "Add(a,b) == Add(b,a) for all a,b in range", Source: "spec:S§2"},
			{ID: "c3", Kind: "characterization", Body: "Add(MAX_INT,1) panics with 'overflow'", Source: "char:probe_5"},
			{ID: "c4", Kind: "example", Body: "Add(-1,1) = 0", Source: "spec:S§1", Optional: true},
		},
	}
	if err := core.WriteSpec(rec); err != nil {
		t.Fatalf("write: %v", err)
	}
	back, ok := core.ReadSpec("Adder")
	if !ok {
		t.Fatal("read missed")
	}
	if len(back.Contract) != 4 {
		t.Fatalf("contract length: got %d want 4: %+v", len(back.Contract), back.Contract)
	}
	for i, want := range rec.Contract {
		got := back.Contract[i]
		if got.ID != want.ID || got.Kind != want.Kind || got.Body != want.Body ||
			got.Source != want.Source || got.Optional != want.Optional {
			t.Errorf("clause[%d] mismatch:\n got=%+v\nwant=%+v", i, got, want)
		}
	}
}

// TestEvidenceRoundTrip_SpecLegacyNoContract — bundles written before
// the Contract field existed (or by tools that don't populate it) must
// still load cleanly with Contract == nil. Guards against accidental
// `len(nil)` panics or unmarshal errors when reading old data.
func TestEvidenceRoundTrip_SpecLegacyNoContract(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	rec := &core.SpecEvidence{
		ObjectID:    "Legacy",
		Description: "no contract here",
		SourceHash:  core.HashSource("v1"),
	}
	if err := core.WriteSpec(rec); err != nil {
		t.Fatalf("write: %v", err)
	}
	back, ok := core.ReadSpec("Legacy")
	if !ok {
		t.Fatal("read missed")
	}
	if back.Contract != nil {
		t.Errorf("expected nil Contract for legacy, got %+v", back.Contract)
	}
	if back.Description != "no contract here" {
		t.Errorf("Description corrupted: %q", back.Description)
	}
}

func TestEvidenceRoundTrip_TestsEvidence(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	rec := &core.TestsEvidence{
		ObjectID: "Foo",
		Lang:     "Go",
		TestCode: "func TestFoo(t *testing.T) {}",
		SpecHash: core.HashSource("source v1"),
	}
	if err := core.WriteTests(rec); err != nil {
		t.Fatalf("write: %v", err)
	}
	back, ok := core.ReadTests("Foo")
	if !ok {
		t.Fatal("read missed")
	}
	if back.Kind != "tests" || back.TestCode != rec.TestCode {
		t.Fatalf("round-trip broken: %+v", back)
	}
}

// TestEvidenceRoundTrip_SchemaCases — round-trip the new Cases field.
func TestEvidenceRoundTrip_SchemaCases(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	rec := &core.TestsEvidence{
		ObjectID: "Foo",
		Lang:     "JavaScript",
		SpecHash: core.HashSource("v1"),
		Cases: []core.TestCase{
			{
				Name: "happy",
				Setup: []core.SetupOp{
					{Set: "x", Value: []byte("5")},
				},
				Call: "Foo(x)",
				Expect: []core.Expectation{
					{Port: "y", Equals: []byte("10")},
				},
			},
		},
	}
	if err := core.WriteTests(rec); err != nil {
		t.Fatalf("write: %v", err)
	}
	back, ok := core.ReadTests("Foo")
	if !ok {
		t.Fatal("read missed")
	}
	if len(back.Cases) != 1 || back.Cases[0].Name != "happy" {
		t.Fatalf("cases lost: %+v", back.Cases)
	}
	if back.Cases[0].Setup[0].Set != "x" {
		t.Fatalf("setup lost")
	}
}
