package typecalc

import (
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

func TestEvidenceRoundTrip_TestsEvidence(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	rec := &TestsEvidence{
		ObjectID: "Foo",
		Lang:     "Go",
		TestCode: "func TestFoo(t *testing.T) {}",
		SpecHash: HashSource("source v1"),
	}
	if err := WriteTests(rec); err != nil {
		t.Fatalf("write: %v", err)
	}
	back, ok := ReadTests("Foo")
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

	rec := &TestsEvidence{
		ObjectID: "Foo",
		Lang:     "JavaScript",
		SpecHash: HashSource("v1"),
		Cases: []TestCase{
			{
				Name: "happy",
				Setup: []SetupOp{
					{Set: "x", Value: []byte("5")},
				},
				Call: "Foo(x)",
				Expect: []Expectation{
					{Port: "y", Equals: []byte("10")},
				},
			},
		},
	}
	if err := WriteTests(rec); err != nil {
		t.Fatalf("write: %v", err)
	}
	back, ok := ReadTests("Foo")
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
