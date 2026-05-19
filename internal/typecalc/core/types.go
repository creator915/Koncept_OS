// Package typecalc implements the KonceptOS type calculator described in
// docs/TypeCalculator.md. It is a layered, orthogonal system: while
// the existing graph package handles the *spatial* dimension (who produces
// what, who consumes what), typecalc handles the *temporal* dimension
// (what state a piece of code is in and which operations it admits).
//
// The integration point is intentionally minimal: typecalc does NOT
// replace the existing OpenAI-style tool_calls protocol used by the agent
// loop. Instead, typed values flow alongside tool calls — every produced
// artifact (a code blob, a test suite, a description) is wrapped in a
// TypedValue carrying the tags from §2 (state, language, channel,
// permissions) and routed through the rule registry in rules.go. The
// router resolves a Rule whose input shape matches the TypedValue and
// dispatches to the registered handler — which may itself invoke the LLM
// (via the actor "llm") or a system tool (compile, test, etc.).
//
// This file defines the primitive types and constructors. See:
//   - sum.go         — sum type validation
//   - request.go     — Request<...Context> enrichment
//   - permission.go  — capability sets and permission gate
//   - rules.go       — rule registry
//   - router.go      — dispatcher
//   - compile.go     — compile loop with retry cap → Obstacle
//   - test.go        — test loop + review_test_error
//   - probe.go       — probe planning from graph topology
//   - feedback.go    — user feedback translation
//   - format.go      — format checkers (per language, per content type)
package core

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Kind enumerates the leaf "content type" of a TypedValue. The doc §2.1
// calls these atomic types. Wrapping constructors (state, lang, chan,
// perm) live as fields on TypedValue, not as nested values.
type Kind string

const (
	KindCode             Kind = "Code"
	KindSignature        Kind = "Signature"
	KindDescription      Kind = "Description"
	KindTestSuite        Kind = "TestSuite"
	KindTestCase         Kind = "TestCase"
	KindConfig           Kind = "Config"
	KindSpec             Kind = "Spec"
	KindArchitecture     Kind = "Architecture"
	KindGraph            Kind = "Graph"
	KindTask             Kind = "Task"
	KindErrorCode        Kind = "ErrorCode"
	KindErrorLog         Kind = "ErrorLog"
	KindReason           Kind = "Reason"
	KindValue            Kind = "Value"
	KindAttrPath         Kind = "AttrPath"
	KindRequest          Kind = "Request"
	KindObstacle         Kind = "Obstacle"
	KindClarificationReq Kind = "ClarificationNeeded"
	KindCompileError     Kind = "CompileError"
	KindTestError        Kind = "TestError"
	KindStructureError   Kind = "StructureError"
	KindFormatError      Kind = "FormatError"
	KindPermissionDenied Kind = "PermissionDenied"
	KindFaultLocated     Kind = "FaultLocated"
	KindUserFeedback     Kind = "UserFeedback"
	KindValueAdjust      Kind = "ValueAdjust"
	KindLawMissing       Kind = "LawMissing"
	KindDesignChange     Kind = "DesignChange"
	KindCannotReproduce  Kind = "CannotReproduce"
	KindProbePlan        Kind = "ProbePlan"
	KindProbeResult      Kind = "ProbeResult"

	// KindInsufficient is the "I cannot verify this" response —
	// returned by compile / test invokers for languages or situations
	// the framework genuinely cannot mechanically check (HTML without
	// in-tree runner, missing toolchain, port_observation undeclared,
	// etc.). It is NOT a pass: gate refuses Insufficient unless paired
	// with an explicit waiver evidence. The point is to prevent the
	// fail-open class of bugs where "we don't know how to test this"
	// silently became "test passed".
	KindInsufficient Kind = "Insufficient"
)

// NewInsufficient constructs an Insufficient TypedValue with a reason
// payload. Reasons should explain WHY mechanical verification was not
// possible, in user-readable form (these surface to the agent and
// downstream gate messages).
func NewInsufficient(reason string) *TypedValue {
	return New(KindInsufficient, reason)
}

// State enumerates the lifecycle constructors from §2.3. Distinct from
// graph.Status — that is the per-entity status persisted in K/graph.json,
// while State is the per-typed-value transient state. The two map onto
// each other at confirmation time (see graph_status_validator.go).
type State string

const (
	StateUncompiled State = "Uncompiled"
	StateCompiled   State = "Compiled"
	StateTestedPass State = "Tested<Pass>"
	StateTestedFail State = "Tested<Fail>"
	StateConfirmed  State = "Confirmed"

	// StateUntyped is the absence-of-state value (§2.3 only applies to Code
	// and similar artifacts). For Spec, Description, etc. State is empty.
	StateUntyped State = ""
)

// Lang is the language tag from §2.4. Empty when not applicable.
type Lang string

const (
	LangNone       Lang = ""
	LangTypeScript Lang = "TypeScript"
	LangJavaScript Lang = "JavaScript"
	LangGo         Lang = "Go"
	LangRust       Lang = "Rust"
	LangPython     Lang = "Python"
	LangJava       Lang = "Java"
	LangHaskell    Lang = "Haskell"
	LangHTML       Lang = "HTML"
	LangC          Lang = "C"
)

// LangFromExt maps a file extension (with or without leading dot) to a
// Lang. Returns LangNone for unrecognized extensions; callers should treat
// that as "no language tag" rather than an error.
func LangFromExt(ext string) Lang {
	ext = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
	switch ext {
	case "ts", "tsx":
		return LangTypeScript
	case "js", "jsx", "mjs", "cjs":
		return LangJavaScript
	case "go":
		return LangGo
	case "rs":
		return LangRust
	case "py":
		return LangPython
	case "java":
		return LangJava
	case "hs":
		return LangHaskell
	case "c":
		return LangC
	case "html", "htm":
		return LangHTML
	}
	return LangNone
}

// TypedValue is the carrier of a value through the type calculator. All
// optional tags default to their zero values. The (Kind, State, Lang,
// Channel) tuple is used by rules.go to match a rule's input pattern; the
// payload is opaque content (string, JSON, structured data) that handlers
// interpret.
//
// "Wrapping" semantics: per §4.1 the recommended outer-to-inner order is
// Chan > Permitted > State > Lang > Kind. We collapse all of those into
// fields on a single struct because the carrier flows together — the
// composition order is conceptual (what's bigger / more macro), not
// representational.
type TypedValue struct {
	Kind    Kind     `json:"kind"`
	State   State    `json:"state,omitempty"`
	Lang    Lang     `json:"lang,omitempty"`
	Channel string   `json:"channel,omitempty"` // session id
	Caps    []string `json:"caps,omitempty"`    // permission tags (see permission.go)

	// Payload is the raw content. For Code it's the source text; for
	// Description it's the natural-language intent; for Request it's a
	// JSON-encoded RequestEnvelope (see request.go); for ProbePlan it's
	// JSON-encoded ProbePlanData; etc.
	Payload string `json:"payload"`

	// Context bundles structured tag metadata that doesn't fit into the
	// flat fields — error logs attached to a Request, expected/actual
	// values in a TestError, etc. JSON-encoded so the carrier stays
	// serializable.
	Context map[string]json.RawMessage `json:"context,omitempty"`
}

// New constructs a TypedValue with sensible defaults.
func New(kind Kind, payload string) *TypedValue {
	return &TypedValue{Kind: kind, Payload: payload}
}

// WithState returns a copy with the State field set. State transitions
// must follow the rules in rules.go — this constructor itself does not
// enforce a transition, since the caller (typically a handler) has
// already determined the correct successor state.
func (t *TypedValue) WithState(s State) *TypedValue {
	cp := *t
	cp.State = s
	return &cp
}

// WithLang returns a copy with the Lang field set.
func (t *TypedValue) WithLang(l Lang) *TypedValue {
	cp := *t
	cp.Lang = l
	return &cp
}

// WithChannel returns a copy with the Channel (session id) set.
func (t *TypedValue) WithChannel(sessionID string) *TypedValue {
	cp := *t
	cp.Channel = sessionID
	return &cp
}

// WithCaps returns a copy with the capability set replaced.
func (t *TypedValue) WithCaps(caps []string) *TypedValue {
	cp := *t
	if caps != nil {
		cp.Caps = append([]string(nil), caps...)
	}
	return &cp
}

// WithContext attaches a single named context blob. Keeps existing keys.
func (t *TypedValue) WithContext(key string, val any) (*TypedValue, error) {
	cp := *t
	if cp.Context == nil {
		cp.Context = map[string]json.RawMessage{}
	} else {
		ctx := make(map[string]json.RawMessage, len(cp.Context))
		for k, v := range cp.Context {
			ctx[k] = v
		}
		cp.Context = ctx
	}
	raw, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("encode context %s: %w", key, err)
	}
	cp.Context[key] = raw
	return &cp, nil
}

// Context decodes a named context blob into out. Returns false if the key
// is absent.
func (t *TypedValue) DecodeContext(key string, out any) (bool, error) {
	raw, ok := t.Context[key]
	if !ok {
		return false, nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return true, fmt.Errorf("decode context %s: %w", key, err)
	}
	return true, nil
}

// String renders the typed value as e.g. "Chan<s_x, Permitted<{r,w},
// Uncompiled<Lang<TypeScript, Code>>>>" for tracing. Order follows §4.1.
func (t *TypedValue) String() string {
	if t == nil {
		return "<nil>"
	}
	core := string(t.Kind)
	if t.Lang != LangNone {
		core = fmt.Sprintf("Lang<%s, %s>", t.Lang, core)
	}
	if t.State != StateUntyped {
		core = fmt.Sprintf("%s<%s>", t.State, core)
	}
	if len(t.Caps) > 0 {
		core = fmt.Sprintf("Permitted<{%s}, %s>", strings.Join(t.Caps, ","), core)
	}
	if t.Channel != "" {
		core = fmt.Sprintf("Chan<%s, %s>", t.Channel, core)
	}
	return core
}

// Tag returns the canonical (Kind,State,Lang) key used for rule matching.
// Channel and Caps are not part of the dispatch key — they're contextual
// metadata flowing alongside the routed value.
func (t *TypedValue) Tag() Tag {
	return Tag{Kind: t.Kind, State: t.State, Lang: t.Lang}
}

// Tag is the rule-matching key for a TypedValue. Two TypedValues with the
// same Tag dispatch to the same handler (per their channel/caps).
type Tag struct {
	Kind  Kind  `json:"kind"`
	State State `json:"state,omitempty"`
	Lang  Lang  `json:"lang,omitempty"`
}

func (t Tag) String() string {
	core := string(t.Kind)
	if t.Lang != LangNone {
		core = fmt.Sprintf("Lang<%s,%s>", t.Lang, core)
	}
	if t.State != StateUntyped {
		core = fmt.Sprintf("%s<%s>", t.State, core)
	}
	return core
}
