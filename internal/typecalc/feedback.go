package typecalc

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/creator915/Koncept_OS/internal/graph"
)

// FeedbackVerdict is the discriminator for the result of receive_feedback
// (§3). The LLM translates user-experience language into one of these
// technical actions.
type FeedbackVerdict string

const (
	FeedbackValueAdjust     FeedbackVerdict = "ValueAdjust"
	FeedbackLawMissing      FeedbackVerdict = "LawMissing"
	FeedbackDesignChange    FeedbackVerdict = "DesignChange"
	FeedbackCannotReproduce FeedbackVerdict = "CannotReproduce"
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
	// Decode NewValue as a JSON object/value and stash it under valueSpace.
	// We don't enforce a schema — the user is asking for a specific value.
	var v map[string]any
	if err := json.Unmarshal(d.NewValue, &v); err == nil {
		attr.ValueSpace = v
	} else {
		// Non-object value: wrap it in a one-key map to preserve shape.
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

// ApplyLawMissing implements rule apply_law_missing:
//
//	LawMissing<AttrPath, NewLaw> × Graph ⇒ Graph(updated) × AffectedModules
//
// Side-effects: the new Law is appended to the attribute's Laws.
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
// ["position", "y"]). The doc §5.4 uses dotted refinement chains for
// attribute drill-downs in user feedback. The root identifier must match
// an attribute id in the graph; the remaining segments are field paths
// inside the value space.
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

// collectDownstream returns the object ids that are affected when the
// given attribute changes — i.e. the transitive set of objects that
// directly or indirectly consume the attribute. Used by both
// ApplyValueAdjust and ApplyLawMissing to compute the AffectedModules
// list (the doc §3 says these objects "may need to be re-tested or
// re-implemented").
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
func NewValueAdjust(attrPath string, newValue any) *TypedValue {
	rawNew, _ := json.Marshal(newValue)
	d := ValueAdjustDetail{AttrPath: attrPath, NewValue: rawNew}
	raw, _ := json.Marshal(d)
	return &TypedValue{Kind: KindValueAdjust, Payload: string(raw)}
}

// NewLawMissing constructs a LawMissing typed value.
func NewLawMissing(attrPath, law string) *TypedValue {
	d := LawMissingDetail{AttrPath: attrPath, NewLaw: law}
	raw, _ := json.Marshal(d)
	return &TypedValue{Kind: KindLawMissing, Payload: string(raw)}
}

// NewDesignChange constructs a DesignChange typed value.
func NewDesignChange(reason string) *TypedValue {
	d := DesignChangeDetail{Reason: reason}
	raw, _ := json.Marshal(d)
	return &TypedValue{Kind: KindDesignChange, Payload: string(raw)}
}

// NewCannotReproduce constructs a CannotReproduce typed value.
func NewCannotReproduce(reason string) *TypedValue {
	d := CannotReproduceDetail{Reason: reason}
	raw, _ := json.Marshal(d)
	return &TypedValue{Kind: KindCannotReproduce, Payload: string(raw)}
}
