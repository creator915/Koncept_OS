package services

import (
	"context"
	"testing"
)

// 2026-05-21 — vacuous-oracle guard.
//
// PB-30 batch #8 figlet exposed a structural hole: probe and run_local
// both routed to /workspace/executable in the agent's container, so the
// equivalence battery compared the same binary against itself and
// reported a fake 100% lock. The guard rejects oracle runs that would
// be vacuous — either because the agent's binary IS the reference, or
// because one of the two channels is missing.
//
// Real docker-mode coverage is integration-only (see
// tests/0520_real_pb after the batch #9 harness fix lands); this test
// covers the host-mode bypass (KCPOS_AMD64_CONTAINER unset) where the
// guard must be inert so non-PB SPEC reconstruction tasks aren't
// blocked.

func TestVacuousOracleCheck_NoContainerEnv_NoGuard(t *testing.T) {
	t.Setenv("KCPOS_AMD64_CONTAINER", "")
	if reason := vacuousOracleCheck(context.Background()); reason != "" {
		t.Errorf("guard must be inert when KCPOS_AMD64_CONTAINER is unset (no PB harness), got: %s", reason)
	}
}

func TestVacuousOracleCheck_FakeContainerName_Errors(t *testing.T) {
	// When KCPOS_AMD64_CONTAINER points at a container that doesn't
	// exist, docker exec errors. The guard must surface this as a
	// VACUOUS reason (block the oracle) rather than silently passing
	// through to the battery loop — a missing container would
	// otherwise have every probe / run_local call fail uniformly,
	// which the byte-equal compare would still tally as "matched".
	t.Setenv("KCPOS_AMD64_CONTAINER", "pbref-real-this-container-does-not-exist-xyz")
	reason := vacuousOracleCheck(context.Background())
	if reason == "" {
		t.Fatal("guard must surface a non-empty reason when /workspace/executable.ref cannot be hashed (container missing or harness misconfigured)")
	}
	// The diagnostic must name the actionable fix.
	if !contains(reason, "vacuous-oracle-guard") {
		t.Errorf("reason must be tagged 'vacuous-oracle-guard' so callers can identify the failure source: %s", reason)
	}
	if !contains(reason, "executable.ref") {
		t.Errorf("reason must name the .ref path the harness is expected to stage: %s", reason)
	}
}

// contains is a tiny needle-in-haystack helper (testify import-free).
func contains(s, needle string) bool {
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
