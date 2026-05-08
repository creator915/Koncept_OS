package typecalc

import (
	"context"
	"strings"
	"testing"
)

func TestParseReviewReply_BareJSON(t *testing.T) {
	v, err := parseReviewReply(`{"verdict":"pass","reasons":["matches intent"],"confidence":0.9}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Verdict != "pass" || len(v.Reasons) != 1 || v.Reasons[0] != "matches intent" {
		t.Fatalf("bad parse: %+v", v)
	}
	if v.Confidence < 0.85 || v.Confidence > 0.95 {
		t.Fatalf("confidence %f", v.Confidence)
	}
}

func TestParseReviewReply_MarkdownFences(t *testing.T) {
	v, err := parseReviewReply("```json\n{\"verdict\":\"fail\",\"reasons\":[\"missing edge case\"],\"confidence\":0.6}\n```")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if v.Verdict != "fail" {
		t.Fatalf("verdict=%q", v.Verdict)
	}
}

func TestParseReviewReply_ChatterBeforeJSON(t *testing.T) {
	v, err := parseReviewReply(`Sure! Here is my verdict:

{"verdict":"pass","reasons":["fine"],"confidence":0.7}

Let me know if you need anything else.`)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if v.Verdict != "pass" {
		t.Fatalf("verdict=%q", v.Verdict)
	}
}

func TestParseReviewReply_NoJSON(t *testing.T) {
	_, err := parseReviewReply("I think it's pass.")
	if err == nil {
		t.Fatal("expected error for non-JSON reply")
	}
}

func TestReviewWithInvoker_HappyPath(t *testing.T) {
	v, err := ReviewWithInvoker(context.Background(),
		ReviewInputs{ObjectID: "X", Intent: "do X"},
		func(ctx context.Context, prompt string) (string, error) {
			// Inject a deterministic LLM reply.
			return `{"verdict":"pass","reasons":["does X"],"confidence":0.8}`, nil
		})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if v.Verdict != "pass" {
		t.Fatalf("verdict=%q", v.Verdict)
	}
}

func TestReviewWithInvoker_RejectsInvalidVerdict(t *testing.T) {
	_, err := ReviewWithInvoker(context.Background(),
		ReviewInputs{ObjectID: "X"},
		func(ctx context.Context, prompt string) (string, error) {
			return `{"verdict":"maybe","reasons":[],"confidence":0.5}`, nil
		})
	if err == nil {
		t.Fatal("expected error for invalid verdict")
	}
	if !strings.Contains(err.Error(), "invalid verdict") {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestReviewWithInvoker_PassesObjectIDIntoPrompt(t *testing.T) {
	captured := ""
	_, _ = ReviewWithInvoker(context.Background(),
		ReviewInputs{ObjectID: "GuessNumber", Intent: "play game"},
		func(ctx context.Context, prompt string) (string, error) {
			captured = prompt
			return `{"verdict":"pass","reasons":["ok"],"confidence":0.7}`, nil
		})
	if !strings.Contains(captured, "GuessNumber") {
		t.Fatalf("prompt missing object id: %s", captured)
	}
	if !strings.Contains(captured, "play game") {
		t.Fatalf("prompt missing intent: %s", captured)
	}
}

func TestDescribeWithInvoker_PassesContent(t *testing.T) {
	captured := ""
	desc, err := DescribeWithInvoker(context.Background(),
		DescribeInputs{ObjectID: "F", Impl: "func F() {}"},
		func(ctx context.Context, prompt string) (string, error) {
			captured = prompt
			return "F is a no-op function.", nil
		})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if desc != "F is a no-op function." {
		t.Fatalf("desc=%q", desc)
	}
	if !strings.Contains(captured, "F()") {
		t.Fatalf("prompt missing impl: %s", captured)
	}
}

func TestDescribeWithInvoker_RejectsEmpty(t *testing.T) {
	_, err := DescribeWithInvoker(context.Background(),
		DescribeInputs{ObjectID: "F"},
		func(ctx context.Context, prompt string) (string, error) {
			return "   ", nil
		})
	if err == nil {
		t.Fatal("expected error for empty description")
	}
}

func TestEvidenceRoundTrip_SpecAndAccepted(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	spec := &SpecEvidence{
		ObjectID:    "Foo",
		Description: "does foo things",
		SourceHash:  HashSource("source content"),
	}
	if err := WriteSpec(spec); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	back, ok := ReadSpec("Foo")
	if !ok {
		t.Fatal("read spec missed")
	}
	if back.Kind != "spec" || back.Description != "does foo things" {
		t.Fatalf("round-trip lost data: %+v", back)
	}
	if back.SourceHash != spec.SourceHash {
		t.Fatalf("hash drift: want %s got %s", spec.SourceHash, back.SourceHash)
	}

	accepted := &AcceptedEvidence{
		ObjectID: "Foo",
		OK:       false,
		StaticIssues: []StaticIssue{
			{Code: "test-code", Where: "Foo", Message: "x"},
		},
		Reasonableness: ReviewVerdict{
			Verdict: "fail", Reasons: []string{"y"}, Confidence: 0.3,
		},
	}
	if err := WriteAccepted(accepted); err != nil {
		t.Fatalf("write accepted: %v", err)
	}
	back2, ok := ReadAccepted("Foo")
	if !ok {
		t.Fatal("read accepted missed")
	}
	if back2.Kind != "accepted" || back2.OK {
		t.Fatalf("round-trip wrong: %+v", back2)
	}
	if len(back2.StaticIssues) != 1 || back2.Reasonableness.Verdict != "fail" {
		t.Fatalf("nested data lost: %+v", back2)
	}
}
