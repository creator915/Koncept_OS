package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Phase 6 e2e: simulate a failed branch (vacuous-oracle obstacle),
// rollback, synthesize lesson, verify it has the right heuristic
// signal AND is round-trippable on disk.
func TestSynthesizeLesson_HeuristicVacuousOracle(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)

	// Build a failed branch: a tool call + an outer transition to
	// Obstacle whose reason names "vacuous-oracle-guard".
	_, _ = s.Append(EventTypeToolExec, ToolExecEvent{
		Tool: "confirm_object", Args: json.RawMessage(`{"object_id":"Foo"}`),
	})
	obstaclePayload, _ := json.Marshal(map[string]interface{}{
		"phase":   "confirm_one",
		"reasons": []string{"vacuous-oracle-guard: /workspace/executable hash matches reference"},
	})
	_, _ = s.Append(EventTypeOuterTransition, OuterTransitionEvent{
		From:    "Outer.SomeConfirmed",
		To:      "Outer.Obstacle",
		Payload: obstaclePayload,
	})

	// Set the branch ref by hand (Rollback would, but here we just
	// want to test the lesson path independently).
	tip, _ := s.Tip()
	_ = s.Refs.Set("attempt/1", tip)

	// Synthesize without LLM — heuristic should match.
	lesson, err := s.SynthesizeLesson("attempt/1", nil)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if !strings.HasPrefix(lesson.GeneratedBy, "heuristic:vacuous-oracle") {
		t.Errorf("expected heuristic:vacuous-oracle key, got %q", lesson.GeneratedBy)
	}
	if !strings.Contains(lesson.WhatWentWrong, "compile") {
		t.Errorf("vacuous-oracle lesson must mention compile as the fix path: %q", lesson.WhatWentWrong)
	}
	if len(lesson.DosAndDonts) == 0 {
		t.Error("heuristic match must produce DosAndDonts")
	}

	// Round-trip: write + read.
	if err := s.WriteLesson(lesson); err != nil {
		t.Fatalf("write lesson: %v", err)
	}
	lessons, err := s.ReadLessons()
	if err != nil {
		t.Fatal(err)
	}
	if len(lessons) != 1 {
		t.Fatalf("expected 1 lesson, got %d", len(lessons))
	}
	if lessons[0].BranchRef != "attempt/1" {
		t.Errorf("BranchRef lost in round-trip: got %q", lessons[0].BranchRef)
	}
	if lessons[0].GeneratedBy != lesson.GeneratedBy {
		t.Errorf("GeneratedBy lost in round-trip: got %q want %q", lessons[0].GeneratedBy, lesson.GeneratedBy)
	}

	// Verify the markdown file on disk has the right shape.
	body, err := os.ReadFile(filepath.Join(srcDir, ".kcpos", "snapshots", "lessons", "attempt-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	str := string(body)
	for _, want := range []string{
		"---",                     // frontmatter delim
		"branchRef: attempt/1",
		"# Failure record",
		"## What went wrong",
		"## Do this on retry",
	} {
		if !strings.Contains(str, want) {
			t.Errorf("lesson markdown missing %q\n--- body ---\n%s", want, str)
		}
	}
}

func TestSynthesizeLesson_HeuristicMethodUseRule(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)
	_, _ = s.Append(EventTypeToolExec, ToolExecEvent{Tool: "confirm"})
	obstaclePayload, _ := json.Marshal(map[string]interface{}{
		"phase":   "gate",
		"reasons": []string{"gate FAIL", "✗ [method-use-rule] object Foo artifact changed since characterization"},
	})
	_, _ = s.Append(EventTypeOuterTransition, OuterTransitionEvent{
		From: "Outer.Checkpointed", To: "Outer.Obstacle", Payload: obstaclePayload,
	})
	tip, _ := s.Tip()
	_ = s.Refs.Set("attempt/1", tip)

	lesson, err := s.SynthesizeLesson("attempt/1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(lesson.GeneratedBy, "heuristic:method-use-rule") {
		t.Errorf("expected method-use-rule heuristic, got %q", lesson.GeneratedBy)
	}
	// Must mention re-characterize as the fix.
	full := lesson.WhatWentWrong + " " + strings.Join(lesson.DosAndDonts, " ")
	if !strings.Contains(strings.ToLower(full), "characteriz") {
		t.Errorf("method-use-rule lesson must mention characterize: %q", full)
	}
}

func TestSynthesizeLesson_HeuristicMonolithic(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)
	_, _ = s.Append(EventTypeToolExec, ToolExecEvent{Tool: "graph_create_object"})
	obstaclePayload, _ := json.Marshal(map[string]interface{}{
		"phase":   "graph_declare",
		"reasons": []string{"reconstruction mode (./probe in cwd) declared only 1 object(s)"},
	})
	_, _ = s.Append(EventTypeOuterTransition, OuterTransitionEvent{
		From: "Outer.Architecture", To: "Outer.Obstacle", Payload: obstaclePayload,
	})
	tip, _ := s.Tip()
	_ = s.Refs.Set("attempt/1", tip)

	lesson, _ := s.SynthesizeLesson("attempt/1", nil)
	if lesson.GeneratedBy != "heuristic:monolithic-decomposition" {
		t.Errorf("expected monolithic heuristic, got %q", lesson.GeneratedBy)
	}
}

// Heuristic that doesn't match → lesson still produced but
// GeneratedBy=heuristic:no-match. The lesson is still useful (carries
// the raw obstacle reason in Failure summary).
func TestSynthesizeLesson_UnknownObstacleFallthrough(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)
	_, _ = s.Append(EventTypeToolExec, ToolExecEvent{Tool: "x"})
	obstaclePayload, _ := json.Marshal(map[string]interface{}{
		"phase":   "some-future-phase",
		"reasons": []string{"a completely novel error message we've never seen"},
	})
	_, _ = s.Append(EventTypeOuterTransition, OuterTransitionEvent{
		From: "Outer.Aggregated", To: "Outer.Obstacle", Payload: obstaclePayload,
	})
	tip, _ := s.Tip()
	_ = s.Refs.Set("attempt/1", tip)

	lesson, err := s.SynthesizeLesson("attempt/1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if lesson.GeneratedBy != "heuristic:no-match" {
		t.Errorf("unknown obstacle should yield heuristic:no-match, got %q", lesson.GeneratedBy)
	}
	if !strings.Contains(lesson.Failure, "novel error") {
		t.Errorf("raw reason must be preserved in Failure: %q", lesson.Failure)
	}
}

// LLM hook: when llm fn is provided, its output supersedes the
// heuristic. Verify Lesson.WhatWentWrong = LLM output, GeneratedBy =
// "llm".
func TestSynthesizeLesson_LLMOverrides(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)
	_, _ = s.Append(EventTypeToolExec, ToolExecEvent{Tool: "x"})
	obstaclePayload, _ := json.Marshal(map[string]interface{}{
		"phase": "gate", "reasons": []string{"unknown thing"},
	})
	_, _ = s.Append(EventTypeOuterTransition, OuterTransitionEvent{
		From: "Outer.Built", To: "Outer.Obstacle", Payload: obstaclePayload,
	})
	tip, _ := s.Tip()
	_ = s.Refs.Set("attempt/1", tip)

	llm := func(events []Event, hsum string) (string, []string, error) {
		if len(events) == 0 {
			return "", nil, nil
		}
		return "LLM-synthesized diagnosis here", []string{"do A", "don't do B"}, nil
	}
	lesson, err := s.SynthesizeLesson("attempt/1", llm)
	if err != nil {
		t.Fatal(err)
	}
	if lesson.WhatWentWrong != "LLM-synthesized diagnosis here" {
		t.Errorf("LLM output should override heuristic: got %q", lesson.WhatWentWrong)
	}
	if lesson.GeneratedBy != "llm" {
		t.Errorf("GeneratedBy should switch to llm: got %q", lesson.GeneratedBy)
	}
	if len(lesson.DosAndDonts) != 2 || lesson.DosAndDonts[0] != "do A" {
		t.Errorf("LLM dos-and-donts not applied: %v", lesson.DosAndDonts)
	}
}

// LLM error must NOT crash lesson synthesis — heuristic remains
// primary, LLM failure is logged into WhatWentWrong as an aside.
func TestSynthesizeLesson_LLMFailureGraceful(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)
	_, _ = s.Append(EventTypeToolExec, ToolExecEvent{Tool: "x"})
	obstaclePayload, _ := json.Marshal(map[string]interface{}{
		"phase": "gate", "reasons": []string{"vacuous-oracle-guard: bin missing"},
	})
	_, _ = s.Append(EventTypeOuterTransition, OuterTransitionEvent{
		From: "Outer.Built", To: "Outer.Obstacle", Payload: obstaclePayload,
	})
	tip, _ := s.Tip()
	_ = s.Refs.Set("attempt/1", tip)

	llm := func(events []Event, hsum string) (string, []string, error) {
		return "", nil, &llmTransportError{msg: "rate limit"}
	}
	lesson, err := s.SynthesizeLesson("attempt/1", llm)
	if err != nil {
		t.Fatalf("lesson synthesis must not fail when LLM errors: %v", err)
	}
	if !strings.HasPrefix(lesson.GeneratedBy, "heuristic:vacuous-oracle") {
		t.Errorf("heuristic must remain primary on LLM failure, got generatedBy=%q", lesson.GeneratedBy)
	}
	if !strings.Contains(lesson.WhatWentWrong, "rate limit") {
		t.Errorf("LLM failure note must appear in lesson body: %q", lesson.WhatWentWrong)
	}
}

type llmTransportError struct{ msg string }

func (e *llmTransportError) Error() string { return e.msg }

// Empty branch (no events) → error. Refusing to synthesize a lesson
// for nothing.
func TestSynthesizeLesson_EmptyBranch(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)
	if err := s.Refs.Set("attempt/empty", strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	_, err := s.SynthesizeLesson("attempt/empty", nil)
	if err == nil {
		t.Error("expected error when branch ref points at nonexistent event")
	}
}

// Phase 6 audit fix: ReadLessons must return lessons in TIMESTAMP
// order, not filesystem order. Phase 7 prompt injection relies on
// chronological ordering so the most recent advice is closest to
// the prompt's tail (recency-effect-friendly).
func TestReadLessons_SortedByTimestamp(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)
	// Write three lessons in REVERSE timestamp order so filesystem
	// order will NOT match timestamp order — only an explicit sort
	// recovers it.
	now := time.Now().UTC()
	for i, off := range []time.Duration{2 * time.Hour, 1 * time.Hour, 0} {
		l := &Lesson{
			BranchRef:   fmt.Sprintf("attempt/%d", i+1),
			FailedAt:    "deadbeef",
			Failure:     "test",
			GeneratedBy: "heuristic:test",
			Timestamp:   now.Add(off),
		}
		if err := s.WriteLesson(l); err != nil {
			t.Fatal(err)
		}
	}
	lessons, err := s.ReadLessons()
	if err != nil {
		t.Fatal(err)
	}
	if len(lessons) != 3 {
		t.Fatalf("expected 3 lessons, got %d", len(lessons))
	}
	for i := 0; i < len(lessons)-1; i++ {
		if lessons[i].Timestamp.After(lessons[i+1].Timestamp) {
			t.Errorf("lessons not sorted by timestamp ascending: [%d]=%v then [%d]=%v",
				i, lessons[i].Timestamp, i+1, lessons[i+1].Timestamp)
		}
	}
}

// Phase 6 audit fix: ReadLessons populates Lesson.Body with the
// raw markdown content. Phase 7 will inject Body into the system
// prompt verbatim.
func TestReadLessons_PopulatesBody(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)
	l := &Lesson{
		BranchRef:     "attempt/3",
		FailedAt:      "abcd",
		Failure:       "test failure",
		WhatWentWrong: "this is what happened",
		DosAndDonts:   []string{"do A", "don't B"},
		GeneratedBy:   "heuristic:test",
		Timestamp:     time.Now().UTC(),
	}
	if err := s.WriteLesson(l); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadLessons()
	if err != nil || len(got) != 1 {
		t.Fatalf("read: %v, count=%d", err, len(got))
	}
	if got[0].Body == "" {
		t.Fatal("Body must be populated on read (Phase 7 injects this verbatim)")
	}
	// Body should contain the human-readable sections (the parsed
	// metadata fields are sufficient for sort/dedup; the body is
	// the agent-facing payload).
	for _, want := range []string{"What went wrong", "this is what happened", "do A"} {
		if !strings.Contains(got[0].Body, want) {
			t.Errorf("Body missing %q\n--- got ---\n%s", want, got[0].Body)
		}
	}
}

// Phase 6 audit fix: Lesson.Render() is the SINGLE source of truth
// for the markdown format. WriteLesson uses it; CLI uses it.
// Round-trip Render → parseLessonMarkdown must preserve metadata.
func TestLesson_RenderRoundTrip(t *testing.T) {
	orig := &Lesson{
		BranchRef:   "attempt/round-trip",
		FailedAt:    "1234abcd",
		Failure:     "round-trip test",
		GeneratedBy: "heuristic:test-round-trip",
		Timestamp:   time.Date(2026, 5, 22, 12, 30, 0, 0, time.UTC),
	}
	body := orig.Render()
	parsed := parseLessonMarkdown(body)
	if parsed == nil {
		t.Fatal("round-trip parseLessonMarkdown returned nil")
	}
	if parsed.BranchRef != orig.BranchRef {
		t.Errorf("BranchRef lost: got %q want %q", parsed.BranchRef, orig.BranchRef)
	}
	if parsed.FailedAt != orig.FailedAt {
		t.Errorf("FailedAt lost: got %q want %q", parsed.FailedAt, orig.FailedAt)
	}
	if parsed.GeneratedBy != orig.GeneratedBy {
		t.Errorf("GeneratedBy lost: got %q want %q", parsed.GeneratedBy, orig.GeneratedBy)
	}
	if !parsed.Timestamp.Equal(orig.Timestamp) {
		t.Errorf("Timestamp lost: got %v want %v", parsed.Timestamp, orig.Timestamp)
	}
	if parsed.Body != body {
		t.Errorf("Body should equal raw rendered markdown")
	}
}

// ReadLessons on fresh project (no lessons dir) → empty, no error.
func TestReadLessons_NoneYet(t *testing.T) {
	srcDir := t.TempDir()
	s := NewSnapshotter(srcDir)
	lessons, err := s.ReadLessons()
	if err != nil {
		t.Errorf("fresh project read must not error, got %v", err)
	}
	if len(lessons) != 0 {
		t.Errorf("expected 0 lessons, got %d", len(lessons))
	}
}

// Heuristic patterns: each entry in the table should match its
// canonical reason fragment. Spot-check a handful that PB-30
// batches actually hit.
func TestLessonHeuristic_PatternCoverage(t *testing.T) {
	cases := map[string]string{
		"phase=gate | vacuous-oracle-guard: bin not found":            "vacuous-oracle",
		"phase=gate | ✗ [method-use-rule] object Foo artifact changed since characterization (hash A, current B)": "method-use-rule-drift",
		"phase=gate | ✗ [method-use-rule] object Foo locks ZERO behavior": "method-use-rule-empty-lock",
		"phase=graph_declare | reconstruction mode (./probe in cwd) declared only 1 object(s)": "monolithic-decomposition",
		"phase=gate | ✗ [compile-not-enough] object Bar":              "compile-not-enough",
		"phase=gate | ✗ [typecalc-test-required] object Bar":          "typecalc-test-required",
		"phase=gate | ✗ runtime-trace-missing":                        "runtime-trace-missing",
		"phase=confirm_one | object Foo did not reach confirmed after sub-agent loop": "confirm-sub-agent-exhausted",
		"phase=test | chain stage \"compile\" exceeded the per-step inactivity timeout": "step-timeout",
		"phase=graph_declare | object \"ProvideClockConfig\" has 11 produces+mutates ports (cap=4) — monolithic": "object-too-many-ports",
	}
	for reason, wantKey := range cases {
		t.Run(wantKey, func(t *testing.T) {
			gotKey, msg, dos := lessonHeuristic(reason)
			if gotKey != wantKey {
				t.Errorf("for %q: got key=%q want %q", reason, gotKey, wantKey)
			}
			if msg == "" {
				t.Errorf("heuristic match must produce non-empty whatWentWrong for %q", reason)
			}
			if len(dos) == 0 {
				t.Errorf("heuristic match must produce non-empty dosAndDonts for %q", reason)
			}
		})
	}
	// Empty input → no match (sentinel).
	if k, _, _ := lessonHeuristic(""); k != "" {
		t.Errorf("empty reason must yield no match, got key=%q", k)
	}
}
