package review

import (
	"testing"
	"time"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// 2026-05-21 reconstruction-mode bypass for runtime_check.
//
// PB-30 batch #4/#5 cmatrix/figlet/tty-clock all reached review's
// runtime-trace-missing → Obstacle path even though the behavioral-
// equivalence oracle (./executable vs ./probe) had already locked the
// impl as equivalent. The trace-based runtime checks (output-port
// presence, value-space conformance, etc.) assume a function-call
// oracle with structured ports — that doesn't exist for CLI-rebuild
// objects whose impl is `./executable`. When a Characterization
// section is present and locked, those rules are not-applicable and
// must Skip rather than Fail.

func TestRuntimeCheck_ReconstructionMode_SkipsAllTraceChecks(t *testing.T) {
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := graph.NewGraph()
	implPath := "src/main.c"
	obj := graph.NewObject("defs/RenderFiglet.h", "render text to ASCII art via the loaded font")
	obj.Impl = &implPath
	obj.Produces = []string{"ascii_art"}
	obj.PortObservation = map[string]string{"ascii_art": "return"}
	g.Objects["RenderFiglet"] = obj

	// Write a Characterization section the way runEquivalenceOracle does —
	// LockedCount > 0 means behavioral equivalence vs ./probe passed.
	sec := &core.CharacterizationSection{
		SuiteID:        "equiv-RenderFiglet",
		Lang:           "C",
		OracleProperty: "deliverable is behaviorally equivalent to the reference ./probe over a 12-item gate-generated battery",
		LockedCount:    12,
		UnlockedCount:  0,
		Timestamp:      time.Now().UTC(),
	}
	if err := core.WriteCharacterization("RenderFiglet", sec); err != nil {
		t.Fatal(err)
	}

	report := RuntimeCheck(g, "RenderFiglet")
	issues := report.Issues()

	// No issue may be runtime-trace-missing — that was the PB-30 obstacle.
	for _, iss := range issues {
		if iss.Code == "runtime-trace-missing" {
			t.Errorf("reconstruction-mode object must NOT fire runtime-trace-missing (oracle is behavioral-equivalence, not trace-based); got: %+v", iss)
		}
	}

	// Each trace-based rule must be visible as Skipped (so the
	// aggregator's silent-pass guard sees them, like HTML branch).
	cov := report.Coverage()
	for _, code := range []string{
		"runtime-trace-missing",
		"runtime-trace-empty",
		"runtime-output-missing",
		"runtime-input-missing",
		"runtime-value-conforms",
		"runtime-trace-sparse",
		"runtime-temporal-frame",
	} {
		status, present := cov[code]
		if !present {
			t.Errorf("rule %q must appear in report so aggregator silent-pass guard catches it; coverage=%v", code, cov)
			continue
		}
		if status != core.StatusSkipped {
			t.Errorf("rule %q must be Skipped under reconstruction-mode bypass, got %v", code, status)
		}
	}
}

func TestRuntimeCheck_GreenfieldStillRequiresTrace(t *testing.T) {
	// Sentinel: when NO Characterization section is present (greenfield
	// project with synthesized unit tests), the trace-based rules must
	// fire normally. The reconstruction bypass must NOT loosen review
	// for greenfield runs.
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := graph.NewGraph()
	g.Attributes["result"] = graph.NewAttribute("defs/result.ts", "computed result")
	implPath := "src/Compute.ts"
	obj := graph.NewObject("defs/Compute.ts", "compute the result")
	obj.Impl = &implPath
	obj.Produces = []string{"result"}
	obj.PortObservation = map[string]string{"result": "return"}
	g.Objects["Compute"] = obj

	// No Characterization. No trace. Greenfield path.
	report := RuntimeCheck(g, "Compute")
	missing := false
	for _, iss := range report.Issues() {
		if iss.Code == "runtime-trace-missing" {
			missing = true
		}
	}
	if !missing {
		t.Errorf("greenfield run without trace must Fail runtime-trace-missing; report=%+v", report.Issues())
	}
}

func TestRuntimeCheck_CharacterizationWithZeroLockedDoesNotBypass(t *testing.T) {
	// If Characterization exists but LockedCount=0 (equivalence oracle
	// failed for every battery item), the impl is broken — but we should
	// NOT also silently skip trace rules. The bypass is gated on the
	// oracle having passed.
	cleanup := useTempEvidenceDir(t)
	defer cleanup()

	g := graph.NewGraph()
	implPath := "src/main.c"
	obj := graph.NewObject("defs/Broken.h", "broken impl")
	obj.Impl = &implPath
	obj.Produces = []string{"out"}
	obj.PortObservation = map[string]string{"out": "return"}
	g.Objects["Broken"] = obj

	sec := &core.CharacterizationSection{
		SuiteID:       "equiv-Broken",
		Lang:          "C",
		LockedCount:   0,
		UnlockedCount: 12,
		Timestamp:     time.Now().UTC(),
	}
	if err := core.WriteCharacterization("Broken", sec); err != nil {
		t.Fatal(err)
	}

	report := RuntimeCheck(g, "Broken")
	missing := false
	for _, iss := range report.Issues() {
		if iss.Code == "runtime-trace-missing" {
			missing = true
		}
	}
	if !missing {
		t.Errorf("Characterization with LockedCount=0 must NOT bypass trace check; report=%+v", report.Issues())
	}
}
