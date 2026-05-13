// Package typecalcchain registers the type-driven state machine for
// one object's "uncompiled → confirmed" verification path. This is
// Phase 6 of the v9.0 migration: it takes the existing typecalc tools
// (compile / describe / synthesize_tests / test / review) and wires
// them as Router handlers keyed by TypedValue tags.
//
// The agent's role shrinks to: produce a starting TypedValue
// (StartConfirm{ObjectID}), call BuildChain().Run(value), receive a
// terminal value (Confirmed | Obstacle). No "what tool to call next"
// decisions inside the chain — the router handles dispatch.
//
// Type tags used in this chain:
//
//	StartConfirm<obj>        — entry; "I want this object confirmed"
//	Compiled<obj>            — typecalc_compile passed
//	CompileError<obj>        — typecalc_compile failed
//	Described<obj>           — typecalc_describe done
//	Synthesized<obj>         — typecalc_synthesize_tests done
//	Tested<obj,Pass>         — typecalc_test passed
//	TestError<obj>           — typecalc_test failed
//	Reviewed<obj,Pass>       — typecalc_review returned ok=true
//	ReviewFailed<obj>        — typecalc_review returned ok=false
//	Request<obj,...>         — enriched-feedback envelope from any error
//	Confirmed<obj>           — terminal success
//	Obstacle<obj,Reason>     — terminal escalation
//
// Concrete payloads (`Content`) live in payloads.go. The object_id is
// always carried in TypedValue.Content so handlers don't need to parse
// the type tag for it; <obj> in tag names is symbolic.
package chains

// TypeStartConfirm is the entry type for "ensure this object is
// confirmed". The chain starts here; the agent (or another router
// invocation) drops a value of this type into the runner.
const TypeStartConfirm = "StartConfirm"

// Mechanical steps along the happy path.
const (
	TypeCompiled    = "Compiled<Object>"
	TypeDescribed   = "Described<Object>"
	TypeSynthesized = "Synthesized<Object>"
	TypeTestedPass  = "Tested<Object,Pass>"
	TypeReviewed    = "Reviewed<Object,Pass>"
)

// Error types — each carries the enrich-loop entry point.
const (
	TypeCompileError = "CompileError<Object>"
	TypeTestError    = "TestError<Object>"
	TypeReviewFailed = "ReviewFailed<Object>"
)

// Enriched-request types — what the LLM sees when the system asks it
// to fix a specific failure. Each is the output of a corresponding
// Enricher and the input of a corresponding retry LLMHandler.
const (
	TypeRequestCompile = "Request<Object,Compile>"
	TypeRequestTest    = "Request<Object,Test>"
	TypeRequestReview  = "Request<Object,Review>"
)

// Terminal types.
const (
	TypeConfirmed = "Confirmed<Object>"
	TypeObstacle  = "Obstacle<Object,Reason>"
)
