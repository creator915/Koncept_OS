package core

import (
	"encoding/json"
	"fmt"
)

// RequestEnvelope is the structured payload for a TypedValue of Kind
// KindRequest. The doc §4.2 calls these "enriched requests" — a single
// Task plus an ordered list of accumulated context blobs (compile errors,
// test errors, prior failed attempts, ...). The agent loop forwards the
// envelope to the LLM as the prompt body whenever a retry is needed.
type RequestEnvelope struct {
	Task    string         `json:"task"`
	History []RequestEntry `json:"history"`
}

// RequestEntry is one piece of accumulated context. Tag describes the
// kind of error/info being attached; Detail is its serialized form.
type RequestEntry struct {
	Tag    string          `json:"tag"`
	Detail json.RawMessage `json:"detail"`
}

// NewRequest constructs a fresh Request<Task>.
func NewRequest(task string) *TypedValue {
	env := RequestEnvelope{Task: task, History: []RequestEntry{}}
	raw, _ := json.Marshal(env)
	return &TypedValue{Kind: KindRequest, Payload: string(raw)}
}

// EnrichRequest appends an error/info blob to a Request. Returns a NEW
// TypedValue — the original is untouched, matching the linear semantics
// of the rule system (a typed value is consumed by exactly one rule and
// produces a new typed value as output).
//
// The rule §4.2 enrich:
//
//	Request<...Context> × Error<...NewInfo>  ⇒  Request<...Context, ...NewInfo>
//
// is implemented here, not as a registered rule, because enrichment is a
// pure system operation (no LLM, no compiler) and is invoked
// programmatically by compile.go / test.go when their loops iterate.
func EnrichRequest(req *TypedValue, tag string, detail any) (*TypedValue, error) {
	if req == nil || req.Kind != KindRequest {
		return nil, fmt.Errorf("EnrichRequest: not a Request typed value (got %s)", req.Tag())
	}
	var env RequestEnvelope
	if err := json.Unmarshal([]byte(req.Payload), &env); err != nil {
		return nil, fmt.Errorf("decode RequestEnvelope: %w", err)
	}
	rawDetail, err := json.Marshal(detail)
	if err != nil {
		return nil, fmt.Errorf("encode enrichment %s: %w", tag, err)
	}
	env.History = append(env.History, RequestEntry{Tag: tag, Detail: rawDetail})
	rawEnv, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("encode enriched RequestEnvelope: %w", err)
	}
	cp := *req
	cp.Payload = string(rawEnv)
	return &cp, nil
}

// DecodeRequest extracts the RequestEnvelope from a Request typed value.
func DecodeRequest(req *TypedValue) (*RequestEnvelope, error) {
	if req == nil || req.Kind != KindRequest {
		return nil, fmt.Errorf("DecodeRequest: not a Request typed value (got %v)", req)
	}
	var env RequestEnvelope
	if err := json.Unmarshal([]byte(req.Payload), &env); err != nil {
		return nil, fmt.Errorf("decode RequestEnvelope: %w", err)
	}
	return &env, nil
}

// CompileErrorDetail is the structured payload of a CompileError typed value.
// Matches §2.7: CompileError<Task, ErrorCode, ErrorLog>.
type CompileErrorDetail struct {
	Task      string `json:"task"`
	ErrorCode string `json:"errorCode"`
	ErrorLog  string `json:"errorLog"`
}

// TestErrorDetail is the structured payload of a TestError typed value.
// Matches §2.7: TestError<TestCase, Value, Value>  (expected, actual).
type TestErrorDetail struct {
	TestCase string `json:"testCase"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

// ObstacleDetail is the structured payload of an Obstacle typed value.
// Matches §2.7: Obstacle<Task, Reason>.
type ObstacleDetail struct {
	Task   string `json:"task"`
	Reason string `json:"reason"`

	// Trail records the history that led to the obstacle. Useful when the
	// obstacle bubbles up to a parent session — the parent gets to see
	// what was tried.
	Trail []RequestEntry `json:"trail,omitempty"`
}

// NewCompileError builds a CompileError<...> typed value.
func NewCompileError(task, errorCode, errorLog string) *TypedValue {
	d := CompileErrorDetail{Task: task, ErrorCode: errorCode, ErrorLog: errorLog}
	raw, _ := json.Marshal(d)
	return &TypedValue{Kind: KindCompileError, Payload: string(raw)}
}

// NewTestError builds a TestError<...> typed value.
func NewTestError(tc, expected, actual string) *TypedValue {
	d := TestErrorDetail{TestCase: tc, Expected: expected, Actual: actual}
	raw, _ := json.Marshal(d)
	return &TypedValue{Kind: KindTestError, Payload: string(raw)}
}

// NewObstacle builds an Obstacle<Task, Reason> typed value, optionally
// carrying the request trail that led to giving up.
func NewObstacle(task, reason string, trail []RequestEntry) *TypedValue {
	d := ObstacleDetail{Task: task, Reason: reason, Trail: trail}
	raw, _ := json.Marshal(d)
	return &TypedValue{Kind: KindObstacle, Payload: string(raw)}
}

// DecodeCompileError unwraps a CompileError typed value.
func DecodeCompileError(tv *TypedValue) (*CompileErrorDetail, error) {
	if tv == nil || tv.Kind != KindCompileError {
		return nil, fmt.Errorf("DecodeCompileError: wrong kind (got %v)", tv)
	}
	var d CompileErrorDetail
	if err := json.Unmarshal([]byte(tv.Payload), &d); err != nil {
		return nil, fmt.Errorf("decode CompileErrorDetail: %w", err)
	}
	return &d, nil
}

// DecodeTestError unwraps a TestError typed value.
func DecodeTestError(tv *TypedValue) (*TestErrorDetail, error) {
	if tv == nil || tv.Kind != KindTestError {
		return nil, fmt.Errorf("DecodeTestError: wrong kind (got %v)", tv)
	}
	var d TestErrorDetail
	if err := json.Unmarshal([]byte(tv.Payload), &d); err != nil {
		return nil, fmt.Errorf("decode TestErrorDetail: %w", err)
	}
	return &d, nil
}

// DecodeObstacle unwraps an Obstacle typed value.
func DecodeObstacle(tv *TypedValue) (*ObstacleDetail, error) {
	if tv == nil || tv.Kind != KindObstacle {
		return nil, fmt.Errorf("DecodeObstacle: wrong kind (got %v)", tv)
	}
	var d ObstacleDetail
	if err := json.Unmarshal([]byte(tv.Payload), &d); err != nil {
		return nil, fmt.Errorf("decode ObstacleDetail: %w", err)
	}
	return &d, nil
}
