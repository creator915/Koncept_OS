package typecalc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/graph"
)

func TestLocateFaultViaLLM_FallsBackWhenInvokerNil(t *testing.T) {
	g := graph.NewGraph()
	plan := &ProbePlanData{
		Points: []ProbePoint{{Attribute: "a", Producer: "P", TopoIndex: 0}},
	}
	obs := &ProbeResultData{
		Observations: []ProbeObservation{
			{Attribute: "a", Value: "1", Note: "diverges"},
		},
	}
	out, err := LocateFaultViaLLM(context.Background(), nil, g, nil, nil, plan, obs)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != KindFaultLocated {
		t.Fatalf("got %s", out.Tag())
	}
}

func TestLocateFaultViaLLM_StubInvoker(t *testing.T) {
	called := false
	env := &RuleEnv{
		LLMInvoker: func(ctx context.Context, env *RuleEnv, prompt string, expected SumType) (string, error) {
			called = true
			if !strings.Contains(prompt, "locate_fault") {
				t.Errorf("prompt should mention rule name: %s", prompt[:min(80, len(prompt))])
			}
			if !expected.Includes(Tag{Kind: KindFaultLocated}) {
				t.Errorf("expected sum should include FaultLocated")
			}
			return "TYPE: FaultLocated\n{\"moduleId\":\"P2\",\"attrPath\":\"b\",\"reason\":\"stub LLM verdict\"}", nil
		},
	}
	plan := &ProbePlanData{
		Points: []ProbePoint{
			{Attribute: "a", Producer: "P1", TopoIndex: 0, Consumers: []string{"P2"}},
			{Attribute: "b", Producer: "P2", TopoIndex: 1},
		},
	}
	obs := &ProbeResultData{
		Observations: []ProbeObservation{
			{Attribute: "a", Value: "ok"},
			{Attribute: "b", Value: "wrong", Note: "expected 100, got 999"},
		},
	}
	descriptions := map[string]string{"P2": "compute b from a"}
	signatures := map[string]string{"P2": "P2: a → b"}
	out, err := LocateFaultViaLLM(context.Background(), env, graph.NewGraph(), descriptions, signatures, plan, obs)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("LLM invoker was not called")
	}
	if out.Kind != KindFaultLocated {
		t.Fatalf("got %s", out.Tag())
	}
	d, _ := decodeFaultLocated(out)
	if d.ModuleID != "P2" {
		t.Fatalf("moduleId = %q", d.ModuleID)
	}
}

func TestLocateFaultViaLLM_AcceptsCannotReproduce(t *testing.T) {
	env := &RuleEnv{
		LLMInvoker: func(ctx context.Context, env *RuleEnv, prompt string, expected SumType) (string, error) {
			return "TYPE: CannotReproduce\n{\"reason\":\"observations all consistent\"}", nil
		},
	}
	plan := &ProbePlanData{Points: []ProbePoint{{Attribute: "a", Producer: "P", TopoIndex: 0}}}
	obs := &ProbeResultData{Observations: []ProbeObservation{{Attribute: "a", Value: "ok"}}}
	out, err := LocateFaultViaLLM(context.Background(), env, graph.NewGraph(), nil, nil, plan, obs)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != KindCannotReproduce {
		t.Fatalf("got %s", out.Tag())
	}
}

func decodeFaultLocated(tv *TypedValue) (*FaultLocatedDetail, error) {
	d := &FaultLocatedDetail{}
	return d, json.Unmarshal([]byte(tv.Payload), d)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
