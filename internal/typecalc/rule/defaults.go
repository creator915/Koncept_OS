package rule

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
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
			Input:       []core.Tag{{Kind: core.KindSpec}},
			Output: core.SumType{
				{Kind: core.KindSignature, State: core.StateUntyped},
				{Kind: core.KindClarificationReq},
			},
			Handler: makeLLMHandler("parse_spec",
				`Read the spec. Emit either a list of signatures + descriptions, or a ClarificationNeeded request if the spec is ambiguous.`,
				core.SumType{
					{Kind: core.KindSignature, State: core.StateUntyped},
					{Kind: core.KindClarificationReq},
				}),
		},
		{
			Name:        "design_architecture",
			Actor:       ActorLLM,
			Description: "Signature[] × Description[] ⇒ Architecture × Graph",
			Input:       []core.Tag{{Kind: core.KindSignature}},
			Output: core.SumType{
				{Kind: core.KindArchitecture},
			},
			Handler: makeLLMHandler("design_architecture",
				`List sub-modules and intermediate variables. Emit the Architecture even if the task seems small enough to do in one shot.`,
				core.SumType{{Kind: core.KindArchitecture}}),
		},
		{
			Name:        "validate_structure",
			Actor:       ActorChecker,
			Description: "Graph ⇒ Validated<Graph> | StructureError",
			Input:       []core.Tag{{Kind: core.KindGraph}},
			Output: core.SumType{
				{Kind: core.KindGraph, State: core.StateConfirmed},
				{Kind: core.KindStructureError},
			},
			Handler: passthroughHandler,
		},
		{
			Name:        "generate_code",
			Actor:       ActorLLM,
			Description: "Declared<Signature> × Description × Graph ⇒ Uncompiled<Lang<L,Code>>",
			Input:       []core.Tag{{Kind: core.KindSignature}},
			Output: core.SumType{
				{Kind: core.KindCode, State: core.StateUncompiled, Lang: core.LangTypeScript},
				{Kind: core.KindCode, State: core.StateUncompiled, Lang: core.LangJavaScript},
				{Kind: core.KindCode, State: core.StateUncompiled, Lang: core.LangGo},
				{Kind: core.KindCode, State: core.StateUncompiled, Lang: core.LangPython},
				{Kind: core.KindCode, State: core.StateUncompiled, Lang: core.LangRust},
				{Kind: core.KindCode, State: core.StateUncompiled, Lang: core.LangJava},
				{Kind: core.KindCode, State: core.StateUncompiled},
			},
			Handler: makeLLMHandler("generate_code",
				`Write the implementation. Output an Uncompiled<Lang<L, Code>> typed value — the first line must be 'TYPE: Uncompiled<Lang<<lang>, Code>>'. The Lang tag must match the project's primary language.`,
				core.SumType{
					{Kind: core.KindCode, State: core.StateUncompiled, Lang: core.LangTypeScript},
					{Kind: core.KindCode, State: core.StateUncompiled, Lang: core.LangJavaScript},
					{Kind: core.KindCode, State: core.StateUncompiled, Lang: core.LangGo},
					{Kind: core.KindCode, State: core.StateUncompiled, Lang: core.LangPython},
					{Kind: core.KindCode, State: core.StateUncompiled, Lang: core.LangRust},
					{Kind: core.KindCode, State: core.StateUncompiled, Lang: core.LangJava},
				}),
		},
		{
			Name:        "compile",
			Actor:       ActorCompiler,
			Description: "Uncompiled<Lang<L,Code>> ⇒ Compiled<Lang<L,Code>> | CompileError",
			Input:       []core.Tag{{Kind: core.KindCode, State: core.StateUncompiled}},
			Output: core.SumType{
				{Kind: core.KindCode, State: core.StateCompiled},
				{Kind: core.KindCompileError},
			},
			Handler: func(ctx context.Context, env *core.RuleEnv, inputs ...*core.TypedValue) (*core.TypedValue, error) {
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
			Input:       []core.Tag{{Kind: core.KindCompileError}},
			Output:      core.SumType{{Kind: core.KindRequest}},
			Handler: func(ctx context.Context, env *core.RuleEnv, inputs ...*core.TypedValue) (*core.TypedValue, error) {
				if len(inputs) != 1 {
					return nil, fmt.Errorf("compiler_in_the_loop: expected 1 input")
				}
				ce, err := core.DecodeCompileError(inputs[0])
				if err != nil {
					return nil, err
				}
				req := core.NewRequest(ce.Task)
				return core.EnrichRequest(req, "compile_error", ce)
			},
		},
		{
			Name:        "retry_compile",
			Actor:       ActorLLM,
			Description: "Request<Task,ErrorCode,ErrorLog> ⇒ Uncompiled<Lang<L,Code>>",
			Input:       []core.Tag{{Kind: core.KindRequest}},
			Output: core.SumType{
				{Kind: core.KindCode, State: core.StateUncompiled},
				{Kind: core.KindObstacle},
			},
			Handler: makeLLMHandler("retry_compile",
				`Previous attempt failed to compile. Read the accumulated error log and produce a corrected Uncompiled<Code>. If the task is impossible, emit Obstacle<Task, Reason>.`,
				core.SumType{
					{Kind: core.KindCode, State: core.StateUncompiled},
					{Kind: core.KindObstacle},
				}),
		},
		{
			Name:        "extract_signature",
			Actor:       ActorSystem,
			Description: "Compiled<Lang<L,Code>> ⇒ Signature(actual)",
			Input:       []core.Tag{{Kind: core.KindCode, State: core.StateCompiled}},
			Output:      core.SumType{{Kind: core.KindSignature}},
			Handler: func(ctx context.Context, env *core.RuleEnv, inputs ...*core.TypedValue) (*core.TypedValue, error) {
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
			Input:       []core.Tag{{Kind: core.KindDescription}},
			Output:      core.SumType{{Kind: core.KindDescription}},
			Handler: makeLLMHandler("refine_description",
				`Refine the description using the actual signature. Output Description type only — no code, no commentary.`,
				core.SumType{{Kind: core.KindDescription}}),
		},
		{
			Name:        "generate_test",
			Actor:       ActorLLM,
			Description: "Description(refined) × Signature(actual) ⇒ TestSuite",
			Input:       []core.Tag{{Kind: core.KindDescription}},
			Output:      core.SumType{{Kind: core.KindTestSuite}},
			Handler: makeLLMHandler("generate_test",
				`Write a test suite from the description and signature. You do NOT see the source — test the contract, not the implementation.`,
				core.SumType{{Kind: core.KindTestSuite}}),
		},
		{
			Name:        "run_test",
			Actor:       ActorTester,
			Description: "Compiled<Code> × TestSuite ⇒ Tested<Code,Pass> | TestError",
			Input:       []core.Tag{{Kind: core.KindTestSuite}},
			Output: core.SumType{
				{Kind: core.KindCode, State: core.StateTestedPass},
				{Kind: core.KindTestError},
			},
			Handler: passthroughHandler,
		},
		{
			Name:        "review_test_error",
			Actor:       ActorLLM,
			Description: "TestError × Description × Signature ⇒ TestCorrect | TestWrong | DescriptionUnclear",
			Input:       []core.Tag{{Kind: core.KindTestError}},
			Output: core.SumType{
				{Kind: core.KindReason},
				{Kind: core.KindClarificationReq},
			},
			Handler: makeLLMHandler("review_test_error",
				`Classify the failure: TestCorrect (code is buggy), TestWrong (test is buggy), DescriptionUnclear (need clarification).`,
				core.SumType{
					{Kind: core.KindReason},
					{Kind: core.KindClarificationReq},
				}),
		},
		{
			Name:        "confirm",
			Actor:       ActorChecker,
			Description: "Tested<Code,Pass> × Validated<Graph> × Signature ⇒ Confirmed<Code> | StructureError",
			Input:       []core.Tag{{Kind: core.KindCode, State: core.StateTestedPass}},
			Output: core.SumType{
				{Kind: core.KindCode, State: core.StateConfirmed},
				{Kind: core.KindStructureError},
			},
			Handler: func(ctx context.Context, env *core.RuleEnv, inputs ...*core.TypedValue) (*core.TypedValue, error) {
				if len(inputs) != 1 {
					return nil, fmt.Errorf("confirm: expected 1 input")
				}
				return inputs[0].WithState(core.StateConfirmed), nil
			},
		},
		{
			Name:        "receive_feedback",
			Actor:       ActorLLM,
			Description: "UserFeedback × Graph × AttrPartialOrder ⇒ ValueAdjust | LawMissing | DesignChange | CannotReproduce",
			Input:       []core.Tag{{Kind: core.KindUserFeedback}},
			Output: core.SumType{
				{Kind: core.KindValueAdjust},
				{Kind: core.KindLawMissing},
				{Kind: core.KindDesignChange},
				{Kind: core.KindCannotReproduce},
			},
			Handler: makeLLMHandler("receive_feedback",
				`Translate user feedback into a technical action. Possible verdicts: ValueAdjust<AttrPath, NewValue>, LawMissing<AttrPath, NewLaw>, DesignChange<Reason>, CannotReproduce<Reason>.`,
				core.SumType{
					{Kind: core.KindValueAdjust},
					{Kind: core.KindLawMissing},
					{Kind: core.KindDesignChange},
					{Kind: core.KindCannotReproduce},
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
func makeLLMHandler(name, instruction string, expected core.SumType) Handler {
	return func(ctx context.Context, env *core.RuleEnv, inputs ...*core.TypedValue) (*core.TypedValue, error) {
		if env == nil || env.LLMInvoker == nil {
			return core.FormatErr("rule %s requires env.LLMInvoker", name), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Rule: %s\n", name)
		fmt.Fprintf(&b, "Instruction: %s\n\n", instruction)
		fmt.Fprintf(&b, "Output the FIRST line as `TYPE: <kind>` and the rest as the payload.\n")
		fmt.Fprintf(&b, "Allowed output kinds: %s\n\n", expected.String())
		fmt.Fprintf(&b, "Inputs (%d):\n", len(inputs))
		for i, in := range inputs {
			fmt.Fprintf(&b, "  [%d] %s\n", i, in.Tag())
			fmt.Fprintf(&b, "  payload: %s\n", core.Trim(in.Payload, 1024))
			if len(in.Context) > 0 {
				ctxJSON, _ := json.Marshal(in.Context)
				fmt.Fprintf(&b, "  context: %s\n", core.Trim(string(ctxJSON), 1024))
			}
		}
		raw, err := env.LLMInvoker(ctx, env, b.String(), expected)
		if err != nil {
			return nil, fmt.Errorf("LLM invoker for %s: %w", name, err)
		}
		out, err := core.ParseLLMOutput(raw, expected)
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
func passthroughHandler(ctx context.Context, env *core.RuleEnv, inputs ...*core.TypedValue) (*core.TypedValue, error) {
	if len(inputs) == 0 {
		return core.FormatErr("passthroughHandler called without inputs"), nil
	}
	return core.FormatErr("rule has no bound handler — caller must supply one externally"), nil
}

// extractSignatureHeuristic is a shallow signature extractor used as the
// default body of rule extract_signature.
func extractSignatureHeuristic(src *core.TypedValue) *core.TypedValue {
	if src == nil {
		return core.New(core.KindSignature, "")
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
	out := core.New(core.KindSignature, strings.Join(keep, "\n"))
	out.Lang = src.Lang
	out.Channel = src.Channel
	return out
}
