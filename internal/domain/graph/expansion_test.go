package graph

import (
	"encoding/json"
	"strings"
	"testing"
)

// P1.1.1: Object.Expansion is the node→sub-hypergraph hyperlink.

// A freshly created object is NOT expanded (nil) — backward-compatible
// degradation: nil Expansion means "behaves exactly like today's flat
// graph".
func TestNewObject_ExpansionNilByDefault(t *testing.T) {
	o := NewObject("defs/Foo.ts", "demo")
	if o.Expansion != nil {
		t.Fatalf("new object must have nil Expansion, got %v", *o.Expansion)
	}
}

// json:"expansion,omitempty": nil must NOT serialize a key (keeps every
// existing K/graph.json byte-identical); a set value round-trips.
func TestObject_ExpansionJSONOmitempty(t *testing.T) {
	nilObj := NewObject("defs/A.ts", "a")
	raw, err := json.Marshal(nilObj)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "\"expansion\"") {
		t.Errorf("nil Expansion must be omitted from JSON, got: %s", raw)
	}

	sid := "s_physics"
	setObj := NewObject("defs/B.ts", "b")
	setObj.Expansion = &sid
	raw2, err := json.Marshal(setObj)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw2), "\"expansion\":\"s_physics\"") {
		t.Errorf("set Expansion must serialize, got: %s", raw2)
	}
	var back Object
	if err := json.Unmarshal(raw2, &back); err != nil {
		t.Fatal(err)
	}
	if back.Expansion == nil || *back.Expansion != "s_physics" {
		t.Errorf("Expansion did not round-trip: %+v", back.Expansion)
	}
}
