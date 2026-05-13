package review

import "github.com/creator915/Koncept_OS/internal/typecalc/core"

import "testing"

// TestRuntimeCheck_SparseTraceFires — synthesised tests have many
// appendTrace calls, but the trace only records 1 call. The sparse
// detective rule should fire.
func TestRuntimeCheck_SparseTraceFires(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	g := newGraphWithRangeAttrs()

	// Pretend the synthesizer wrote 8 appendTrace lines — one per test.
	if err := core.WriteTests(&core.TestsEvidence{
		ObjectID: "Move",
		Lang:     "JavaScript",
		TestCode: "test('a', () => { call(); appendTrace({}, {}); assert(); });\n" +
			"test('b', () => { call(); appendTrace({}, {}); assert(); });\n" +
			"test('c', () => { call(); appendTrace({}, {}); assert(); });\n" +
			"test('d', () => { call(); appendTrace({}, {}); assert(); });\n" +
			"test('e', () => { call(); appendTrace({}, {}); assert(); });\n" +
			"test('f', () => { call(); appendTrace({}, {}); assert(); });\n" +
			"test('g', () => { call(); appendTrace({}, {}); assert(); });\n" +
			"test('h', () => { call(); appendTrace({}, {}); assert(); });\n",
		SpecHash: "x",
	}); err != nil {
		t.Fatal(err)
	}
	// Trace has only ONE call recorded — most tests crashed before append.
	writeTrace(t, "Move", []map[string]any{
		{"inputs": map[string]any{"speed": 5.0}, "outputs": map[string]any{"dir": "left"}},
	})

	issues := RuntimeCheck(g, "Move").Issues()
	if !hasIssue(issues, "runtime-trace-sparse") {
		t.Fatalf("expected runtime-trace-sparse, got %v", issues)
	}
}

// TestRuntimeCheck_SparseDoesNotFireOnSmallSuites — for tiny test
// suites (1 expected call), it's normal not to have multiples; we
// don't flag those.
func TestRuntimeCheck_SparseQuietForSingleAppendTrace(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	g := newGraphWithRangeAttrs()

	if err := core.WriteTests(&core.TestsEvidence{
		ObjectID: "Move",
		Lang:     "JavaScript",
		TestCode: "test('only', () => { call(); appendTrace({}, {}); assert(); });\n",
		SpecHash: "x",
	}); err != nil {
		t.Fatal(err)
	}
	writeTrace(t, "Move", []map[string]any{
		{"inputs": map[string]any{"speed": 5.0}, "outputs": map[string]any{"dir": "left"}},
	})

	issues := RuntimeCheck(g, "Move").Issues()
	for _, is := range issues {
		if is.Code == "runtime-trace-sparse" {
			t.Fatalf("should not fire for single-test suite, got %v", is)
		}
	}
}

// TestRuntimeCheck_SparseQuietWhenAllCallsRecorded — ratio above
// threshold ⇒ no sparse warning.
func TestRuntimeCheck_SparseQuietWhenAllCallsRecorded(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	g := newGraphWithRangeAttrs()
	if err := core.WriteTests(&core.TestsEvidence{
		ObjectID: "Move",
		Lang:     "JavaScript",
		TestCode: "appendTrace({}, {});\nappendTrace({}, {});\nappendTrace({}, {});\nappendTrace({}, {});\n",
		SpecHash: "x",
	}); err != nil {
		t.Fatal(err)
	}
	writeTrace(t, "Move", []map[string]any{
		{"inputs": map[string]any{"speed": 1.0}, "outputs": map[string]any{"dir": "left"}},
		{"inputs": map[string]any{"speed": 2.0}, "outputs": map[string]any{"dir": "left"}},
		{"inputs": map[string]any{"speed": 3.0}, "outputs": map[string]any{"dir": "right"}},
		{"inputs": map[string]any{"speed": 4.0}, "outputs": map[string]any{"dir": "right"}},
	})
	issues := RuntimeCheck(g, "Move").Issues()
	for _, is := range issues {
		if is.Code == "runtime-trace-sparse" {
			t.Fatalf("should not fire when ratio is 100%%, got %v", is)
		}
	}
}
