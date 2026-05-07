package typecalc

import "context"

// RuleEnv is the ambient context handlers see — the LLM client, current
// session id, capability set, working directory, etc. It is intentionally
// loose: handlers cherry-pick the fields they need.
//
// RuleEnv lives in core typecalc/ rather than typecalc/rule/ because the
// language toolchain (typecalc/lang/) and the probe / feedback subpackages
// all reference *RuleEnv directly via their function signatures (e.g.
// CompileInvoker takes a *RuleEnv). Keeping it in core breaks an
// otherwise-circular import (rule needs lang for handlers, lang needs
// RuleEnv defined in rule).
type RuleEnv struct {
	WorkDir   string
	SessionID string
	Caps      CapSet

	// LLMInvoker is the function the rule registry calls when a rule's
	// Actor is ActorLLM. Decoupled as a function pointer so unit tests
	// can stub it.
	LLMInvoker LLMInvoker

	// CompileInvoker / TestInvoker are similarly stubbable. They run real
	// language toolchains in production (typecalc/lang/{compile,test}.go).
	CompileInvoker CompileInvoker
	TestInvoker    TestInvoker

	// MaxRetries caps the compile / test loops (§7.1, §7.2). 0 = use the
	// package default (DefaultMaxRetries).
	MaxRetries int
}

// LLMInvoker is the function signature for invoking an LLM with a prompt
// and the legal sum type. The implementation is responsible for inserting
// instructions about the TYPE: header convention.
type LLMInvoker func(ctx context.Context, env *RuleEnv, prompt string, expected SumType) (raw string, err error)

// CompileInvoker runs the real compiler for a given language and source
// payload. Returns either Compiled-tagged output, or a CompileError.
type CompileInvoker func(ctx context.Context, env *RuleEnv, src *TypedValue) (*TypedValue, error)

// TestInvoker runs the real test runner over the given Compiled source
// and TestSuite. Returns either Tested<Pass> or a TestError.
type TestInvoker func(ctx context.Context, env *RuleEnv, compiled, suite *TypedValue) (*TypedValue, error)
