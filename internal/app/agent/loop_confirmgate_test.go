package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// ③ regression guard. hasConfirmedDeliverable must be false when there
// is no graph / no confirmed object, and true only when an object is
// genuinely at status=confirmed. The loop's termination gate keys on
// this; weakening it re-opens "finish without verification".
func TestHasConfirmedDeliverable(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	if hasConfirmedDeliverable() {
		t.Fatal("no graph at all ⇒ must be false (nothing verified)")
	}

	if err := os.MkdirAll("K", 0o755); err != nil {
		t.Fatal(err)
	}
	// One object, NOT confirmed.
	notYet := `{"attributes":{},"objects":{"A":{"def":"defs/A.ts","impl":"a.go",` +
		`"consumes":[],"produces":[],"mutates":[],"intent":"","temporal":null,` +
		`"preconditions":"","postconditions":"","status":"implementing","statusSession":null}}}`
	if err := os.WriteFile(filepath.Join("K", "graph.json"), []byte(notYet), 0o644); err != nil {
		t.Fatal(err)
	}
	if hasConfirmedDeliverable() {
		t.Fatal("object at status=implementing ⇒ must be false")
	}

	confirmed := `{"attributes":{},"objects":{"A":{"def":"defs/A.ts","impl":"a.go",` +
		`"consumes":[],"produces":[],"mutates":[],"intent":"","temporal":null,` +
		`"preconditions":"","postconditions":"","status":"confirmed","statusSession":null}}}`
	if err := os.WriteFile(filepath.Join("K", "graph.json"), []byte(confirmed), 0o644); err != nil {
		t.Fatal(err)
	}
	if !hasConfirmedDeliverable() {
		t.Fatal("object at status=confirmed ⇒ must be true")
	}
}

// The nudge bound must be a positive finite number — the gate refuses
// then fails explicitly, it never spins forever.
func TestConfirmGateBoundIsFinite(t *testing.T) {
	if maxConfirmGateNudges <= 0 {
		t.Fatalf("maxConfirmGateNudges must be > 0, got %d", maxConfirmGateNudges)
	}
}
