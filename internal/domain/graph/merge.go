package graph

import (
	"fmt"
	"strings"
)

// mergeableAttrFields lists fields a merge patch is allowed to touch.
// Structural fields (def, refines) require dedicated link/unlink ops to
// keep history attribution consistent.
var mergeableAttrFields = map[string]bool{
	"intent":        true,
	"status":        true,
	"statusSession": true,
	"valueSpace":    true,
	"confirmedOps":  true,
	"laws":          true,
}

var mergeableObjectFields = map[string]bool{
	"intent":          true,
	"impl":            true,
	"implFragment":    true,
	"implSymbol":      true,
	"status":          true,
	"statusSession":   true,
	"temporal":        true,
	"preconditions":   true,
	"postconditions":  true,
	"portObservation": true,
}

// MergeAttribute applies a partial JSON patch to an existing attribute.
// Only fields listed in mergeableAttrFields may appear in patch; unknown
// or structural fields are rejected to catch typos and prevent silent
// drift.
func (g *Graph) MergeAttribute(id string, patch map[string]any) error {
	a, ok := g.Attributes[id]
	if !ok {
		return fmt.Errorf("attribute %q not found", id)
	}
	for k := range patch {
		if !mergeableAttrFields[k] {
			return fmt.Errorf("attribute merge: field %q is not mergeable (allowed: intent/status/statusSession/valueSpace/confirmedOps/laws)", k)
		}
	}
	if v, has := patch["intent"]; has {
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("intent must be string")
		}
		a.Intent = s
	}
	if v, has := patch["status"]; has {
		s, ok := v.(string)
		if !ok || !validStatus(s) {
			return fmt.Errorf("status must be one of declared|implementing|confirmed")
		}
		if err := validStatusTransition(a.Status, s); err != nil {
			return fmt.Errorf("attribute %q: %w", id, err)
		}
		a.Status = s
	}
	if v, has := patch["statusSession"]; has {
		ptr, err := stringPtrOrNil(v, "statusSession")
		if err != nil {
			return err
		}
		a.StatusSession = ptr
	}
	if v, has := patch["valueSpace"]; has {
		if v == nil {
			a.ValueSpace = nil
		} else {
			m, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("valueSpace must be object or null")
			}
			a.ValueSpace = m
		}
	}
	if v, has := patch["confirmedOps"]; has {
		ops, err := stringSlice(v, "confirmedOps")
		if err != nil {
			return err
		}
		a.ConfirmedOps = ops
	}
	if v, has := patch["laws"]; has {
		laws, err := stringSlice(v, "laws")
		if err != nil {
			return err
		}
		a.Laws = laws
	}
	return nil
}

// MergeObject applies a partial JSON patch to an existing object.
func (g *Graph) MergeObject(id string, patch map[string]any) error {
	o, ok := g.Objects[id]
	if !ok {
		return fmt.Errorf("object %q not found", id)
	}
	for k := range patch {
		if !mergeableObjectFields[k] {
			return fmt.Errorf("object merge: field %q is not mergeable (allowed: intent/impl/implFragment/implSymbol/status/statusSession/temporal/preconditions/postconditions/portObservation)", k)
		}
	}
	if v, has := patch["intent"]; has {
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("intent must be string")
		}
		o.Intent = s
	}
	if v, has := patch["impl"]; has {
		ptr, err := stringPtrOrNil(v, "impl")
		if err != nil {
			return err
		}
		o.Impl = ptr
	}
	if v, has := patch["implFragment"]; has {
		ptr, err := stringPtrOrNil(v, "implFragment")
		if err != nil {
			return err
		}
		o.ImplFragment = ptr
	}
	if v, has := patch["implSymbol"]; has {
		if v == nil {
			o.ImplSymbol = ""
		} else {
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("implSymbol must be string or null")
			}
			o.ImplSymbol = s
		}
	}
	if v, has := patch["status"]; has {
		s, ok := v.(string)
		if !ok || !validStatus(s) {
			return fmt.Errorf("status must be one of declared|implementing|confirmed")
		}
		if err := validStatusTransition(o.Status, s); err != nil {
			return fmt.Errorf("object %q: %w", id, err)
		}
		o.Status = s
	}
	if v, has := patch["statusSession"]; has {
		ptr, err := stringPtrOrNil(v, "statusSession")
		if err != nil {
			return err
		}
		o.StatusSession = ptr
	}
	if v, has := patch["temporal"]; has {
		if v == nil {
			o.Temporal = nil
		} else {
			t, err := temporalFromAny(v)
			if err != nil {
				return err
			}
			o.Temporal = t
		}
	}
	if v, has := patch["preconditions"]; has {
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("preconditions must be string")
		}
		o.Preconditions = s
	}
	if v, has := patch["postconditions"]; has {
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("postconditions must be string")
		}
		o.Postconditions = s
	}
	if v, has := patch["portObservation"]; has {
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("portObservation must be an object {port: extractor}")
		}
		out := make(map[string]string, len(m))
		for k, val := range m {
			s, ok := val.(string)
			if !ok {
				return fmt.Errorf("portObservation[%q] must be string (e.g. \"global\", \"return.ball\", \"side_effect\"), got %T", k, val)
			}
			if !validPortObservation(s) {
				return fmt.Errorf("portObservation[%q]=%q is not a recognised extractor (allowed: \"global\", \"return.<path>\", \"args.<n>.<path>\", \"side_effect\")", k, s)
			}
			out[k] = s
		}
		o.PortObservation = out
	}
	return nil
}

// validPortObservation gatekeeps the small DSL of extractor strings.
// Extending this list requires touching the harness at the same time —
// keep it conservative.
func validPortObservation(s string) bool {
	if s == "global" || s == "side_effect" {
		return true
	}
	if strings.HasPrefix(s, "return.") || strings.HasPrefix(s, "return") && (s == "return") {
		return true
	}
	if strings.HasPrefix(s, "args.") {
		return true
	}
	return false
}

func validStatus(s string) bool {
	return s == StatusDeclared || s == StatusImplementing || s == StatusConfirmed
}

// validStatusTransition enforces the §5.2 state machine from the
// TypeCalculator design doc:
//
//	declared → implementing → confirmed
//
// No-op transitions (from == to) are allowed. Skipping implementing or
// demoting status are rejected; rollback is the only legal way out of
// confirmed (see internal/session/rollback.go).
func validStatusTransition(from, to string) error {
	if from == to || from == "" {
		return nil
	}
	allowed := map[string]string{
		StatusDeclared:     StatusImplementing,
		StatusImplementing: StatusConfirmed,
	}
	expected, ok := allowed[from]
	if !ok {
		return fmt.Errorf("status %s is terminal — rollback the session that confirmed this entity instead of mutating it", from)
	}
	if to != expected {
		return fmt.Errorf("illegal status transition %s → %s; the only legal next step is %s (docs/TypeCalculator.md §5.2)", from, to, expected)
	}
	return nil
}

func stringSlice(v any, field string) ([]string, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be array of strings", field)
	}
	out := make([]string, 0, len(arr))
	for i, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%d] must be string", field, i)
		}
		out = append(out, s)
	}
	return out, nil
}

func stringPtrOrNil(v any, field string) (*string, error) {
	if v == nil {
		return nil, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("%s must be string or null", field)
	}
	return &s, nil
}

func temporalFromAny(v any) (*Temporal, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("temporal must be object or null")
	}
	t := &Temporal{}
	if frameVar, ok := m["frameVar"].(string); ok {
		t.FrameVar = frameVar
	} else {
		return nil, fmt.Errorf("temporal.frameVar required (string)")
	}
	consumes, err := frameRefsFromAny(m["consumes"], "consumes")
	if err != nil {
		return nil, err
	}
	produces, err := frameRefsFromAny(m["produces"], "produces")
	if err != nil {
		return nil, err
	}
	t.Consumes = consumes
	t.Produces = produces
	return t, nil
}

func frameRefsFromAny(v any, field string) ([]FrameRef, error) {
	if v == nil {
		return []FrameRef{}, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("temporal.%s must be array", field)
	}
	out := make([]FrameRef, 0, len(arr))
	for i, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("temporal.%s[%d] must be object", field, i)
		}
		attr, _ := m["attribute"].(string)
		frame, _ := m["frame"].(string)
		if attr == "" || frame == "" {
			return nil, fmt.Errorf("temporal.%s[%d] requires attribute and frame", field, i)
		}
		out = append(out, FrameRef{Attribute: attr, Frame: frame})
	}
	return out, nil
}
