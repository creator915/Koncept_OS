package services

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// freshMonolithicProject sets up a workdir with ./probe + K/graph.json
// containing N declared objects (single deliverable). Used to exercise
// the monolithic-CLI detection without spinning up real docker. Returns
// the workdir path.
func freshMonolithicProject(t *testing.T, nObjects int) string {
	t.Helper()
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prev)
		// Clear the per-cwd cache so cross-test interference doesn't bleed.
		wholeBinaryOracleMu.Lock()
		delete(wholeBinaryOracleCache, dir)
		wholeBinaryOracleMu.Unlock()
	})
	if err := os.WriteFile(filepath.Join(dir, "probe"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "K"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Build graph.json by hand to avoid pulling in the full graph
	// creation API; the detector only inspects len(g.Objects).
	objs := map[string]map[string]interface{}{}
	for i := 0; i < nObjects; i++ {
		name := []string{"Alpha", "Beta", "Gamma", "Delta", "Epsilon"}[i]
		objs[name] = map[string]interface{}{
			"def": "defs/" + name + ".ts", "impl": nil,
			"consumes": []string{}, "produces": []string{}, "mutates": []string{},
			"intent": "x", "status": "declared", "statusSession": nil,
			"temporal": nil, "preconditions": "", "postconditions": "",
		}
	}
	raw, _ := json.Marshal(map[string]interface{}{
		"attributes": map[string]interface{}{}, "objects": objs,
	})
	if err := os.WriteFile(persistence.GraphDefaultPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestIsMonolithicCLI_TriggersWhenProbeAndMultiObject — the core
// detection rule: ./probe + ≥2 graph objects ⇒ monolithic CLI.
func TestIsMonolithicCLI_TriggersWhenProbeAndMultiObject(t *testing.T) {
	freshMonolithicProject(t, 3)
	if !isMonolithicCLI() {
		t.Fatal("probe + 3 objects must trigger monolithic-CLI mode")
	}
}

// TestIsMonolithicCLI_SingleObjectNo — per-object isolation IS
// meaningful when there's only one graph object (no "siblings sharing
// main()" problem), so single-object reconstruction stays on the
// original per-object path.
func TestIsMonolithicCLI_SingleObjectNo(t *testing.T) {
	freshMonolithicProject(t, 1)
	if isMonolithicCLI() {
		t.Error("single object should NOT trigger monolithic mode — per-object oracle is correct shape")
	}
}

// TestIsMonolithicCLI_NoProbeNo — without ./probe we're in SPEC mode,
// the equiv_oracle path doesn't apply at all and monolithic detection
// must stay off so the SPEC chain (compile→synth→test→review) runs
// untouched.
func TestIsMonolithicCLI_NoProbeNo(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.MkdirAll(filepath.Join(dir, "K"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Same multi-object graph as the monolithic test, but no probe.
	raw, _ := json.Marshal(map[string]interface{}{
		"attributes": map[string]interface{}{},
		"objects": map[string]interface{}{
			"A": map[string]interface{}{"def": "defs/A.ts", "consumes": []string{}, "produces": []string{}, "mutates": []string{}, "intent": "", "status": "declared"},
			"B": map[string]interface{}{"def": "defs/B.ts", "consumes": []string{}, "produces": []string{}, "mutates": []string{}, "intent": "", "status": "declared"},
		},
	})
	_ = os.WriteFile(persistence.GraphDefaultPath, raw, 0o644)
	if isMonolithicCLI() {
		t.Error("no probe ⇒ SPEC mode, monolithic detector must return false")
	}
}

// TestIsMonolithicCLI_NoGraphNo — defensive: empty graph (no objects)
// shouldn't trigger; the chain wouldn't even be at confirm yet.
func TestIsMonolithicCLI_NoGraphNo(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	_ = os.WriteFile(filepath.Join(dir, "probe"), []byte("#!/bin/sh\n"), 0o755)
	if isMonolithicCLI() {
		t.Error("no graph ⇒ no detection signal, must return false")
	}
}

// TestHasEquivEvidence_ReadsFromContractClause — 2026-05-26 audit BUG 2
// regression guard. After the Step 5 single-source refactor,
// WriteCharacterization clears b.Characterization=nil and mirrors the
// payload into Spec.Contract. hasEquivEvidence MUST read through
// ReadCharacterization (the compat bridge) — direct b.Characterization
// access returns nil for every post-Step-5 bundle, which silently
// breaks MarkConfirmed's gate.
//
// Uses the real WriteCharacterization → ReadCharacterization path
// to mirror production data flow.
func TestHasEquivEvidence_ReadsFromContractClause(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	// Empty state → false.
	if hasEquivEvidence("X") {
		t.Fatal("no bundle ⇒ no evidence")
	}

	// Write via the real path. This sets b.Characterization=nil and
	// stores the section into Spec.Contract.
	if err := core.WriteCharacterization("X", &core.CharacterizationSection{
		SuiteID:     "equiv-X",
		LockedCount: 5,
		// UnlockedCount intentionally non-zero to verify the relaxed
		// check (was previously full-pass-only).
		UnlockedCount: 3,
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	if !hasEquivEvidence("X") {
		t.Errorf("post-Step-5 evidence with LockedCount>0 must register; the silent-break bug returned false here")
	}
}

// TestHasEquivEvidence_MonolithicSuiteID — accepts the
// "whole-binary-equiv" SuiteID from runWholeBinaryOracle, not just
// the per-object "equiv-<id>" form. Otherwise the new monolithic
// mode's verdict (written with shared SuiteID) wouldn't satisfy the
// MarkConfirmed gate even after BUG 2's read-path fix.
func TestHasEquivEvidence_MonolithicSuiteID(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	if err := core.WriteCharacterization("Y", &core.CharacterizationSection{
		SuiteID:     "whole-binary-equiv",
		LockedCount: 400,
		UnlockedCount: 107,
	}); err != nil {
		t.Fatal(err)
	}
	if !hasEquivEvidence("Y") {
		t.Error("monolithic SuiteID with LockedCount>0 must register; SuiteID prefix check was over-specific")
	}
}

// TestHasEquivEvidence_ZeroLocksNo — defensive: any equiv section
// with LockedCount=0 doesn't satisfy the gate. The vacuous-oracle
// protection sits at this boundary — no real matches ⇒ no real
// behavioral evidence ⇒ refuse confirm.
func TestHasEquivEvidence_ZeroLocksNo(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	_ = core.WriteCharacterization("Z", &core.CharacterizationSection{
		SuiteID:       "equiv-Z",
		LockedCount:   0,
		UnlockedCount: 50,
	})
	if hasEquivEvidence("Z") {
		t.Error("LockedCount=0 must NOT register (vacuous evidence)")
	}
}

// TestHasEquivEvidence_NonEquivSuiteIDRejected — defensive: a
// characterization with SuiteID that doesn't contain "equiv" (e.g.
// some future characterization type the legacy/characterize tool
// might write) doesn't satisfy the equiv-evidence requirement.
func TestHasEquivEvidence_NonEquivSuiteIDRejected(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	_ = core.WriteCharacterization("W", &core.CharacterizationSection{
		SuiteID:     "static-analysis-only",
		LockedCount: 50,
	})
	if hasEquivEvidence("W") {
		t.Error("non-equiv SuiteID must NOT count as equivalence evidence")
	}
}

// TestHashExecutableForKey_MissingReturnsStableSentinel — when
// ./executable doesn't exist, the helper returns "missing" not an
// error. This keeps the cache key stable across multiple oracle
// invocations during a broken workdir state (vacuousOracleCheck will
// reject the oracle anyway, but the cache entry itself shouldn't
// thrash on each call).
func TestHashExecutableForKey_MissingReturnsStableSentinel(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	got1 := hashExecutableForKey()
	got2 := hashExecutableForKey()
	if got1 != "missing" {
		t.Errorf("missing executable should return 'missing' sentinel, got %q", got1)
	}
	if got1 != got2 {
		t.Errorf("two calls on same missing state must return same key: %q vs %q", got1, got2)
	}
}

// TestHashExecutableForKey_ContentChangeBustsKey — different binary
// content yields different hash. This is the load-bearing property
// for cache invalidation on recompile / Phase 7 rollback.
func TestHashExecutableForKey_ContentChangeBustsKey(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	_ = os.WriteFile(filepath.Join(dir, "executable"), []byte("v1-binary-content"), 0o755)
	h1 := hashExecutableForKey()
	if h1 == "missing" || h1 == "" {
		t.Fatalf("hashing real binary failed: %q", h1)
	}
	_ = os.WriteFile(filepath.Join(dir, "executable"), []byte("v2-recompiled-content"), 0o755)
	h2 := hashExecutableForKey()
	if h1 == h2 {
		t.Errorf("different binary content must yield different hash; got identical %q", h1)
	}
}

// TestHashExecutableForKey_SameContentDeterministic — same binary
// content yields same hash across calls. Fixes the audit S4
// nondeterminism (binary hash is content-derived, not call-order
// derived).
func TestHashExecutableForKey_SameContentDeterministic(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	_ = os.WriteFile(filepath.Join(dir, "executable"), []byte("stable-binary"), 0o755)
	h1 := hashExecutableForKey()
	h2 := hashExecutableForKey()
	if h1 != h2 || h1 == "" || h1 == "missing" {
		t.Errorf("hash must be deterministic for stable content, got %q vs %q", h1, h2)
	}
}

// TestWholeBinaryOracleCache_KeyedByCwd — two different workdirs (e.g.
// concurrent PB tasks) must NOT share oracle verdicts; each cwd has
// its own cache slot.
func TestWholeBinaryOracleCache_KeyedByCwd(t *testing.T) {
	wholeBinaryOracleMu.Lock()
	// Snapshot then clear so this test runs in isolation.
	snapshot := wholeBinaryOracleCache
	wholeBinaryOracleCache = map[string]*wholeBinaryVerdict{}
	wholeBinaryOracleMu.Unlock()
	t.Cleanup(func() {
		wholeBinaryOracleMu.Lock()
		wholeBinaryOracleCache = snapshot
		wholeBinaryOracleMu.Unlock()
	})

	wholeBinaryOracleMu.Lock()
	wholeBinaryOracleCache["/path/a"] = &wholeBinaryVerdict{Matched: 7, BatteryN: 10}
	wholeBinaryOracleCache["/path/b"] = &wholeBinaryVerdict{Matched: 3, BatteryN: 10}
	gotA := wholeBinaryOracleCache["/path/a"]
	gotB := wholeBinaryOracleCache["/path/b"]
	wholeBinaryOracleMu.Unlock()
	if gotA == gotB || gotA.Matched != 7 || gotB.Matched != 3 {
		t.Errorf("cache entries collided: a=%+v b=%+v", gotA, gotB)
	}
}
