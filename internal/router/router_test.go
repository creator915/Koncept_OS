package router

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNewTypedValue_RejectsEmptyTag(t *testing.T) {
	if _, err := NewTypedValue("", nil); err == nil {
		t.Error("expected error for empty tag")
	}
}

func TestNewTypedValue_RoundTrip(t *testing.T) {
	in := map[string]int{"x": 1}
	v, err := NewTypedValue("Compiled<Lang<Go,Code>>", in)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]int
	if err := v.Unmarshal(&out); err != nil {
		t.Fatal(err)
	}
	if out["x"] != 1 {
		t.Errorf("roundtrip lost data: %v", out)
	}
}

func TestParseFromLLM_HappyPath_JSONPayload(t *testing.T) {
	raw := "TYPE: Uncompiled<Lang<Go,Code>>\n" +
		"{\"source\":\"package main\\nfunc main(){}\"}"
	v, err := ParseFromLLM(raw, []string{"Uncompiled<Lang<Go,Code>>", "ClarificationNeeded"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != "Uncompiled<Lang<Go,Code>>" {
		t.Errorf("type=%q", v.Type)
	}
	var body map[string]string
	if err := v.Unmarshal(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body["source"], "func main") {
		t.Errorf("payload lost: %v", body)
	}
}

func TestParseFromLLM_TolerantWhitespaceInTag(t *testing.T) {
	raw := "TYPE:  Tested< Code , Pass >\n\"ok\""
	v, err := ParseFromLLM(raw, []string{"Tested<Code,Pass>"})
	if err != nil {
		t.Fatalf("expected tag normalization, got %v", err)
	}
	if v.Type != "Tested<Code,Pass>" {
		t.Errorf("type=%q want Tested<Code,Pass>", v.Type)
	}
}

func TestParseFromLLM_RejectsUnauthorizedBranch(t *testing.T) {
	raw := "TYPE: Confirmed<Code>\n{}"
	_, err := ParseFromLLM(raw, []string{"Uncompiled<Lang<Go,Code>>"})
	if err == nil {
		t.Fatal("expected unauthorized-branch error")
	}
	if !strings.Contains(err.Error(), "unauthorized branch") {
		t.Errorf("error message should mention authorization: %v", err)
	}
}

func TestParseFromLLM_RejectsMissingTag(t *testing.T) {
	raw := "Here's the code:\nfunc main() {}\n"
	_, err := ParseFromLLM(raw, []string{"Uncompiled<Lang<Go,Code>>"})
	if err == nil {
		t.Fatal("expected missing-tag error")
	}
}

// ---- Router lifecycle tests ----

type alwaysOK struct {
	in   string
	outs []string
	pick string // output type to return
}

func (a *alwaysOK) Accepts() string { return a.in }
func (a *alwaysOK) Outputs() []string {
	if a.outs == nil {
		return []string{a.pick}
	}
	return a.outs
}
func (a *alwaysOK) Handle(ctx context.Context, in TypedValue) (TypedValue, error) {
	return TypedValue{Type: a.pick}, nil
}

func TestRouter_RunUntilTerminal(t *testing.T) {
	r := NewRouter()
	r.Register(&alwaysOK{in: "A", pick: "B"})
	r.Register(&alwaysOK{in: "B", pick: "C"})
	r.RegisterTerminal("C")

	out, err := r.Run(context.Background(), TypedValue{Type: "A"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != "C" {
		t.Errorf("expected terminal C, got %q", out.Type)
	}
}

func TestRouter_NoHandler_Errors(t *testing.T) {
	r := NewRouter()
	r.Register(&alwaysOK{in: "A", pick: "B"})
	// B is unregistered AND not terminal — router must error.
	_, err := r.Run(context.Background(), TypedValue{Type: "A"})
	if err == nil {
		t.Fatal("expected no-handler error")
	}
	if !strings.Contains(err.Error(), "no handler for type \"B\"") {
		t.Errorf("error message should name the missing type: %v", err)
	}
}

func TestRouter_HandlerProducesUnauthorizedOutput(t *testing.T) {
	r := NewRouter()
	// Handler declares Outputs=[B] but actually picks C.
	r.Register(&alwaysOK{in: "A", outs: []string{"B"}, pick: "C"})
	r.RegisterTerminal("B")
	_, err := r.Run(context.Background(), TypedValue{Type: "A"})
	if err == nil {
		t.Fatal("expected unauthorized-output error")
	}
	if !strings.Contains(err.Error(), "unauthorized output type") {
		t.Errorf("error should flag unauthorized: %v", err)
	}
}

func TestRouter_MaxStepsGuard(t *testing.T) {
	r := NewRouter()
	r.SetMaxSteps(5)
	// A → B → A → B → … forever
	r.Register(&alwaysOK{in: "A", pick: "B"})
	r.Register(&alwaysOK{in: "B", pick: "A"})

	_, err := r.Run(context.Background(), TypedValue{Type: "A"})
	if err == nil {
		t.Fatal("expected max-steps error")
	}
	if !strings.Contains(err.Error(), "exceeded max steps") {
		t.Errorf("error should mention max steps: %v", err)
	}
}

func TestRouter_RegisterDuplicatePanics(t *testing.T) {
	r := NewRouter()
	r.Register(&alwaysOK{in: "A", pick: "B"})
	defer func() {
		if recover() == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	r.Register(&alwaysOK{in: "A", pick: "C"})
}

func TestRouter_Connectivity_FlagsOrphanOutputs(t *testing.T) {
	r := NewRouter()
	r.Register(&alwaysOK{in: "A", outs: []string{"B", "C"}, pick: "B"})
	r.RegisterTerminal("B")
	// C has no handler and is not terminal → orphan.
	orphans := r.Connectivity()
	if len(orphans) != 1 || orphans[0] != "C" {
		t.Errorf("expected orphan [C], got %v", orphans)
	}
}

func TestRouter_RespectsContextCancellation(t *testing.T) {
	r := NewRouter()
	r.Register(&alwaysOK{in: "A", pick: "B"})
	r.Register(&alwaysOK{in: "B", pick: "A"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	_, err := r.Run(ctx, TypedValue{Type: "A"})
	if err == nil {
		t.Fatal("expected ctx-cancelled error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// ---- LLMHandler tests ----

func TestLLMHandler_DispatchesViaInvoker(t *testing.T) {
	invoked := false
	stub := func(ctx context.Context, sys, user string) (string, error) {
		invoked = true
		if !strings.Contains(user, "PAYLOAD") {
			return "", fmt.Errorf("user prompt missing payload: %s", user)
		}
		return "TYPE: Compiled<Lang<Go,Code>>\n{\"hash\":\"abc\"}", nil
	}
	h := &LLMHandler{
		In:             "Uncompiled<Lang<Go,Code>>",
		AllowedOutputs: []string{"Compiled<Lang<Go,Code>>", "CompileError"},
		SystemPrompt:   "you are the compiler",
		BuildPrompt:    func(in TypedValue) (string, error) { return "PAYLOAD " + in.Type, nil },
		Invoke:         stub,
	}
	in, _ := NewTypedValue("Uncompiled<Lang<Go,Code>>", map[string]string{"src": "package main"})
	out, err := h.Handle(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !invoked {
		t.Error("invoker never called")
	}
	if out.Type != "Compiled<Lang<Go,Code>>" {
		t.Errorf("type=%q", out.Type)
	}
}

func TestLLMHandler_RejectsOffListBranch(t *testing.T) {
	stub := func(ctx context.Context, sys, user string) (string, error) {
		return "TYPE: SomeOtherThing\nignored", nil
	}
	h := &LLMHandler{
		In:             "X",
		AllowedOutputs: []string{"Y", "Z"},
		BuildPrompt:    func(in TypedValue) (string, error) { return "p", nil },
		Invoke:         stub,
	}
	_, err := h.Handle(context.Background(), TypedValue{Type: "X"})
	if err == nil {
		t.Fatal("expected off-list rejection")
	}
}

func TestLLMHandler_PreservesCarrierMetadata(t *testing.T) {
	stub := func(ctx context.Context, sys, user string) (string, error) {
		return "TYPE: B\n{}", nil
	}
	h := &LLMHandler{
		In:             "A",
		AllowedOutputs: []string{"B"},
		BuildPrompt:    func(in TypedValue) (string, error) { return "p", nil },
		Invoke:         stub,
	}
	in := TypedValue{Type: "A", Channel: "s_root", Lang: "Go"}
	out, err := h.Handle(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Channel != "s_root" {
		t.Errorf("channel lost: %q", out.Channel)
	}
	if out.Lang != "Go" {
		t.Errorf("lang lost: %q", out.Lang)
	}
}
