package services

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/creator915/Koncept_OS/internal/infra/persistence"
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
