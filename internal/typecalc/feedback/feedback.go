// Package feedback implements §3 receive_feedback / apply_value_adjust /
// apply_law_missing — the user-feedback rules. The agent translates a
// natural-language complaint into one of four typed verdicts, and this
// package applies the value/law verdicts to the graph (the other two are
// surfaced for human follow-up).
package feedback

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/creator915/Koncept_OS/internal/graph"
	"github.com/creator915/Koncept_OS/internal/typecalc"
)

// Verdict is the discriminator for the result of receive_feedback (§3).
// The LLM translates user-experience language into one of these technical
// actions.
type Verdict string

const (
	VerdictValueAdjust     Verdict = "ValueAdjust"
	VerdictLawMissing      Verdict = "LawMissing"
	VerdictDesignChange    Verdict = "DesignChange"
	VerdictCannotReproduce Verdict = "CannotReproduce"
)

// ValueAdjustDetail is the payload for KindValueAdjust.
type ValueAdjustDetail struct {
	AttrPath string          `json:"attrPath"`
	NewValue json.RawMessage `json:"newValue"`
}

// LawMissingDetail is the payload for KindLawMissing.
type LawMissingDetail struct {
	AttrPath string `json:"attrPath"`
	NewLaw   string `json:"newLaw"`
}

// DesignChangeDetail is the payload for KindDesignChange.
type DesignChangeDetail struct {
	Reason string `json:"reason"`
}

// CannotReproduceDetail is the payload for KindCannotReproduce.
type CannotReproduceDetail struct {
	Reason string `json:"reason"`
}

// AffectedModules summarises the cascade of an apply_value_adjust /
// apply_law_missing rule (§3): the system propagates the change through
// the graph and lists every object whose status must be reset.
type AffectedModules struct {
	Objects []string `json:"objects"`
	Reason  string   `json:"reason"`
}

// ApplyValueAdjust implements rule apply_value_adjust:
//
//	ValueAdjust<AttrPath, NewValue> × Graph ⇒ Graph(updated) × AffectedModules
//
// Side-effects on g: the named attribute's ValueSpace is updated. We do
// NOT change graph.Status here — that's the caller's responsibility, since
// invalidating downstream modules has session/rollback implications.
func ApplyValueAdjust(g *graph.Graph, d *ValueAdjustDetail) (*AffectedModules, error) {
	if g == nil || d == nil {
		return nil, fmt.Errorf("ApplyValueAdjust: nil input")
	}
	attrID, _, err := SplitAttrPath(d.AttrPath)
	if err != nil {
		return nil, err
	}
	attr, ok := g.Attributes[attrID]
	if !ok {
		return nil, fmt.Errorf("attribute %s not in graph", attrID)
	}
	var v map[string]any
	if err := json.Unmarshal(d.NewValue, &v); err == nil {
		attr.ValueSpace = v
	} else {
		var raw any
		if err := json.Unmarshal(d.NewValue, &raw); err != nil {
			return nil, fmt.Errorf("decode NewValue: %w", err)
		}
		attr.ValueSpace = map[string]any{"value": raw}
	}
	affected := collectDownstream(g, attrID)
	return &AffectedModules{
		Objects: affected,
		Reason:  fmt.Sprintf("value of %s updated", attrID),
	}, nil
}

// ApplyLawMissing implements rule apply_law_missing.
func ApplyLawMissing(g *graph.Graph, d *LawMissingDetail) (*AffectedModules, error) {
	if g == nil || d == nil {
		return nil, fmt.Errorf("ApplyLawMissing: nil input")
	}
	attrID, _, err := SplitAttrPath(d.AttrPath)
	if err != nil {
		return nil, err
	}
	attr, ok := g.Attributes[attrID]
	if !ok {
		return nil, fmt.Errorf("attribute %s not in graph", attrID)
	}
	if !contains(attr.Laws, d.NewLaw) {
		attr.Laws = append(attr.Laws, d.NewLaw)
	}
	affected := collectDownstream(g, attrID)
	return &AffectedModules{
		Objects: affected,
		Reason:  fmt.Sprintf("law on %s added", attrID),
	}, nil
}

// SplitAttrPath splits "player_state.position.y" into ("player_state",
// ["position", "y"]). The root identifier must match an attribute id in
// the graph; the remaining segments are field paths inside the value space.
func SplitAttrPath(p string) (root string, sub []string, err error) {
	if p == "" {
		return "", nil, fmt.Errorf("empty attr path")
	}
	parts := splitDot(p)
	return parts[0], parts[1:], nil
}

func splitDot(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == '.' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// collectDownstream returns the object ids transitively affected when the
// given attribute changes.
func collectDownstream(g *graph.Graph, attrID string) []string {
	visited := map[string]bool{}
	var walk func(attr string)
	walk = func(attr string) {
		for objID, obj := range g.Objects {
			if !contains(obj.Consumes, attr) {
				continue
			}
			if visited[objID] {
				continue
			}
			visited[objID] = true
			for _, prod := range obj.Produces {
				walk(prod)
			}
		}
	}
	walk(attrID)
	out := make([]string, 0, len(visited))
	for k := range visited {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// NewValueAdjust constructs a ValueAdjust typed value.
func NewValueAdjust(attrPath string, newValue any) *typecalc.TypedValue {
	rawNew, _ := json.Marshal(newValue)
	d := ValueAdjustDetail{AttrPath: attrPath, NewValue: rawNew}
	raw, _ := json.Marshal(d)
	return &typecalc.TypedValue{Kind: typecalc.KindValueAdjust, Payload: string(raw)}
}

// NewLawMissing constructs a LawMissing typed value.
func NewLawMissing(attrPath, law string) *typecalc.TypedValue {
	d := LawMissingDetail{AttrPath: attrPath, NewLaw: law}
	raw, _ := json.Marshal(d)
	return &typecalc.TypedValue{Kind: typecalc.KindLawMissing, Payload: string(raw)}
}

// NewDesignChange constructs a DesignChange typed value.
func NewDesignChange(reason string) *typecalc.TypedValue {
	d := DesignChangeDetail{Reason: reason}
	raw, _ := json.Marshal(d)
	return &typecalc.TypedValue{Kind: typecalc.KindDesignChange, Payload: string(raw)}
}

// NewCannotReproduce constructs a CannotReproduce typed value.
func NewCannotReproduce(reason string) *typecalc.TypedValue {
	d := CannotReproduceDetail{Reason: reason}
	raw, _ := json.Marshal(d)
	return &typecalc.TypedValue{Kind: typecalc.KindCannotReproduce, Payload: string(raw)}
}
