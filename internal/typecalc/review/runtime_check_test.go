package review

import (
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
)

// writeTrace puts a core.RuntimeTrace section into the unified bundle at
// the canonical path inside the already-tempdir-cd'd test environment.
// Merges into any existing bundle (preserves other sections like Tests
// when both writeTrace and core.WriteTests are called in the same test).
func writeTrace(t *testing.T, objID string, calls []map[string]any) {
	t.Helper()
	rtCalls := make([]core.RuntimeCall, 0, len(calls))
	for _, c := range calls {
		var rc core.RuntimeCall
		if in, ok := c["inputs"].(map[string]any); ok {
			rc.Inputs = map[string]json.RawMessage{}
			for k, v := range in {
				raw, _ := json.Marshal(v)
				rc.Inputs[k] = raw
			}
		}
		if out, ok := c["outputs"].(map[string]any); ok {
			rc.Outputs = map[string]json.RawMessage{}
			for k, v := range out {
				raw, _ := json.Marshal(v)
				rc.Outputs[k] = raw
			}
		}
		rtCalls = append(rtCalls, rc)
	}
	if err := core.SetRuntimeTrace(objID, rtCalls); err != nil {
		t.Fatal(err)
	}
	// Silence unused-import warnings if tests no longer need these.
	_ = os.MkdirAll
	_ = filepath.Join
}

func newGraphWithRangeAttrs() *graph.Graph {
	g := graph.NewGraph()
	speed := graph.NewAttribute("defs/speed.go", "ball speed")
	speed.ValueSpace = map[string]any{"type": "number", "min": 0.0, "max": 10.0}
	g.Attributes["speed"] = speed
	dir := graph.NewAttribute("defs/dir.go", "direction enum")
	dir.ValueSpace = map[string]any{"type": "string", "enum": []any{"left", "right"}}
	g.Attributes["dir"] = dir
	implPath := "src/Move.go"
	o := graph.NewObject("defs/Move.go", "moves the ball")
	o.Impl = &implPath
	o.Consumes = []string{"speed"}
	o.Produces = []string{"dir"}
	g.Objects["Move"] = o
	return g
}

func TestRuntimeCheck_FlagsMissingTrace(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	g := newGraphWithRangeAttrs()
	issues := RuntimeCheck(g, "Move").Issues()
	if !hasIssue(issues, "runtime-trace-missing") {
		t.Fatalf("expected runtime-trace-missing, got %v", issues)
	}
}

func TestRuntimeCheck_FlagsEmptyTrace(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	g := newGraphWithRangeAttrs()
	writeTrace(t, "Move", []map[string]any{})
	issues := RuntimeCheck(g, "Move").Issues()
	if !hasIssue(issues, "runtime-trace-empty") {
		t.Fatalf("expected runtime-trace-empty, got %v", issues)
	}
}

func TestRuntimeCheck_FlagsMissingOutputPort(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	g := newGraphWithRangeAttrs()
	// Trace records a speed input but no dir output.
	writeTrace(t, "Move", []map[string]any{
		{
			"inputs":  map[string]any{"speed": 5.0},
			"outputs": map[string]any{},
		},
	})
	issues := RuntimeCheck(g, "Move").Issues()
	if !hasIssue(issues, "runtime-output-missing") {
		t.Fatalf("expected runtime-output-missing, got %v", issues)
	}
}

func TestRuntimeCheck_FlagsMissingInputPort(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	g := newGraphWithRangeAttrs()
	writeTrace(t, "Move", []map[string]any{
		{
			"inputs":  map[string]any{},
			"outputs": map[string]any{"dir": "left"},
		},
	})
	issues := RuntimeCheck(g, "Move").Issues()
	if !hasIssue(issues, "runtime-input-missing") {
		t.Fatalf("expected runtime-input-missing, got %v", issues)
	}
}

func TestRuntimeCheck_FlagsOutOfRange(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	g := newGraphWithRangeAttrs()
	writeTrace(t, "Move", []map[string]any{
		{
			// speed=99 violates max=10.
			"inputs":  map[string]any{"speed": 99},
			"outputs": map[string]any{"dir": "left"},
		},
	})
	issues := RuntimeCheck(g, "Move").Issues()
	if !hasIssue(issues, "runtime-out-of-range") {
		t.Fatalf("expected runtime-out-of-range, got %v", issues)
	}
}

func TestRuntimeCheck_FlagsTypeMismatch(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	g := newGraphWithRangeAttrs()
	writeTrace(t, "Move", []map[string]any{
		{
			// speed should be number but recorded as a string.
			"inputs":  map[string]any{"speed": "fast"},
			"outputs": map[string]any{"dir": "left"},
		},
	})
	issues := RuntimeCheck(g, "Move").Issues()
	if !hasIssue(issues, "runtime-type-mismatch") {
		t.Fatalf("expected runtime-type-mismatch, got %v", issues)
	}
}

func TestRuntimeCheck_FlagsEnumViolation(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	g := newGraphWithRangeAttrs()
	writeTrace(t, "Move", []map[string]any{
		{
			"inputs":  map[string]any{"speed": 5.0},
			"outputs": map[string]any{"dir": "diagonal"},
		},
	})
	issues := RuntimeCheck(g, "Move").Issues()
	if !hasIssue(issues, "runtime-enum-violation") {
		t.Fatalf("expected runtime-enum-violation, got %v", issues)
	}
}

func TestRuntimeCheck_PassesWhenInRange(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	g := newGraphWithRangeAttrs()
	writeTrace(t, "Move", []map[string]any{
		{
			"inputs":  map[string]any{"speed": 5.5},
			"outputs": map[string]any{"dir": "left"},
		},
		{
			"inputs":  map[string]any{"speed": 0.0},
			"outputs": map[string]any{"dir": "right"},
		},
	})
	issues := RuntimeCheck(g, "Move").Issues()
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues, got %d: %v", len(issues), issues)
	}
}

func TestRuntimeCheck_TemporalRequiresFrame(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()
	g := newGraphWithRangeAttrs()
	g.Objects["Move"].Temporal = &graph.Temporal{FrameVar: "e"}
	writeTrace(t, "Move", []map[string]any{
		{
			"inputs":  map[string]any{"speed": 5.0},
			"outputs": map[string]any{"dir": "left"},
		},
	})
	issues := RuntimeCheck(g, "Move").Issues()
	if !hasIssue(issues, "runtime-temporal-frame") {
		t.Fatalf("expected runtime-temporal-frame, got %v", issues)
	}
}
