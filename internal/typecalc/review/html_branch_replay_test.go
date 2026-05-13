package review

import (
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
)

// TestHTMLBranchReplay_V93_05_InitWorld replays the v93-05 batch's
// InitWorld review failure to confirm v9.3.1 closes the
// runtime-trace-missing / port-observation-required / value-space-empty
// loop. Setup mirrors the archived bundle exactly:
//
//   - impl = index.html  (HTML deliverable → v9.3 chain HTML branch)
//   - implFragment = K/frags/InitWorld.js
//   - produces = [world_state, game_state]
//   - mutates  = []
//   - portObservation = nil   (agent skipped — chain doesn't use it for HTML)
//   - attribute valueSpaces  = nil for both (declared status, not yet
//     backfilled — gate's [attrs-backfilled] enforces this at root finish,
//     not at chain time)
//
// Pre-v9.3.1: review fails with:
//   staticIssues: [port-observation-required, value-space-empty x2]
//   runtimeIssues: [runtime-trace-missing]
//
// Post-v9.3.1: all four codes must NOT fire. The remaining issues
// (evidence-stale, spec-stale, frags-content-matches-def) are real
// hygiene problems the agent must fix in source — they're not part
// of this regression's scope.
func TestHTMLBranchReplay_V93_05_InitWorld(t *testing.T) {
	g := graph.NewGraph()

	// Two declared-status attributes with no valueSpace, mirroring
	// the archived state.
	g.Attributes["world_state"] = graph.NewAttribute("K/defs/world_state.js", "world tiles + biomes")
	g.Attributes["game_state"] = graph.NewAttribute("K/defs/game_state.js", "game-wide singleton")
	// (NewAttribute defaults to declared with no valueSpace, which is
	// exactly the state v93-05 was in when the chain ran review.)

	implPath := "index.html"
	fragPath := "K/frags/InitWorld.js"
	obj := graph.NewObject("K/defs/InitWorld.js", "Generate the world from a seed.")
	obj.Impl = &implPath
	obj.ImplFragment = &fragPath
	obj.Produces = []string{"world_state", "game_state"}
	// PortObservation intentionally left nil — agent skipped this in
	// the v93-05 run because the HTML branch doesn't use it.
	g.Objects["InitWorld"] = obj

	// Static check: HTML-branch carve-outs must apply.
	staticIssues := StaticCheck("", g, "InitWorld").Issues()
	for _, iss := range staticIssues {
		switch iss.Code {
		case "port-observation-required":
			t.Errorf("v9.3.1 bug: port-observation-required must NOT fire on HTML deliverable (chain doesn't use portObservation for HTML). Got: %s", iss.Message)
		case "value-space-empty":
			t.Errorf("v9.3.1 bug: value-space-empty must NOT fire on HTML deliverable mid-chain (gate's [attrs-backfilled] enforces at root finish, not here). Got: %s", iss.Message)
		case "runtime-trace-stale":
			t.Errorf("v9.3.1 bug: runtime-trace-stale must NOT fire on HTML deliverable (no trace is ever produced). Got: %s", iss.Message)
		}
	}

	// Runtime check: HTML deliverables produce no trace, so the entire
	// runtime check must return empty (NOT runtime-trace-missing).
	runtimeIssues := RuntimeCheck(g, "InitWorld").Issues()
	if len(runtimeIssues) > 0 {
		var codes []string
		for _, iss := range runtimeIssues {
			codes = append(codes, iss.Code)
		}
		t.Errorf("v9.3.1 bug: RuntimeCheck must return nil for HTML deliverables (chain skipped synth+test, no trace). Got: [%s]", strings.Join(codes, ", "))
	}
}

// TestHTMLBranchReplay_NonHTMLStillStrict verifies the carve-outs are
// HTML-scoped — non-HTML objects must still get the full set of checks.
// Without this guard, the v9.3.1 change could silently weaken
// verification for the test-harness path.
func TestHTMLBranchReplay_NonHTMLStillStrict(t *testing.T) {
	g := graph.NewGraph()
	g.Attributes["state"] = graph.NewAttribute("defs/state.go", "")
	// No valueSpace → expect value-space-empty.

	implPath := "src/Update.go" // Go impl → non-HTML branch.
	obj := graph.NewObject("defs/Update.go", "update state")
	obj.Impl = &implPath
	obj.Produces = []string{"state"}
	// PortObservation nil → expect port-observation-required.
	g.Objects["Update"] = obj

	issues := StaticCheck("", g, "Update").Issues()
	hasPortReq := false
	hasValueSpaceEmpty := false
	for _, iss := range issues {
		if iss.Code == "port-observation-required" {
			hasPortReq = true
		}
		if iss.Code == "value-space-empty" {
			hasValueSpaceEmpty = true
		}
	}
	if !hasPortReq {
		t.Error("non-HTML object missing portObservation must still fire port-observation-required (carve-out is HTML-scoped)")
	}
	if !hasValueSpaceEmpty {
		t.Error("non-HTML object's attribute without valueSpace must still fire value-space-empty (carve-out is HTML-scoped)")
	}

	rtIssues := RuntimeCheck(g, "Update").Issues()
	hasTraceMissing := false
	for _, iss := range rtIssues {
		if iss.Code == "runtime-trace-missing" {
			hasTraceMissing = true
		}
	}
	if !hasTraceMissing {
		t.Error("non-HTML object with no runtime trace must still fire runtime-trace-missing (carve-out is HTML-scoped)")
	}
}
