package agentctx

import (
	"context"
	"strings"
	"testing"
)

// Tests for the v9.6 depth + dispatch-mode helpers introduced to stop
// the main conversation from eating LLM stream timeouts on large graphs
// (fx batch 2026-05-14).

func TestDepth_DefaultsToZero(t *testing.T) {
	if got := Depth(nil); got != 0 {
		t.Errorf("nil ctx: want 0, got %d", got)
	}
	if got := Depth(context.Background()); got != 0 {
		t.Errorf("bg ctx: want 0, got %d", got)
	}
}

func TestDepth_RoundTrip(t *testing.T) {
	ctx := WithDepth(context.Background(), 2)
	if got := Depth(ctx); got != 2 {
		t.Errorf("after WithDepth(2): want 2, got %d", got)
	}
}

func TestCheckMainImplWork_SubagentNeverBlocked(t *testing.T) {
	// Subagent (depth >= 1) can always do impl work regardless of count.
	ctx := WithDepth(context.Background(), 1)
	if err := CheckMainImplWork(ctx, 100, "write_file"); err != nil {
		t.Errorf("subagent should never be blocked, got: %v", err)
	}
}

func TestCheckMainImplWork_MainBelowThreshold(t *testing.T) {
	// Main conversation with small graph — allowed.
	ctx := context.Background()
	if err := CheckMainImplWork(ctx, DispatchModeThreshold-1, "write_file"); err != nil {
		t.Errorf("main with %d objects (< threshold %d) should be allowed, got: %v",
			DispatchModeThreshold-1, DispatchModeThreshold, err)
	}
}

func TestCheckMainImplWork_MainAtThreshold_Blocked(t *testing.T) {
	ctx := context.Background()
	err := CheckMainImplWork(ctx, DispatchModeThreshold, "write_file K/defs/Foo.go")
	if err == nil {
		t.Fatalf("main at threshold should be blocked, got nil error")
	}
	if !strings.Contains(err.Error(), "spawn_subagent") {
		t.Errorf("error should mention spawn_subagent as remediation: %v", err)
	}
	if !strings.Contains(err.Error(), "write_file K/defs/Foo.go") {
		t.Errorf("error should include the action that was blocked: %v", err)
	}
}

func TestCheckMainImplWork_MainAboveThreshold_Blocked(t *testing.T) {
	ctx := context.Background()
	if err := CheckMainImplWork(ctx, DispatchModeThreshold+5, "confirm_object"); err == nil {
		t.Errorf("main with %d objects (> threshold) should be blocked",
			DispatchModeThreshold+5)
	}
}
