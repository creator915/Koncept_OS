package typecalcchain

// All TypedValue payloads in this chain carry ObjectID — the chain
// is per-object, and the handlers consult disk via the unified bundle
// at .kcpos/typecalc/<id>.json for everything else (the impl source,
// the spec section, the tests section, etc.). Keeping the payloads
// small means TypedValue serialization stays cheap and the on-disk
// state remains the canonical record.

// StartConfirmPayload is what the chain entry value carries. The agent
// (or the main loop) constructs this with the object's ID and the
// pre-existing impl path; the chain takes it from there.
type StartConfirmPayload struct {
	ObjectID string `json:"objectId"`

	// MaxRetries caps how many enrich-retry cycles the chain will
	// attempt before escalating to Obstacle. 0 = use the package
	// default. Lower per-object than the global router maxSteps so a
	// single object can't burn the whole budget.
	MaxRetries int `json:"maxRetries,omitempty"`
}

// CompiledPayload — kind=compile passed. Carries the lang so the next
// step can decide if test evidence is mandatory (Go/JS/TS/Python) vs
// optional (HTML/Rust without an in-tree runner).
type CompiledPayload struct {
	ObjectID string `json:"objectId"`
	Lang     string `json:"lang"`
}

// CompileErrorPayload — kind=compile returned non-zero. The errorLog
// is what the LLM needs to debug; we copy it from the on-disk evidence
// here so the enricher doesn't need to re-read the bundle.
type CompileErrorPayload struct {
	ObjectID  string `json:"objectId"`
	Lang      string `json:"lang"`
	ErrorCode string `json:"errorCode,omitempty"` // e.g. "SYNTAX_ERROR"
	ErrorLog  string `json:"errorLog"`            // compiler stderr
	Attempts  int    `json:"attempts"`
}

// DescribedPayload — typecalc_describe wrote the Spec section.
type DescribedPayload struct {
	ObjectID    string `json:"objectId"`
	Description string `json:"description"` // copied for retry prompts
}

// SynthesizedPayload — typecalc_synthesize_tests wrote the Tests section.
type SynthesizedPayload struct {
	ObjectID  string `json:"objectId"`
	CaseCount int    `json:"caseCount"` // for progress display
}

// TestedPassPayload — typecalc_test returned Tested<Pass>.
type TestedPassPayload struct {
	ObjectID string `json:"objectId"`
}

// TestErrorPayload — typecalc_test returned TestError.
type TestErrorPayload struct {
	ObjectID  string `json:"objectId"`
	FailingCase string `json:"failingCase,omitempty"` // human-readable name
	Expected    string `json:"expected,omitempty"`
	Actual      string `json:"actual,omitempty"`
	RunnerLog   string `json:"runnerLog,omitempty"` // truncated tail
	Attempts    int    `json:"attempts"`
}

// ReviewedPayload — typecalc_review returned ok=true.
type ReviewedPayload struct {
	ObjectID   string `json:"objectId"`
	Confidence float64 `json:"confidence,omitempty"`
}

// ReviewFailedPayload — typecalc_review returned ok=false. Carries
// either the structural issues OR the LLM reviewer's "fail" reasons.
type ReviewFailedPayload struct {
	ObjectID       string   `json:"objectId"`
	StaticIssues   []string `json:"staticIssues,omitempty"`
	RuntimeIssues  []string `json:"runtimeIssues,omitempty"`
	ReviewerReasons []string `json:"reviewerReasons,omitempty"`
	Attempts       int      `json:"attempts"`
}

// ConfirmedPayload — terminal happy state. The chain has driven the
// object through every gate; graph.Object.Status is now confirmed.
type ConfirmedPayload struct {
	ObjectID string `json:"objectId"`
}

// ObstaclePayload — terminal escalation. Either the agent gave up
// (retry budget exhausted) or a handler diagnosed a structural problem
// the chain cannot resolve.
type ObstaclePayload struct {
	ObjectID string `json:"objectId"`
	Reason   string `json:"reason"`
	LastType string `json:"lastType,omitempty"` // the type the chain was at when it gave up
}
