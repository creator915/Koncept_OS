package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/checkpoint"
	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"github.com/creator915/Koncept_OS/internal/router"
	"github.com/creator915/Koncept_OS/internal/tools"
)

// stubDeps builds a Deps suitable for connectivity / dispatch tests.
// Client is nil — tests that actually invoke creative Handlers must
// not exercise them. Deterministic Handlers run.
func stubDeps(rootID string) *OuterDeps {
	return &OuterDeps{
		Client:            nil,
		ToolSet:           tools.Builtins(),
		RootSessionID:     rootID,
		SpecPath:          "SPEC.md",
		TaskDescription:   "test",
		PerHandlerMaxIter: 1,
		MaxRouterSteps:    50,
	}
}

// TestOuterRouter_RegistrationIsClosed verifies BuildOuterRouter
// returns no orphan output types — every Handler's declared Outputs
// is either a registered Handler's input or a Terminal. This is the
// structural "the state graph is closed" invariant.
func TestOuterRouter_RegistrationIsClosed(t *testing.T) {
	r, err := BuildOuterRouter(stubDeps("s_test"))
	if err != nil {
		t.Fatalf("BuildOuterRouter must succeed with valid deps: %v", err)
	}
	if orphans := r.Connectivity(); len(orphans) > 0 {
		t.Fatalf("orphan outputs (not registered, not terminal): %v", orphans)
	}
}

// TestOuterFlowRules_MatchRegisteredEdges verifies the OuterFlowRules
// table is in sync with the actually-registered Handler edges. Every
// rule's From must have a registered Handler (or be terminal — but
// terminals shouldn't appear as From); every To must be a registered
// In or terminal.
func TestOuterFlowRules_MatchRegisteredEdges(t *testing.T) {
	r, err := BuildOuterRouter(stubDeps("s_test"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range router.OuterFlowRules {
		if router.IsOuterTerminal(rule.From) {
			t.Errorf("FlowRule.From %q must not be terminal", rule.From)
			continue
		}
		if !r.Has(rule.From) {
			t.Errorf("FlowRule.From %q has no registered handler", rule.From)
		}
		for _, to := range rule.To {
			if router.IsOuterTerminal(to) {
				continue
			}
			if !r.Has(to) {
				t.Errorf("FlowRule edge %q → %q: To has no registered handler and is not terminal", rule.From, to)
			}
		}
	}
}

// TestOuterFlowAllows asserts a few canonical happy-path edges + a
// few feedback-arc edges are declared in OuterFlowRules.
func TestOuterFlowAllows(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{router.OuterTypeTask, router.OuterTypeArchitecture, true},
		{router.OuterTypeArchitecture, router.OuterTypeGraphDeclared, true},
		{router.OuterTypeGraphDeclared, router.OuterTypeSomeConfirmed, true},
		{router.OuterTypeSomeConfirmed, router.OuterTypeAllConfirmed, true},
		{router.OuterTypeAllConfirmed, router.OuterTypeAggregated, true},
		{router.OuterTypeAggregated, router.OuterTypeBuilt, true},
		{router.OuterTypeAggregated, router.OuterTypeCheckpointed, true}, // multi-file skip
		{router.OuterTypeCheckpointed, router.OuterTypeGatePassed, true},
		{router.OuterTypeGatePassed, router.OuterTypeFinished, true},
		// Feedback arcs
		{router.OuterTypeGateFailObject, router.OuterTypeSomeConfirmed, true},
		{router.OuterTypeGateFailCheckpoint, router.OuterTypeCheckpointed, true},
		{router.OuterTypeGateFailBuild, router.OuterTypeBuilt, true},
		// Illegal jumps
		{router.OuterTypeTask, router.OuterTypeFinished, false},
		{router.OuterTypeArchitecture, router.OuterTypeCheckpointed, false},
	}
	for _, c := range cases {
		got := router.OuterFlowAllows(c.from, c.to)
		if got != c.want {
			t.Errorf("OuterFlowAllows(%q, %q) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

// TestInferOuterState_NoSession returns Outer.Task.
func TestInferOuterState_NoSession(t *testing.T) {
	chdirTmp(t)
	if got := router.InferOuterState("s_root"); got != router.OuterTypeTask {
		t.Errorf("empty workspace must infer Outer.Task, got %q", got)
	}
}

// TestInferOuterState_ArchitectureOnly recognises an architecture-set
// session with no graph as Outer.Architecture.
func TestInferOuterState_ArchitectureOnly(t *testing.T) {
	chdirTmp(t)
	s := session.New("s_root", "", "task", session.Input{})
	s.Status = session.StatusActive
	s.Output.Architecture = "## subsystems: ..."
	if err := persistence.SaveSession(persistence.SessionDefaultDir, s); err != nil {
		t.Fatal(err)
	}
	if got := router.InferOuterState("s_root"); got != router.OuterTypeArchitecture {
		t.Errorf("got %q, want Outer.Architecture", got)
	}
}

// TestInferOuterState_GraphDeclared sees objects but none confirmed.
func TestInferOuterState_GraphDeclared(t *testing.T) {
	chdirTmp(t)
	s := session.New("s_root", "", "task", session.Input{})
	s.Status = session.StatusActive
	s.Output.Architecture = "x"
	_ = persistence.SaveSession(persistence.SessionDefaultDir, s)
	g := graph.NewGraph()
	g.Objects["A"] = graph.NewObject("defs/A.ts", "A")
	g.Objects["B"] = graph.NewObject("defs/B.ts", "B")
	_ = persistence.SaveGraph(persistence.GraphDefaultPath, g)
	if got := router.InferOuterState("s_root"); got != router.OuterTypeGraphDeclared {
		t.Errorf("got %q, want Outer.GraphDeclared", got)
	}
}

// TestInferOuterState_SomeConfirmed: one object confirmed, one not.
func TestInferOuterState_SomeConfirmed(t *testing.T) {
	chdirTmp(t)
	s := session.New("s_root", "", "task", session.Input{})
	s.Status = session.StatusActive
	s.Output.Architecture = "x"
	_ = persistence.SaveSession(persistence.SessionDefaultDir, s)
	g := graph.NewGraph()
	objA := graph.NewObject("defs/A.ts", "A")
	objA.Status = graph.StatusConfirmed
	g.Objects["A"] = objA
	g.Objects["B"] = graph.NewObject("defs/B.ts", "B")
	_ = persistence.SaveGraph(persistence.GraphDefaultPath, g)
	if got := router.InferOuterState("s_root"); got != router.OuterTypeSomeConfirmed {
		t.Errorf("got %q, want Outer.SomeConfirmed", got)
	}
}

// TestInferOuterState_AllConfirmed: every object confirmed but no aggregate.
func TestInferOuterState_AllConfirmed(t *testing.T) {
	chdirTmp(t)
	s := session.New("s_root", "", "task", session.Input{})
	s.Status = session.StatusActive
	s.Output.Architecture = "x"
	_ = persistence.SaveSession(persistence.SessionDefaultDir, s)
	g := graph.NewGraph()
	objA := graph.NewObject("defs/A.ts", "A")
	objA.Status = graph.StatusConfirmed
	g.Objects["A"] = objA
	_ = persistence.SaveGraph(persistence.GraphDefaultPath, g)
	if got := router.InferOuterState("s_root"); got != router.OuterTypeAllConfirmed {
		t.Errorf("got %q, want Outer.AllConfirmed", got)
	}
}

// TestInferOuterState_Finished: session status finished + checkpoint PASS.
func TestInferOuterState_Finished(t *testing.T) {
	chdirTmp(t)
	s := session.New("s_root", "", "task", session.Input{})
	s.Status = session.StatusFinished
	s.Output.Architecture = "x"
	s.Output.Implementations = []string{"src/A.ts"}
	_ = persistence.SaveSession(persistence.SessionDefaultDir, s)
	g := graph.NewGraph()
	objA := graph.NewObject("defs/A.ts", "A")
	objA.Status = graph.StatusConfirmed
	g.Objects["A"] = objA
	_ = persistence.SaveGraph(persistence.GraphDefaultPath, g)
	mustFreezeAndFill(t, "C1")
	if got := router.InferOuterState("s_root"); got != router.OuterTypeFinished {
		t.Errorf("got %q, want Outer.Finished", got)
	}
}

// mustFreezeAndFill writes a checkpoint with one frozen, filled must item.
// SaveCheckpoint recomputes Summary on save, so a hand-set "PASS" without
// items gets reset to PENDING — fixtures must add items + freeze + fill
// to land at PASS.
func mustFreezeAndFill(t *testing.T, itemID string) {
	t.Helper()
	c := checkpoint.New()
	c.Items = append(c.Items, checkpoint.Item{
		ID:        itemID,
		Severity:  checkpoint.SeverityMust,
		CodeProof: "src/A.ts:1 export A",
	})
	c.Frozen = true
	if err := persistence.SaveCheckpoint(persistence.CheckpointDefaultPath, c); err != nil {
		t.Fatal(err)
	}
}

// TestRunRoutedTurn_ResumeAtFinished: if disk already says Finished,
// the loop returns immediately with no Handler invocations.
func TestRunRoutedTurn_ResumeAtFinished(t *testing.T) {
	chdirTmp(t)
	s := session.New("s_root", "", "task", session.Input{})
	s.Status = session.StatusFinished
	s.Output.Architecture = "x"
	s.Output.Implementations = []string{"src/A.ts"}
	_ = persistence.SaveSession(persistence.SessionDefaultDir, s)
	g := graph.NewGraph()
	objA := graph.NewObject("defs/A.ts", "A")
	objA.Status = graph.StatusConfirmed
	g.Objects["A"] = objA
	_ = persistence.SaveGraph(persistence.GraphDefaultPath, g)
	mustFreezeAndFill(t, "C1")

	res, err := RunRoutedTurn(context.Background(), stubDeps("s_root"))
	if err != nil {
		t.Fatal(err)
	}
	if res.TerminalType != router.OuterTypeFinished {
		t.Errorf("terminal type = %q, want Outer.Finished", res.TerminalType)
	}
	if res.Steps != 0 {
		t.Errorf("steps = %d, want 0 (already at terminal)", res.Steps)
	}
}

// TestRunRoutedTurn_TraceIsJSONSerializable: a finished run produces a
// trace + terminal payload that round-trips through JSON cleanly.
func TestRunRoutedTurn_TraceIsJSONSerializable(t *testing.T) {
	chdirTmp(t)
	s := session.New("s_root", "", "task", session.Input{})
	s.Status = session.StatusFinished
	s.Output.Implementations = []string{"src/A.ts"}
	_ = persistence.SaveSession(persistence.SessionDefaultDir, s)
	g := graph.NewGraph()
	objA := graph.NewObject("defs/A.ts", "A")
	objA.Status = graph.StatusConfirmed
	g.Objects["A"] = objA
	_ = persistence.SaveGraph(persistence.GraphDefaultPath, g)
	mustFreezeAndFill(t, "C1")

	res, _ := RunRoutedTurn(context.Background(), stubDeps("s_root"))
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("RoutedRunResult must round-trip JSON: %v", err)
	}
	var back RoutedRunResult
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	if back.TerminalType != res.TerminalType {
		t.Errorf("terminal type mismatch: %q vs %q", back.TerminalType, res.TerminalType)
	}
}

// chdirTmp creates a tmpdir, cd's into it, restores cwd on cleanup.
func chdirTmp(t *testing.T) string {
	t.Helper()
	prev, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	// Pre-create K/sessions so tests with no session still don't crash on save.
	_ = os.MkdirAll(filepath.Join(dir, "K", "sessions"), 0o755)
	return dir
}
