// Package router implements the v9.0 type-driven state machine that
// replaces the v8.x ReAct agent loop. The design follows
// docs/TypeCalculator.md verbatim:
//
//  - Every value flowing through the system has a nominal type tag,
//    not an inferred structural type. The tag determines what happens
//    next, not the value content.
//
//  - Each handler in the router declares "I accept type T₁, produce
//    O₁ | O₂ | … | Oₘ". The runtime dispatches by Type, never by
//    LLM choice of "next tool". LLMs are handlers' internals — they
//    pick a *branch* of a sum-type output by writing `TYPE: <tag>`
//    on the first line, but never freely select what to do next.
//
//  - Failures are typed values, not exceptions. CompileError /
//    TestError / Obstacle / ClarificationNeeded are first-class types
//    handled by handlers like any other. See router/feedback.go
//    (Phase 5) for the enriched-Request loop that turns errors into
//    LLM-actionable retry inputs.
//
// This package supplies the bare infrastructure: TypedValue, Handler,
// Router, ParseFromLLM, LLMHandler. Concrete typecalc-state handlers
// land in router/typecalc.go (Phase 6) and the main agent loop swap
// in router/main_loop.go (Phase 7).
package router

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// TypedValue is the structured value flowing through the router.
// Type is the nominal tag (e.g. "Uncompiled<Lang<JS,Code>>",
// "TestError", "Confirmed<Code>") and uniquely determines which
// handler runs next.
//
// Content carries the payload. Its schema depends on Type and is
// validated only at handler boundaries — the router itself never
// inspects Content. Storing as json.RawMessage lets handlers unmarshal
// into their own Go types without forcing a shared schema layer.
//
// Channel / Lang / Meta are optional carrier-level annotations that
// nest the type semantically (Chan<sessionID, T>, Lang<L, T>) without
// each carrier inflating the Type string.
type TypedValue struct {
	Type    string          `json:"type"`
	Content json.RawMessage `json:"content,omitempty"`

	// Channel = session id this value belongs to (Chan<S, T> in the
	// TypeCalculator.md notation). Empty means "no session focus";
	// handlers that mutate session state read this to choose where
	// the diff lands.
	Channel string `json:"channel,omitempty"`

	// Lang annotates code values (Lang<L, T>). Optional; empty means
	// the type carries its own language info or doesn't need one.
	Lang string `json:"lang,omitempty"`

	// Meta carries cross-handler trace data: e.g. retry count, parent
	// task id, original LLM raw output. The router itself doesn't
	// touch it; handlers use it to reconstruct context after errors.
	Meta map[string]string `json:"meta,omitempty"`
}

// NewTypedValue constructs a TypedValue with the given type tag and
// payload. Payload is JSON-marshalled into Content. Returns an error
// only if payload is not JSON-marshallable (rare — usually a programmer
// bug like passing a channel).
func NewTypedValue(typeTag string, payload interface{}) (TypedValue, error) {
	if typeTag == "" {
		return TypedValue{}, fmt.Errorf("type tag is required")
	}
	if payload == nil {
		return TypedValue{Type: typeTag}, nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return TypedValue{}, fmt.Errorf("marshal payload for %s: %w", typeTag, err)
	}
	return TypedValue{Type: typeTag, Content: raw}, nil
}

// Unmarshal extracts the typed payload into `into`. Returns nil for
// empty Content (the typed-value has no payload, only its tag — common
// for terminal types like Confirmed<Code> where the carrier value is
// fully described by the channel and the previous accepted record).
func (v TypedValue) Unmarshal(into interface{}) error {
	if len(v.Content) == 0 {
		return nil
	}
	return json.Unmarshal(v.Content, into)
}

// Handler is the contract for one routable step. Given an input value
// of a known type, the handler MUST produce an output TypedValue of
// one of the declared sum-type branches OR return an error (only for
// infrastructural failures — application-level failures must be
// returned as typed values like CompileError).
//
// Handlers are pure with respect to control flow: they don't decide
// "what runs next"; the router does that based on the output Type.
type Handler interface {
	// Accepts returns the type tag this handler runs on. Exact match;
	// no pattern semantics. To handle multiple input types, register
	// the handler multiple times.
	Accepts() string

	// Outputs returns the set of output type tags this handler MAY
	// produce. Used at registration time to validate the router graph
	// is connected (every output must have a registered handler OR be
	// declared terminal). LLM handlers also use this list to constrain
	// the LLM's sum-branch choice via ParseFromLLM.
	Outputs() []string

	// Handle runs the step. Output's Type MUST be in Outputs(),
	// otherwise the router treats it as a programmer error and aborts.
	Handle(ctx context.Context, in TypedValue) (TypedValue, error)
}

// HandlerFunc is the function-shaped adapter for Handler. Use when
// a closure is more ergonomic than defining a named struct.
type HandlerFunc struct {
	In     string
	Out    []string
	Run    func(ctx context.Context, in TypedValue) (TypedValue, error)
}

func (h *HandlerFunc) Accepts() string                                                      { return h.In }
func (h *HandlerFunc) Outputs() []string                                                    { return h.Out }
func (h *HandlerFunc) Handle(ctx context.Context, in TypedValue) (TypedValue, error)        { return h.Run(ctx, in) }

// typeTagLine matches `TYPE: <tag>` at the start of an LLM reply, with
// optional whitespace and case-insensitive prefix. Captures the tag.
// Tag content allows angle-brackets, commas, alphanumerics, underscores
// (covers nested constructors like Tested<Code, Pass>).
var typeTagLine = regexp.MustCompile(`(?im)^\s*TYPE\s*[:：]\s*([A-Za-z_][A-Za-z0-9_<>,\s]*?)\s*$`)

// ParseFromLLM extracts a TypedValue from raw LLM output. Behavior:
//
//  1. Look for `TYPE: <tag>` on the first non-empty line. Tag must be
//     trimmed and normalized (whitespace inside angle brackets removed).
//  2. Verify the tag is in `allowed` (the handler's declared Outputs).
//     If not, return an error — the LLM picked an unauthorized branch.
//  3. The remainder of the reply (after the TYPE line) becomes the
//     Content as a raw JSON value if the LLM emitted JSON, else a
//     stringified payload wrapped in a JSON string.
//
// This is the only place LLM control flow enters the router: the LLM
// can ONLY pick a tag from `allowed`. It cannot "choose what to do
// next" outside that list.
func ParseFromLLM(raw string, allowed []string) (TypedValue, error) {
	if strings.TrimSpace(raw) == "" {
		return TypedValue{}, fmt.Errorf("LLM reply is empty")
	}
	match := typeTagLine.FindStringSubmatch(raw)
	if match == nil {
		return TypedValue{}, fmt.Errorf("LLM reply missing required `TYPE: <tag>` first-line tag — got prefix %q", truncate(raw, 80))
	}
	tag := normalizeTypeTag(match[1])
	if !inSet(tag, allowed) {
		return TypedValue{}, fmt.Errorf("LLM picked unauthorized branch %q — allowed: %v", tag, allowed)
	}
	// Strip the TYPE line plus any leading whitespace; rest is payload.
	body := typeTagLine.ReplaceAllString(raw, "")
	body = strings.TrimSpace(body)
	if body == "" {
		return TypedValue{Type: tag}, nil
	}
	// If body parses as JSON, store as-is. Otherwise wrap in a string.
	if isJSON(body) {
		return TypedValue{Type: tag, Content: json.RawMessage(body)}, nil
	}
	raw2, _ := json.Marshal(body)
	return TypedValue{Type: tag, Content: raw2}, nil
}

// normalizeTypeTag collapses whitespace inside angle brackets so
// "Tested< Code , Pass >" and "Tested<Code,Pass>" hash to the same tag.
func normalizeTypeTag(s string) string {
	s = strings.TrimSpace(s)
	out := strings.Builder{}
	inBracket := 0
	for _, r := range s {
		switch r {
		case '<':
			inBracket++
			out.WriteRune(r)
		case '>':
			if inBracket > 0 {
				inBracket--
			}
			out.WriteRune(r)
		case ' ', '\t':
			if inBracket == 0 {
				out.WriteRune(r)
			}
			// drop spaces inside <>
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

func inSet(s string, set []string) bool {
	for _, x := range set {
		if x == s {
			return true
		}
	}
	return false
}

func isJSON(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// Cheap structural check — full JSON validation happens later
	// during Unmarshal at the handler boundary.
	first := s[0]
	last := s[len(s)-1]
	return (first == '{' && last == '}') ||
		(first == '[' && last == ']') ||
		(first == '"' && last == '"') ||
		s == "true" || s == "false" || s == "null"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
