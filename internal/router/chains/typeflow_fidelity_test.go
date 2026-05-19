package chains

import (
	"testing"

	"github.com/creator915/Koncept_OS/internal/router"
)

// P2.3 fidelity: the frozen router.ObservedTypeFlow must not drift from
// the REAL chain. Every frozen source type must be a registered handler
// In-type in the actual BuildChain, the frozen terminals must be the
// router's terminals, and the canonical spine's sources must all exist.
// This makes the freeze provably a projection of reality, not invention.

func TestTypeFlowFidelity_FrozenSourcesExistInRealChain(t *testing.T) {
	r, err := BuildChain(happyDeps())
	if err != nil {
		t.Fatalf("BuildChain: %v", err)
	}
	for _, rule := range router.ObservedTypeFlow {
		if !r.Has(rule.From) && !r.IsTerminal(rule.From) {
			t.Errorf("frozen flow source %q is not a real registered handler In-type — freeze drifted from reality", rule.From)
		}
	}
}

func TestTypeFlowFidelity_TerminalsAgree(t *testing.T) {
	r, err := BuildChain(happyDeps())
	if err != nil {
		t.Fatal(err)
	}
	for _, term := range []string{"Confirmed<Object>", "Obstacle<Object,Reason>"} {
		if !r.IsTerminal(term) {
			t.Errorf("router must treat %q as terminal (frozen flow says so)", term)
		}
		if !router.FlowIsTerminal(term) {
			t.Errorf("frozen flow must mark %q terminal (router says so)", term)
		}
	}
}

func TestTypeFlowFidelity_CanonicalSpineSourcesAreReal(t *testing.T) {
	r, err := BuildChain(happyDeps())
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range router.CanonicalSpine {
		if !r.Has(typ) && !r.IsTerminal(typ) {
			t.Errorf("spine type %q exists in neither handlers nor terminals of the real chain", typ)
		}
	}
}
