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
	"implContent":     true,  // v10: direct content storage
	"implLang":        true,  // v10: detected language
	"status":          true,
	"statusSession":   true,
	"temporal":        true,
	"preconditions":   true,
	"postconditions":  true,
	"portObservation": true,
	"storyPoints":     true,
	"storyRationale":  true,
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
			return fmt.Errorf("object merge: field %q is not mergeable (allowed: intent/impl/implFragment/implSymbol/implContent/implLang/status/statusSession/temporal/preconditions/postconditions/portObservation/storyPoints/storyRationale)", k)
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
		// v9.5: storyPoints >= 8 means the object must be split before
		// implementation can begin. Block declared -> implementing.
		if o.Status == StatusDeclared && s == StatusImplementing && o.StoryPoints >= 8 {
			return fmt.Errorf("object %q has storyPoints=%d (≥8) and must be split via graph_split_object before implementation begins", id, o.StoryPoints)
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
				return fmt.Errorf("portObservation[%q] must be string (one of: \"global\", \"return\", \"return.<path>\", \"args.<n>.<path>\", \"side_effect\"), got %T", k, val)
			}
			if !validPortObservation(s) {
				return fmt.Errorf(
					"portObservation[%q]=%q is not a recognised extractor. Allowed values:\n"+
						"  \"global\"            — port read from module/globalThis namespace\n"+
						"  \"return\"            — port IS the call's whole return value (Go single return; JS IIFE return)\n"+
						"  \"return.<path>\"     — port is a dotted-path field on the return value, e.g. \"return.ball.x\"\n"+
						"  \"args.<n>.<path>\"   — port is a field on the n-th positional argument (for mutating-arg APIs)\n"+
						"  \"side_effect\"       — port has only externally-observable effects (skip runtime check)\n"+
						"Common mistakes: \"return value\" / \"function return\" / \"output\" are NOT valid — use bare \"return\" for the whole return, or \"return.<field>\" for nested access.",
					k, s)
			}
			out[k] = s
		}
		o.PortObservation = out
	}
	// storyPoints: Fibonacci values only (1, 2, 3, 5, 8, 13)
	if v, has := patch["storyPoints"]; has {
		f, ok := v.(float64)
		if !ok {
			return fmt.Errorf("storyPoints must be a number")
		}
		n := int(f)
		validSP := map[int]bool{1: true, 2: true, 3: true, 5: true, 8: true, 13: true}
		if !validSP[n] {
			return fmt.Errorf("storyPoints must be one of 1, 2, 3, 5, 8, 13 (Fibonacci scale); got %d", n)
		}
		o.StoryPoints = n
	}
	// storyRationale: required when storyPoints is set, >= 10 chars
	if v, has := patch["storyRationale"]; has {
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("storyRationale must be string")
		}
		if o.StoryPoints > 0 && len(s) < 10 {
			return fmt.Errorf("storyRationale must be at least 10 characters when storyPoints is set; got %d chars", len(s))
		}
		o.StoryRationale = s
	}
	// implContent (v10): direct source code content stored in graph.
	// The chain reads this directly; files are projections only.
	if v, has := patch["implContent"]; has {
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("implContent must be string")
		}
		o.ImplContent = s
	}
	// implLang (v10): detected programming language of implContent.
	// 2026-05-21: the graph layer no longer hardcodes a whitelist —
	// internal/typecalc/lang/ is the single source of truth for which
	// languages have real compile + test invokers (currently: Go,
	// TypeScript, JavaScript, Python, Rust, C, HTML). When the agent
	// declares an unsupported lang, the chain's compile/test step
	// returns Insufficient with a stage-named reason, the SAME way
	// it does for typos or genuinely unsupported langs.
	//
	// Why this changed: the previous hardcoded whitelist had drifted
	// (it allowed Java/Haskell — never implemented — and refused C —
	// fully implemented). PB-30 batch #4's `entr` agent burned an hour
	// pivoting C→Go→TS→JS because the graph layer rejected `implLang="c"`
	// even though internal/typecalc/lang/compile.go has runCCompile.
	// Single source of truth is the only way to keep these in sync.
	if v, has := patch["implLang"]; has {
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("implLang must be string")
		}
		o.ImplLang = s
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
