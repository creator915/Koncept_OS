package rule

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/creator915/Koncept_OS/internal/typecalc"
	"github.com/creator915/Koncept_OS/internal/typecalc/lang"
)

// RegisterDefaults populates a Registry with every rule from §3 of the
// design document. The rules' handlers wire through to RuleEnv's
// LLMInvoker / CompileInvoker / TestInvoker — none of them call out to
// the real LLM directly. That decoupling lets unit tests stub the
// invokers and exercise rule plumbing without network calls.
func RegisterDefaults(reg *Registry) error {
	rules := []*Rule{
		{
			Name:        "parse_spec",
			Actor:       ActorLLM,
			Description: "Spec ⇒ Declared<Signature[]> × Description[] | ClarificationNeeded",
			Input:       []typecalc.Tag{{Kind: typecalc.KindSpec}},
			Output: typecalc.SumType{
				{Kind: typecalc.KindSignature, State: typecalc.StateUntyped},
				{Kind: typecalc.KindClarificationReq},
			},
			Handler: makeLLMHandler("parse_spec",
				`Read the spec. Emit either a list of signatures + descriptions, or a ClarificationNeeded request if the spec is ambiguous.`,
				typecalc.SumType{
					{Kind: typecalc.KindSignature, State: typecalc.StateUntyped},
					{Kind: typecalc.KindClarificationReq},
				}),
		},
		{
			Name:        "design_architecture",
			Actor:       ActorLLM,
			Description: "Signature[] × Description[] ⇒ Architecture × Graph",
			Input:       []typecalc.Tag{{Kind: typecalc.KindSignature}},
			Output: typecalc.SumType{
				{Kind: typecalc.KindArchitecture},
			},
			Handler: makeLLMHandler("design_architecture",
				`List sub-modules and intermediate variables. Emit the Architecture even if the task seems small enough to do in one shot.`,
				typecalc.SumType{{Kind: typecalc.KindArchitecture}}),
		},
		{
			Name:        "validate_structure",
			Actor:       ActorChecker,
			Description: "Graph ⇒ Validated<Graph> | StructureError",
			Input:       []typecalc.Tag{{Kind: typecalc.KindGraph}},
			Output: typecalc.SumType{
				{Kind: typecalc.KindGraph, State: typecalc.StateConfirmed},
				{Kind: typecalc.KindStructureError},
			},
			Handler: passthroughHandler,
		},
		{
			Name:        "generate_code",
			Actor:       ActorLLM,
			Description: "Declared<Signature> × Description × Graph ⇒ Uncompiled<Lang<L,Code>>",
			Input:       []typecalc.Tag{{Kind: typecalc.KindSignature}},
			Output: typecalc.SumType{
				{Kind: typecalc.KindCode, State: typecalc.StateUncompiled, Lang: typecalc.LangTypeScript},
				{Kind: typecalc.KindCode, State: typecalc.StateUncompiled, Lang: typecalc.LangJavaScript},
				{Kind: typecalc.KindCode, State: typecalc.StateUncompiled, Lang: typecalc.LangGo},
				{Kind: typecalc.KindCode, State: typecalc.StateUncompiled, Lang: typecalc.LangPython},
				{Kind: typecalc.KindCode, State: typecalc.StateUncompiled, Lang: typecalc.LangRust},
				{Kind: typecalc.KindCode, State: typecalc.StateUncompiled, Lang: typecalc.LangJava},
				{Kind: typecalc.KindCode, State: typecalc.StateUncompiled},
			},
			Handler: makeLLMHandler("generate_code",
				`Write the implementation. Output an Uncompiled<Lang<L, Code>> typed value — the first line must be 'TYPE: Uncompiled<Lang<<lang>, Code>>'. The Lang tag must match the project's primary language.`,
				typecalc.SumType{
					{Kind: typecalc.KindCode, State: typecalc.StateUncompiled, Lang: typecalc.LangTypeScript},
					{Kind: typecalc.KindCode, State: typecalc.StateUncompiled, Lang: typecalc.LangJavaScript},
					{Kind: typecalc.KindCode, State: typecalc.StateUncompiled, Lang: typecalc.LangGo},
					{Kind: typecalc.KindCode, State: typecalc.StateUncompiled, Lang: typecalc.LangPython},
					{Kind: typecalc.KindCode, State: typecalc.StateUncompiled, Lang: typecalc.LangRust},
					{Kind: typecalc.KindCode, State: typecalc.StateUncompiled, Lang: typecalc.LangJava},
				}),
		},
		{
			Name:        "compile",
			Actor:       ActorCompiler,
			Description: "Uncompiled<Lang<L,Code>> ⇒ Compiled<Lang<L,Code>> | CompileError",
			Input:       []typecalc.Tag{{Kind: typecalc.KindCode, State: typecalc.StateUncompiled}},
			Output: typecalc.SumType{
				{Kind: typecalc.KindCode, State: typecalc.StateCompiled},
				{Kind: typecalc.KindCompileError},
			},
			Handler: func(ctx context.Context, env *typecalc.RuleEnv, inputs ...*typecalc.TypedValue) (*typecalc.TypedValue, error) {
				if len(inputs) != 1 {
					return nil, fmt.Errorf("compile: expected 1 input, got %d", len(inputs))
				}
				inv := env.CompileInvoker
				if inv == nil {
					inv = lang.CompileLanguageInvoker
				}
				return inv(ctx, env, inputs[0])
			},
		},
		{
			Name:        "compiler_in_the_loop",
			Actor:       ActorSystem,
			Description: "CompileError ⇒ Request<Task,ErrorCode,ErrorLog>",
			Input:       []typecalc.Tag{{Kind: typecalc.KindCompileError}},
			Output:      typecalc.SumType{{Kind: typecalc.KindRequest}},
			Handler: func(ctx context.Context, env *typecalc.RuleEnv, inputs ...*typecalc.TypedValue) (*typecalc.TypedValue, error) {
				if len(inputs) != 1 {
					return nil, fmt.Errorf("compiler_in_the_loop: expected 1 input")
				}
				ce, err := typecalc.DecodeCompileError(inputs[0])
				if err != nil {
					return nil, err
				}
				req := typecalc.NewRequest(ce.Task)
				return typecalc.EnrichRequest(req, "compile_error", ce)
			},
		},
		{
			Name:        "retry_compile",
			Actor:       ActorLLM,
			Description: "Request<Task,ErrorCode,ErrorLog> ⇒ Uncompiled<Lang<L,Code>>",
			Input:       []typecalc.Tag{{Kind: typecalc.KindRequest}},
			Output: typecalc.SumType{
				{Kind: typecalc.KindCode, State: typecalc.StateUncompiled},
				{Kind: typecalc.KindObstacle},
			},
			Handler: makeLLMHandler("retry_compile",
				`Previous attempt failed to compile. Read the accumulated error log and produce a corrected Uncompiled<Code>. If the task is impossible, emit Obstacle<Task, Reason>.`,
				typecalc.SumType{
					{Kind: typecalc.KindCode, State: typecalc.StateUncompiled},
					{Kind: typecalc.KindObstacle},
				}),
		},
		{
			Name:        "extract_signature",
			Actor:       ActorSystem,
			Description: "Compiled<Lang<L,Code>> ⇒ Signature(actual)",
			Input:       []typecalc.Tag{{Kind: typecalc.KindCode, State: typecalc.StateCompiled}},
			Output:      typecalc.SumType{{Kind: typecalc.KindSignature}},
			Handler: func(ctx context.Context, env *typecalc.RuleEnv, inputs ...*typecalc.TypedValue) (*typecalc.TypedValue, error) {
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
			Input:       []typecalc.Tag{{Kind: typecalc.KindDescription}},
			Output:      typecalc.SumType{{Kind: typecalc.KindDescription}},
			Handler: makeLLMHandler("refine_description",
				`Refine the description using the actual signature. Output Description type only — no code, no commentary.`,
				typecalc.SumType{{Kind: typecalc.KindDescription}}),
		},
		{
			Name:        "generate_test",
			Actor:       ActorLLM,
			Description: "Description(refined) × Signature(actual) ⇒ TestSuite",
			Input:       []typecalc.Tag{{Kind: typecalc.KindDescription}},
			Output:      typecalc.SumType{{Kind: typecalc.KindTestSuite}},
			Handler: makeLLMHandler("generate_test",
				`Write a test suite from the description and signature. You do NOT see the source — test the contract, not the implementation.`,
				typecalc.SumType{{Kind: typecalc.KindTestSuite}}),
		},
		{
			Name:        "run_test",
			Actor:       ActorTester,
			Description: "Compiled<Code> × TestSuite ⇒ Tested<Code,Pass> | TestError",
			Input:       []typecalc.Tag{{Kind: typecalc.KindTestSuite}},
			Output: typecalc.SumType{
				{Kind: typecalc.KindCode, State: typecalc.StateTestedPass},
				{Kind: typecalc.KindTestError},
			},
			Handler: passthroughHandler,
		},
		{
			Name:        "review_test_error",
			Actor:       ActorLLM,
			Description: "TestError × Description × Signature ⇒ TestCorrect | TestWrong | DescriptionUnclear",
			Input:       []typecalc.Tag{{Kind: typecalc.KindTestError}},
			Output: typecalc.SumType{
				{Kind: typecalc.KindReason},
				{Kind: typecalc.KindClarificationReq},
			},
			Handler: makeLLMHandler("review_test_error",
				`Classify the failure: TestCorrect (code is buggy), TestWrong (test is buggy), DescriptionUnclear (need clarification).`,
				typecalc.SumType{
					{Kind: typecalc.KindReason},
					{Kind: typecalc.KindClarificationReq},
				}),
		},
		{
			Name:        "confirm",
			Actor:       ActorChecker,
			Description: "Tested<Code,Pass> × Validated<Graph> × Signature ⇒ Confirmed<Code> | StructureError",
			Input:       []typecalc.Tag{{Kind: typecalc.KindCode, State: typecalc.StateTestedPass}},
			Output: typecalc.SumType{
				{Kind: typecalc.KindCode, State: typecalc.StateConfirmed},
				{Kind: typecalc.KindStructureError},
			},
			Handler: func(ctx context.Context, env *typecalc.RuleEnv, inputs ...*typecalc.TypedValue) (*typecalc.TypedValue, error) {
				if len(inputs) != 1 {
					return nil, fmt.Errorf("confirm: expected 1 input")
				}
				return inputs[0].WithState(typecalc.StateConfirmed), nil
			},
		},
		{
			Name:        "receive_feedback",
			Actor:       ActorLLM,
			Description: "UserFeedback × Graph × AttrPartialOrder ⇒ ValueAdjust | LawMissing | DesignChange | CannotReproduce",
			Input:       []typecalc.Tag{{Kind: typecalc.KindUserFeedback}},
			Output: typecalc.SumType{
				{Kind: typecalc.KindValueAdjust},
				{Kind: typecalc.KindLawMissing},
				{Kind: typecalc.KindDesignChange},
				{Kind: typecalc.KindCannotReproduce},
			},
			Handler: makeLLMHandler("receive_feedback",
				`Translate user feedback into a technical action. Possible verdicts: ValueAdjust<AttrPath, NewValue>, LawMissing<AttrPath, NewLaw>, DesignChange<Reason>, CannotReproduce<Reason>.`,
				typecalc.SumType{
					{Kind: typecalc.KindValueAdjust},
					{Kind: typecalc.KindLawMissing},
					{Kind: typecalc.KindDesignChange},
					{Kind: typecalc.KindCannotReproduce},
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
func makeLLMHandler(name, instruction string, expected typecalc.SumType) Handler {
	return func(ctx context.Context, env *typecalc.RuleEnv, inputs ...*typecalc.TypedValue) (*typecalc.TypedValue, error) {
		if env == nil || env.LLMInvoker == nil {
			return typecalc.FormatErr("rule %s requires env.LLMInvoker", name), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Rule: %s\n", name)
		fmt.Fprintf(&b, "Instruction: %s\n\n", instruction)
		fmt.Fprintf(&b, "Output the FIRST line as `TYPE: <kind>` and the rest as the payload.\n")
		fmt.Fprintf(&b, "Allowed output kinds: %s\n\n", expected.String())
		fmt.Fprintf(&b, "Inputs (%d):\n", len(inputs))
		for i, in := range inputs {
			fmt.Fprintf(&b, "  [%d] %s\n", i, in.Tag())
			fmt.Fprintf(&b, "  payload: %s\n", typecalc.Trim(in.Payload, 1024))
			if len(in.Context) > 0 {
				ctxJSON, _ := json.Marshal(in.Context)
				fmt.Fprintf(&b, "  context: %s\n", typecalc.Trim(string(ctxJSON), 1024))
			}
		}
		raw, err := env.LLMInvoker(ctx, env, b.String(), expected)
		if err != nil {
			return nil, fmt.Errorf("LLM invoker for %s: %w", name, err)
		}
		out, err := typecalc.ParseLLMOutput(raw, expected)
		if err != nil {
			return nil, err
		}
		if env.SessionID != "" {
			out.Channel = env.SessionID
		}
		return out, nil
	}
}

// passthroughHandler is the placeholder for rules whose handler is bound
// externally (e.g. validate_structure needs the graph package; run_test
// needs both compiled+suite). Returns FormatError if invoked directly.
func passthroughHandler(ctx context.Context, env *typecalc.RuleEnv, inputs ...*typecalc.TypedValue) (*typecalc.TypedValue, error) {
	if len(inputs) == 0 {
		return typecalc.FormatErr("passthroughHandler called without inputs"), nil
	}
	return typecalc.FormatErr("rule has no bound handler — caller must supply one externally"), nil
}

// extractSignatureHeuristic is a shallow signature extractor used as the
// default body of rule extract_signature.
func extractSignatureHeuristic(src *typecalc.TypedValue) *typecalc.TypedValue {
	if src == nil {
		return typecalc.New(typecalc.KindSignature, "")
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
	out := typecalc.New(typecalc.KindSignature, strings.Join(keep, "\n"))
	out.Lang = src.Lang
	out.Channel = src.Channel
	return out
}
