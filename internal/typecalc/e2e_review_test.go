package typecalc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEndToEnd_DescribeReviewAccept exercises the full describe → static
// check → review → accept pipeline in one test, with stub LLM invokers
// so it runs offline. This is the highest-confidence integration check
// short of an actual API round-trip.
func TestEndToEnd_DescribeReviewAccept(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	// 1. Set up a fixture: graph with one object Op, an impl file, and
	//    an attribute it produces (with a non-empty valueSpace).
	g := newTestGraph()
	g.Attributes["data_out"].ValueSpace = map[string]any{"shape": "string"}

	implContent := "package main\n\nfunc Op(in string) string {\n  return strings.ToUpper(in)\n}\n"
	if err := os.MkdirAll("src", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("src/Process.impl.go", []byte(implContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("defs", 0o755); err != nil {
		t.Fatal(err)
	}
	defContent := "package defs\n\ntype Op = func(string) string\n"
	if err := os.WriteFile("defs/Process.go", []byte(defContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. Lay down the compile/test evidence the describe step expects.
	if err := RecordEvidence("Process", "test", "Go", true); err != nil {
		t.Fatal(err)
	}

	// 3. typecalc_describe equivalent — call DescribeWithInvoker with a
	//    canned LLM reply.
	desc, err := DescribeWithInvoker(context.Background(),
		DescribeInputs{
			ObjectID:  "Process",
			Intent:    g.Objects["Process"].Intent,
			Signature: defContent,
			Impl:      implContent,
		},
		stubInvoker("Process consumes a string, applies strings.ToUpper, returns the resulting upper-cased string. No side effects, no I/O, deterministic."))
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	specRec := &SpecEvidence{
		ObjectID:    "Process",
		Description: desc,
		SourceHash:  HashSource(implContent),
	}
	if err := WriteSpec(specRec); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	// 4. Static check — should now pass with everything in place.
	issues := StaticCheck(".", g, "Process")
	if len(issues) != 0 {
		t.Fatalf("static check expected 0 issues, got %d:\n%v", len(issues), issues)
	}

	// 5. Reasonableness review — stub returns a passing verdict.
	verdict, err := ReviewWithInvoker(context.Background(),
		ReviewInputs{
			ObjectID:    "Process",
			Intent:      g.Objects["Process"].Intent,
			Description: desc,
			Signature:   defContent,
			Impl:        implContent,
			TestCode:    "func TestOp(t *testing.T) { ... }",
			TestLog:     "PASS",
		},
		stubInvoker(`{"verdict":"pass","reasons":["matches intent: transform input to output"],"confidence":0.85}`))
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if verdict.Verdict != "pass" {
		t.Fatalf("expected pass, got %q", verdict.Verdict)
	}

	// 6. Persist accepted evidence.
	rec := &AcceptedEvidence{
		ObjectID:       "Process",
		OK:             len(issues) == 0 && verdict.Verdict == "pass",
		StaticIssues:   issues,
		Reasonableness: verdict,
		SourceHash:     HashSource(implContent),
		SpecHash:       specRec.SourceHash,
	}
	if err := WriteAccepted(rec); err != nil {
		t.Fatalf("write accepted: %v", err)
	}

	// 7. Read back and verify on-disk state.
	back, ok := ReadAccepted("Process")
	if !ok {
		t.Fatal("accepted evidence missing after write")
	}
	if !back.OK {
		t.Fatalf("expected ok=true, got %+v", back)
	}
	if back.Kind != "accepted" {
		t.Fatalf("kind=%q", back.Kind)
	}
	if back.Reasonableness.Verdict != "pass" {
		t.Fatalf("verdict=%q", back.Reasonableness.Verdict)
	}
	if back.SourceHash != HashSource(implContent) {
		t.Fatalf("source hash mismatch")
	}

	// 8. Verify the file actually exists on disk where the gate expects.
	expectedPath := filepath.Join(EvidenceDir, "Process.accepted.json")
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("accepted evidence not at expected path %s: %v", expectedPath, err)
	}
}

// TestEndToEnd_StaleSpecBlocksReview demonstrates the stale-detection
// path: an impl edited after describe ran fails the static check, so
// the agent must re-describe before review can succeed.
func TestEndToEnd_StaleSpecBlocksReview(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := newTestGraph()
	g.Attributes["data_out"].ValueSpace = map[string]any{"shape": "string"}

	if err := os.MkdirAll("src", 0o755); err != nil {
		t.Fatal(err)
	}
	originalImpl := "package main\nfunc Op(s string) string { return s }\n"
	if err := os.WriteFile("src/Process.impl.go", []byte(originalImpl), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RecordEvidence("Process", "test", "Go", true); err != nil {
		t.Fatal(err)
	}

	// Describe against the original impl.
	if err := WriteSpec(&SpecEvidence{
		ObjectID:    "Process",
		Description: "noop",
		SourceHash:  HashSource(originalImpl),
	}); err != nil {
		t.Fatal(err)
	}

	// Now mutate impl WITHOUT re-running describe.
	editedImpl := originalImpl + "// added behavior\n"
	if err := os.WriteFile("src/Process.impl.go", []byte(editedImpl), 0o644); err != nil {
		t.Fatal(err)
	}

	issues := StaticCheck(".", g, "Process")
	stale := false
	for _, is := range issues {
		if is.Code == "spec-stale" {
			stale = true
		}
	}
	if !stale {
		t.Fatalf("expected spec-stale issue after impl edit, got %v", issues)
	}
}

// TestEndToEnd_BadVerdictRecorded verifies that a failing verdict still
// produces an on-disk record so the gate has actionable evidence.
func TestEndToEnd_BadVerdictRecorded(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	verdict, err := ReviewWithInvoker(context.Background(),
		ReviewInputs{ObjectID: "X"},
		stubInvoker(`{"verdict":"fail","reasons":["does not match intent","missing edge case"],"confidence":0.7}`))
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if verdict.Verdict != "fail" {
		t.Fatalf("verdict=%q", verdict.Verdict)
	}

	rec := &AcceptedEvidence{
		ObjectID:       "X",
		OK:             false,
		Reasonableness: verdict,
	}
	if err := WriteAccepted(rec); err != nil {
		t.Fatal(err)
	}
	back, ok := ReadAccepted("X")
	if !ok {
		t.Fatal("missed read")
	}
	if back.OK {
		t.Fatal("OK should be false")
	}
	if !strings.Contains(strings.Join(back.Reasonableness.Reasons, ";"), "missing edge case") {
		t.Fatalf("reasons not preserved: %v", back.Reasonableness.Reasons)
	}
}

// stubInvoker returns an Invoker that always replies with the given
// canned text, regardless of prompt.
func stubInvoker(reply string) Invoker {
	return func(ctx context.Context, prompt string) (string, error) {
		return reply, nil
	}
}
