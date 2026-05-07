package typecalc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// RegisterDefaults populates a Registry with every rule from §3 of the
// design document. The rules' handlers wire through to RuleEnv's
// LLMInvoker / CompileInvoker / TestInvoker — none of them call out to
// the real LLM directly. That decoupling lets unit tests stub the
// invokers and exercise rule plumbing without network calls.
func RegisterDefaults(reg *Registry) error {
	rules := []*Rule{
		// --- Requirements phase ---
		{
			Name:        "parse_spec",
			Actor:       ActorLLM,
			Description: "Spec ⇒ Declared<Signature[]> × Description[] | ClarificationNeeded",
			Input:       []Tag{{Kind: KindSpec}},
			Output: SumType{
				{Kind: KindSignature, State: StateUntyped},
				{Kind: KindClarificationReq},
			},
			Handler: makeLLMHandler("parse_spec",
				`Read the spec. Emit either a list of signatures + descriptions, or a ClarificationNeeded request if the spec is ambiguous.`,
				SumType{
					{Kind: KindSignature, State: StateUntyped},
					{Kind: KindClarificationReq},
				}),
		},
		// --- Architecture design ---
		{
			Name:        "design_architecture",
			Actor:       ActorLLM,
			Description: "Signature[] × Description[] ⇒ Architecture × Graph",
			Input:       []Tag{{Kind: KindSignature}},
			Output: SumType{
				{Kind: KindArchitecture},
			},
			Handler: makeLLMHandler("design_architecture",
				`List sub-modules and intermediate variables. Emit the Architecture even if the task seems small enough to do in one shot.`,
				SumType{{Kind: KindArchitecture}}),
		},
		{
			Name:        "validate_structure",
			Actor:       ActorChecker,
			Description: "Graph ⇒ Validated<Graph> | StructureError",
			Input:       []Tag{{Kind: KindGraph}},
			Output: SumType{
				{Kind: KindGraph, State: StateConfirmed},
				{Kind: KindStructureError},
			},
			Handler: passthroughHandler, // wired in by agent loop with real graph.Validate
		},

		// --- Compile phase ---
		{
			Name:        "generate_code",
			Actor:       ActorLLM,
			Description: "Declared<Signature> × Description × Graph ⇒ Uncompiled<Lang<L,Code>>",
			Input:       []Tag{{Kind: KindSignature}},
			Output: SumType{
				{Kind: KindCode, State: StateUncompiled, Lang: LangTypeScript},
				{Kind: KindCode, State: StateUncompiled, Lang: LangJavaScript},
				{Kind: KindCode, State: StateUncompiled, Lang: LangGo},
				{Kind: KindCode, State: StateUncompiled, Lang: LangPython},
				{Kind: KindCode, State: StateUncompiled, Lang: LangRust},
				{Kind: KindCode, State: StateUncompiled, Lang: LangJava},
				{Kind: KindCode, State: StateUncompiled},
			},
			Handler: makeLLMHandler("generate_code",
				`Write the implementation. Output an Uncompiled<Lang<L, Code>> typed value — the first line must be 'TYPE: Uncompiled<Lang<<lang>, Code>>'. The Lang tag must match the project's primary language.`,
				SumType{
					{Kind: KindCode, State: StateUncompiled, Lang: LangTypeScript},
					{Kind: KindCode, State: StateUncompiled, Lang: LangJavaScript},
					{Kind: KindCode, State: StateUncompiled, Lang: LangGo},
					{Kind: KindCode, State: StateUncompiled, Lang: LangPython},
					{Kind: KindCode, State: StateUncompiled, Lang: LangRust},
					{Kind: KindCode, State: StateUncompiled, Lang: LangJava},
				}),
		},
		{
			Name:        "compile",
			Actor:       ActorCompiler,
			Description: "Uncompiled<Lang<L,Code>> ⇒ Compiled<Lang<L,Code>> | CompileError",
			Input:       []Tag{{Kind: KindCode, State: StateUncompiled}},
			Output: SumType{
				{Kind: KindCode, State: StateCompiled},
				{Kind: KindCompileError},
			},
			Handler: func(ctx context.Context, env *RuleEnv, inputs ...*TypedValue) (*TypedValue, error) {
				if len(inputs) != 1 {
					return nil, fmt.Errorf("compile: expected 1 input, got %d", len(inputs))
				}
				inv := env.CompileInvoker
				if inv == nil {
					inv = CompileLanguageInvoker
				}
				return inv(ctx, env, inputs[0])
			},
		},
		{
			Name:        "compiler_in_the_loop",
			Actor:       ActorSystem,
			Description: "CompileError ⇒ Request<Task,ErrorCode,ErrorLog>",
			Input:       []Tag{{Kind: KindCompileError}},
			Output:      SumType{{Kind: KindRequest}},
			Handler: func(ctx context.Context, env *RuleEnv, inputs ...*TypedValue) (*TypedValue, error) {
				if len(inputs) != 1 {
					return nil, fmt.Errorf("compiler_in_the_loop: expected 1 input")
				}
				ce, err := DecodeCompileError(inputs[0])
				if err != nil {
					return nil, err
				}
				req := NewRequest(ce.Task)
				return EnrichRequest(req, "compile_error", ce)
			},
		},
		{
			Name:        "retry_compile",
			Actor:       ActorLLM,
			Description: "Request<Task,ErrorCode,ErrorLog> ⇒ Uncompiled<Lang<L,Code>>",
			Input:       []Tag{{Kind: KindRequest}},
			Output: SumType{
				{Kind: KindCode, State: StateUncompiled},
				{Kind: KindObstacle},
			},
			Handler: makeLLMHandler("retry_compile",
				`Previous attempt failed to compile. Read the accumulated error log and produce a corrected Uncompiled<Code>. If the task is impossible, emit Obstacle<Task, Reason>.`,
				SumType{
					{Kind: KindCode, State: StateUncompiled},
					{Kind: KindObstacle},
				}),
		},

		// --- Signature extraction ---
		{
			Name:        "extract_signature",
			Actor:       ActorSystem,
			Description: "Compiled<Lang<L,Code>> ⇒ Signature(actual)",
			Input:       []Tag{{Kind: KindCode, State: StateCompiled}},
			Output:      SumType{{Kind: KindSignature}},
			Handler: func(ctx context.Context, env *RuleEnv, inputs ...*TypedValue) (*TypedValue, error) {
				if len(inputs) != 1 {
					return nil, fmt.Errorf("extract_signature: expected 1 input")
				}
				return extractSignatureHeuristic(inputs[0]), nil
			},
		},
		{
			Name:        "refine_description",
			Actor:       ActorLLM,
			Description: "Description(coarse) × Signature(actual) ⇒ Description(refined)",
			Input:       []Tag{{Kind: KindDescription}},
			Output:      SumType{{Kind: KindDescription}},
			Handler: makeLLMHandler("refine_description",
				`Refine the description using the actual signature. Output Description type only — no code, no commentary.`,
				SumType{{Kind: KindDescription}}),
		},

		// --- Test phase ---
		{
			Name:        "generate_test",
			Actor:       ActorLLM,
			Description: "Description(refined) × Signature(actual) ⇒ TestSuite",
			Input:       []Tag{{Kind: KindDescription}},
			Output:      SumType{{Kind: KindTestSuite}},
			Handler: makeLLMHandler("generate_test",
				`Write a test suite from the description and signature. You do NOT see the source — test the contract, not the implementation.`,
				SumType{{Kind: KindTestSuite}}),
		},
		{
			Name:        "run_test",
			Actor:       ActorTester,
			Description: "Compiled<Code> × TestSuite ⇒ Tested<Code,Pass> | TestError",
			Input:       []Tag{{Kind: KindTestSuite}},
			Output: SumType{
				{Kind: KindCode, State: StateTestedPass},
				{Kind: KindTestError},
			},
			Handler: passthroughHandler, // bound externally — needs both compiled + suite
		},
		{
			Name:        "review_test_error",
			Actor:       ActorLLM,
			Description: "TestError × Description × Signature ⇒ TestCorrect | TestWrong | DescriptionUnclear",
			Input:       []Tag{{Kind: KindTestError}},
			Output: SumType{
				{Kind: KindReason}, // generic — verdict is in payload
				{Kind: KindClarificationReq},
			},
			Handler: makeLLMHandler("review_test_error",
				`Classify the failure: TestCorrect (code is buggy), TestWrong (test is buggy), DescriptionUnclear (need clarification).`,
				SumType{
					{Kind: KindReason},
					{Kind: KindClarificationReq},
				}),
		},

		// --- Confirmation ---
		{
			Name:        "confirm",
			Actor:       ActorChecker,
			Description: "Tested<Code,Pass> × Validated<Graph> × Signature ⇒ Confirmed<Code> | StructureError",
			Input:       []Tag{{Kind: KindCode, State: StateTestedPass}},
			Output: SumType{
				{Kind: KindCode, State: StateConfirmed},
				{Kind: KindStructureError},
			},
			Handler: func(ctx context.Context, env *RuleEnv, inputs ...*TypedValue) (*TypedValue, error) {
				if len(inputs) != 1 {
					return nil, fmt.Errorf("confirm: expected 1 input")
				}
				// The actual structural validation against the graph is
				// performed externally (see internal/graph/checker.go). At
				// this layer we just promote the state.
				return inputs[0].WithState(StateConfirmed), nil
			},
		},

		// --- User feedback ---
		{
			Name:        "receive_feedback",
			Actor:       ActorLLM,
			Description: "UserFeedback × Graph × AttrPartialOrder ⇒ ValueAdjust | LawMissing | DesignChange | CannotReproduce",
			Input:       []Tag{{Kind: KindUserFeedback}},
			Output: SumType{
				{Kind: KindValueAdjust},
				{Kind: KindLawMissing},
				{Kind: KindDesignChange},
				{Kind: KindCannotReproduce},
			},
			Handler: makeLLMHandler("receive_feedback",
				`Translate user feedback into a technical action. Possible verdicts: ValueAdjust<AttrPath, NewValue>, LawMissing<AttrPath, NewLaw>, DesignChange<Reason>, CannotReproduce<Reason>.`,
				SumType{
					{Kind: KindValueAdjust},
					{Kind: KindLawMissing},
					{Kind: KindDesignChange},
					{Kind: KindCannotReproduce},
				}),
		},
	}
	for _, r := range rules {
		if err := reg.Register(r); err != nil {
			return err
		}
	}
	return nil
}

// makeLLMHandler returns a Handler that calls env.LLMInvoker with a
// generated prompt and parses the result against the expected sum type.
// The body of the prompt is composed of: the typed value's payload (which
// for a Request includes the accumulated history) + a fixed instruction
// describing the legal output forms.
//
// If env.LLMInvoker is nil the handler returns a FormatError so misuse
// surfaces immediately rather than producing garbage typed values.
func makeLLMHandler(name, instruction string, expected SumType) Handler {
	return func(ctx context.Context, env *RuleEnv, inputs ...*TypedValue) (*TypedValue, error) {
		if env == nil || env.LLMInvoker == nil {
			return formatErr("rule %s requires env.LLMInvoker", name), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Rule: %s\n", name)
		fmt.Fprintf(&b, "Instruction: %s\n\n", instruction)
		fmt.Fprintf(&b, "Output the FIRST line as `TYPE: <kind>` and the rest as the payload.\n")
		fmt.Fprintf(&b, "Allowed output kinds: %s\n\n", expected.String())
		fmt.Fprintf(&b, "Inputs (%d):\n", len(inputs))
		for i, in := range inputs {
			fmt.Fprintf(&b, "  [%d] %s\n", i, in.Tag())
			fmt.Fprintf(&b, "  payload: %s\n", trim(in.Payload, 1024))
			if len(in.Context) > 0 {
				ctxJSON, _ := json.Marshal(in.Context)
				fmt.Fprintf(&b, "  context: %s\n", trim(string(ctxJSON), 1024))
			}
		}
		raw, err := env.LLMInvoker(ctx, env, b.String(), expected)
		if err != nil {
			return nil, fmt.Errorf("LLM invoker for %s: %w", name, err)
		}
		out, err := ParseLLMOutput(raw, expected)
		if err != nil {
			return nil, err
		}
		// Tag the output with the channel so downstream rules know which
		// session it belongs to.
		if env.SessionID != "" {
			out.Channel = env.SessionID
		}
		return out, nil
	}
}

// passthroughHandler is the placeholder for rules whose handler is bound
// externally by the agent loop (e.g. validate_structure needs access to
// the graph package, run_test needs both compiled+suite). The placeholder
// returns a FormatError if invoked directly to surface mis-wiring.
func passthroughHandler(ctx context.Context, env *RuleEnv, inputs ...*TypedValue) (*TypedValue, error) {
	if len(inputs) == 0 {
		return formatErr("passthroughHandler called without inputs"), nil
	}
	return formatErr("rule has no bound handler — caller must supply one externally"), nil
}

// extractSignatureHeuristic is a shallow signature extractor used as the
// default body of rule extract_signature. It scans the source for top-
// level type/interface/function declarations and concatenates them. A
// real implementation would call a per-language AST parser; this default
// keeps the rule wired without dragging in tree-sitter.
func extractSignatureHeuristic(src *TypedValue) *TypedValue {
	if src == nil {
		return New(KindSignature, "")
	}
	lines := strings.Split(src.Payload, "\n")
	var keep []string
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "export ") ||
			strings.HasPrefix(t, "interface ") ||
			strings.HasPrefix(t, "type ") ||
			strings.HasPrefix(t, "func ") ||
			strings.HasPrefix(t, "def ") ||
			strings.HasPrefix(t, "fn ") ||
			strings.HasPrefix(t, "class ") ||
			strings.HasPrefix(t, "public ") {
			keep = append(keep, t)
		}
	}
	out := New(KindSignature, strings.Join(keep, "\n"))
	out.Lang = src.Lang
	out.Channel = src.Channel
	return out
}
