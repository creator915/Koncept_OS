package chains

import (
	"context"
	"errors"
	"testing"

	"github.com/creator915/Koncept_OS/internal/router"
)

// TestChain_CharacterizeFrontStage proves the brownfield front stage
// actually flows: StartCharacterize → (engine recovers the lock) →
// Characterized → the SAME runCompile shared with StartConfirm → the
// unchanged greenfield pipeline → Confirmed. Before this test the
// front-stage wiring was only "compiles + connectivity-valid"; this
// exercises the path end to end.
func TestChain_CharacterizeFrontStage(t *testing.T) {
	deps := happyDeps()
	called := false
	deps.Characterize = func(ctx context.Context, id string) (int, int, error) {
		called = true
		if id != "Legacy" {
			t.Fatalf("characterize got wrong object id %q", id)
		}
		return 8, 0, nil // 8 locked, 0 unlocked
	}

	r, err := BuildChain(deps)
	if err != nil {
		t.Fatal(err)
	}
	in, _ := router.NewTypedValue(TypeStartCharacterize, StartCharacterizePayload{ObjectID: "Legacy"})
	out, err := r.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("Characterize dep was never invoked — front stage did not run")
	}
	if out.Type != TypeConfirmed {
		t.Fatalf("expected the characterized object to flow through the existing pipeline to %s, got %s",
			TypeConfirmed, out.Type)
	}
	var p ConfirmedPayload
	if err := out.Unmarshal(&p); err != nil {
		t.Fatal(err)
	}
	if p.ObjectID != "Legacy" {
		t.Fatalf("object id lost across the front stage: %q", p.ObjectID)
	}
}

// TestChain_CharacterizeFailureBecomesObstacle verifies an engine error
// in the front stage escalates rather than silently entering the
// pipeline (a failed characterization is NOT a safe basis to verify).
func TestChain_CharacterizeFailureBecomesObstacle(t *testing.T) {
	deps := happyDeps()
	deps.Characterize = func(ctx context.Context, id string) (int, int, error) {
		return 0, 0, errors.New("synthesizer produced no probes")
	}
	r, _ := BuildChain(deps)
	in, _ := router.NewTypedValue(TypeStartCharacterize, StartCharacterizePayload{ObjectID: "Legacy"})
	out, err := r.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != TypeObstacle {
		t.Fatalf("characterize failure must escalate to %s, got %s", TypeObstacle, out.Type)
	}
}

// TestChain_FrontStageIsOptional proves the additive guarantee: when
// Deps.Characterize is nil the front-stage handlers are NOT registered,
// so the chain is exactly the pre-existing greenfield machine and a
// StartCharacterize value has no handler (router refuses it). This is
// what keeps every existing caller/test byte-compatible.
func TestChain_FrontStageIsOptional(t *testing.T) {
	deps := happyDeps() // no Characterize set
	r, err := BuildChain(deps)
	if err != nil {
		t.Fatal(err)
	}
	in, _ := router.NewTypedValue(TypeStartCharacterize, StartCharacterizePayload{ObjectID: "Legacy"})
	if _, err := r.Run(context.Background(), in); err == nil {
		t.Fatal("StartCharacterize must be unhandled when Characterize dep is nil (front stage is opt-in/additive)")
	}
	// And the greenfield entry still works unchanged.
	gin, _ := router.NewTypedValue(TypeStartConfirm, StartConfirmPayload{ObjectID: "Foo"})
	gout, err := r.Run(context.Background(), gin)
	if err != nil {
		t.Fatal(err)
	}
	if gout.Type != TypeConfirmed {
		t.Fatalf("greenfield path regressed: expected %s, got %s", TypeConfirmed, gout.Type)
	}
}
