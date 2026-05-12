package router

import (
	"context"
	"strings"
	"testing"
)

// TestFeedbackLoop_CompileErrorRoundTrip exercises the canonical
// failure → enriched-request → retry path end-to-end:
//
//   1. Uncompiled<Lang<Go,Code>> handler ("compile") produces CompileError
//   2. CompileError enricher wraps the error fields into Request<Compile>
//   3. Request<Compile> handler ("retry compiler with feedback") emits a
//      fresh Uncompiled<Lang<Go,Code>> with the LLM having seen the
//      Task + ErrorCode + ErrorLog + Guidance
//   4. Compile handler succeeds on retry → Compiled<…> → terminal
//
// The LLM stub asserts it received the imperative-style prompt body
// (Original task + What went wrong + What to produce next), which is
// what makes the feedback "tell the LLM HOW to pass" rather than just
// "the test failed".
func TestFeedbackLoop_CompileErrorRoundTrip(t *testing.T) {
	r := NewRouter()

	// Compile handler — emits CompileError on first run, Compiled on
	// second. We use the attempt counter on the input to differentiate.
	compileCalls := 0
	r.Register(&HandlerFunc{
		In:  "Uncompiled<Lang<Go,Code>>",
		Out: []string{"Compiled<Lang<Go,Code>>", "CompileError"},
		Run: func(ctx context.Context, in TypedValue) (TypedValue, error) {
			compileCalls++
			if compileCalls == 1 {
				return NewTypedValue("CompileError", map[string]string{
					"task":      "implement greet(name string) string",
					"errorCode": "TYPE_MISMATCH",
					"errorLog":  "func greet(name) string — missing parameter type",
				})
			}
			return NewTypedValue("Compiled<Lang<Go,Code>>", map[string]string{"hash": "abc"})
		},
	})

	// Enricher — turns CompileError into Request<Compile>
	r.Register(&EnrichHandler{
		In:  "CompileError",
		Out: "Request<Compile>",
		Transform: func(in TypedValue) (Request, error) {
			var payload struct {
				Task      string `json:"task"`
				ErrorCode string `json:"errorCode"`
				ErrorLog  string `json:"errorLog"`
			}
			if err := in.Unmarshal(&payload); err != nil {
				return Request{}, err
			}
			return Request{
				Task: payload.Task,
				Context: map[string]string{
					"errorCode": payload.ErrorCode,
					"errorLog":  payload.ErrorLog,
				},
				Guidance: []string{
					"Re-emit Uncompiled<Lang<Go,Code>> with each parameter's type annotation present.",
					"Refer to the errorLog above for the exact line that needs fixing.",
				},
				Attempts: 1,
			}, nil
		},
	})

	// Retry handler — LLM-driven. The stub asserts it received
	// the canonical prompt shape.
	stubLLM := func(ctx context.Context, sys, user string) (string, error) {
		// Crucial: the prompt MUST contain all three sections so the
		// LLM has enough information to fix the bug without "thinking
		// about what to do next".
		for _, must := range []string{
			"Original task",
			"What went wrong",
			"errorCode",
			"TYPE_MISMATCH",
			"What to produce next",
			"Re-emit Uncompiled",
		} {
			if !strings.Contains(user, must) {
				t.Errorf("retry prompt missing %q. got prompt:\n%s", must, user)
			}
		}
		return "TYPE: Uncompiled<Lang<Go,Code>>\n" +
			"{\"source\":\"func greet(name string) string { return \\\"hello\\\" }\"}", nil
	}

	r.Register(&LLMHandler{
		In:             "Request<Compile>",
		AllowedOutputs: []string{"Uncompiled<Lang<Go,Code>>", "Obstacle<Task,Reason>"},
		SystemPrompt:   "You are the compiler retry handler.",
		BuildPrompt: func(in TypedValue) (string, error) {
			var req Request
			if err := in.Unmarshal(&req); err != nil {
				return "", err
			}
			return FormatRequestForLLM(req, []string{
				"Uncompiled<Lang<Go,Code>>", "Obstacle<Task,Reason>",
			}), nil
		},
		Invoke: stubLLM,
	})

	r.RegisterTerminal("Compiled<Lang<Go,Code>>")
	r.RegisterTerminal("Obstacle<Task,Reason>")

	// Validate connectivity before run — every output should resolve.
	orphans := r.Connectivity()
	if len(orphans) > 0 {
		t.Fatalf("router has orphan outputs: %v", orphans)
	}

	initial, _ := NewTypedValue("Uncompiled<Lang<Go,Code>>", map[string]string{
		"source": "func greet(name) string { return \"hello\" }",
	})
	out, err := r.Run(context.Background(), initial)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != "Compiled<Lang<Go,Code>>" {
		t.Errorf("expected Compiled, got %q", out.Type)
	}
	if compileCalls != 2 {
		t.Errorf("compile handler should run twice (first fail, second pass), got %d", compileCalls)
	}
}

func TestFormatRequestForLLM_IncludesAllSections(t *testing.T) {
	req := Request{
		Task:     "compile module Foo",
		Context:  map[string]string{"errorCode": "E001", "errorLog": "missing semicolon"},
		Guidance: []string{"add semicolon at line 3", "re-emit Uncompiled"},
		Attempts: 2,
	}
	out := FormatRequestForLLM(req, []string{"Uncompiled<Code>", "Obstacle"})
	checks := []string{
		"## Original task",
		"compile module Foo",
		"attempt 2",
		"### errorCode",
		"E001",
		"### errorLog",
		"missing semicolon",
		"## What to produce next",
		"- add semicolon at line 3",
		"- re-emit Uncompiled",
		"TYPE: Uncompiled<Code>",
		"TYPE: Obstacle",
	}
	for _, c := range checks {
		if !strings.Contains(out, c) {
			t.Errorf("rendered prompt missing %q in:\n%s", c, out)
		}
	}
}

func TestIncrementRetry_StartsAtOne(t *testing.T) {
	meta := IncrementRetry(nil)
	if meta[RetryAttemptsMeta] != "1" {
		t.Errorf("first increment should be 1, got %q", meta[RetryAttemptsMeta])
	}
}

func TestIncrementRetry_PreservesOtherKeys(t *testing.T) {
	in := map[string]string{"k": "v", RetryAttemptsMeta: "3"}
	out := IncrementRetry(in)
	if out[RetryAttemptsMeta] != "4" {
		t.Errorf("expected 4, got %q", out[RetryAttemptsMeta])
	}
	if out["k"] != "v" {
		t.Errorf("other key lost: %q", out["k"])
	}
	// Input must be unchanged (purity).
	if in[RetryAttemptsMeta] != "3" {
		t.Errorf("input mutated; counter is now %q", in[RetryAttemptsMeta])
	}
}
