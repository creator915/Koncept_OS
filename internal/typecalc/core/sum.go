package core

import (
	"fmt"
	"strings"
)

// SumType is a set of Tags that an LLM-actored rule may legally produce.
// It enumerates the alternatives in a sum-type position from §4.3 of the
// design doc:
//
//	A | B | C  ⇒  SumType{A, B, C}
//
// The selector parses the LLM's free-form output, extracts the type label
// from the first non-empty line (`TYPE: <kind>`), and verifies that label
// is in the SumType. The rest of the output is the typed payload.
type SumType []Tag

// Includes reports whether tag is one of the legal alternatives.
func (s SumType) Includes(tag Tag) bool {
	for _, t := range s {
		if t == tag {
			return true
		}
	}
	return false
}

// FindKind returns the first tag in the sum whose Kind matches. Used
// when the LLM emits only a kind label (e.g. "TYPE: Uncompiled<Code>"
// or just "TYPE: CompileError"); we tolerate either flat or wrapped
// forms because requiring the full constructor tower in the label would
// be brittle.
func (s SumType) FindKind(k Kind) (Tag, bool) {
	for _, t := range s {
		if t.Kind == k {
			return t, true
		}
	}
	return Tag{}, false
}

// String renders the sum as "A | B | C".
func (s SumType) String() string {
	parts := make([]string, len(s))
	for i, t := range s {
		parts[i] = t.String()
	}
	return strings.Join(parts, " | ")
}

// ParseLLMOutput parses an LLM response intended to inhabit a sum type.
// The grammar (§4.3):
//
//	<output> ::= "TYPE:" <kind-label> "\n" <payload>
//
// The kind-label may be any of: a plain Kind ("Code"), a wrapped form
// ("Uncompiled<Lang<TypeScript,Code>>"), or a previously-seen Tag stringified
// by Tag.String(). We accept any form whose Kind component matches one of
// the legal alternatives in expected.
//
// Returns the matched Tag and the payload (the lines after the TYPE
// header). On failure returns a *TypedValue with Kind=KindFormatError so
// the caller can route it like any other typed value.
func ParseLLMOutput(raw string, expected SumType) (*TypedValue, error) {
	lines := strings.SplitN(raw, "\n", 2)
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return FormatErr("empty LLM output"), nil
	}
	header := strings.TrimSpace(lines[0])
	if !strings.HasPrefix(header, "TYPE:") {
		return FormatErr("first line must start with `TYPE:` — got %q", Trim(header, 80)), nil
	}
	label := strings.TrimSpace(strings.TrimPrefix(header, "TYPE:"))
	if label == "" {
		return FormatErr("`TYPE:` header has empty label"), nil
	}
	kind := extractKind(label)
	if kind == "" {
		return FormatErr("could not extract Kind from label %q", label), nil
	}
	tag, ok := expected.FindKind(kind)
	if !ok {
		return FormatErr("kind %q not in expected sum %s", kind, expected.String()), nil
	}
	payload := ""
	if len(lines) == 2 {
		payload = strings.TrimLeft(lines[1], "\n")
	}
	tv := New(tag.Kind, payload)
	tv.State = tag.State
	tv.Lang = tag.Lang
	return tv, nil
}

// extractKind pulls the innermost identifier out of a wrapped label.
// "Uncompiled<Lang<TypeScript,Code>>" → "Code"
// "CompileError"                       → "CompileError"
// "Lang<TypeScript,Code>"              → "Code"
//
// The innermost identifier is always the LAST identifier token in the
// label — every wrapping constructor (Uncompiled, Lang, Chan, Permitted)
// puts its content kind in the rightmost slot, so a simple "scan for the
// last identifier" suffices and avoids fragile bracket nesting.
func extractKind(label string) Kind {
	var last, cur []byte
	flush := func() {
		if len(cur) > 0 {
			last = append(last[:0], cur...)
			cur = cur[:0]
		}
	}
	for i := 0; i < len(label); i++ {
		c := label[i]
		isIdent := (c >= 'A' && c <= 'Z') ||
			(c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') ||
			c == '_'
		if isIdent {
			cur = append(cur, c)
		} else {
			flush()
		}
	}
	flush()
	_ = strings.TrimSpace // (kept import alive)
	return Kind(last)
}

func FormatErr(format string, args ...any) *TypedValue {
	return &TypedValue{
		Kind:    KindFormatError,
		Payload: fmt.Sprintf(format, args...),
	}
}

func Trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
