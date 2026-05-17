package characterize

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// stubSynth returns fixed probes regardless of spec — the engine must
// not depend on synthesizer cleverness, only on its stimuli.
func stubSynth(probes []CharProbe) SynthProbes {
	return func(_ context.Context, _ ProbeSpec) ([]CharProbe, error) {
		return probes, nil
	}
}

// fakeHarness returns a fixed trace — simulating what the UNTRUSTED
// legacy artifact actually did, without running any process.
func fakeHarness(tr *core.RuntimeTrace) RunHarness {
	return func(_ context.Context, _ HarnessRequest) (*core.RuntimeTrace, error) {
		return tr, nil
	}
}

func raw(v string) json.RawMessage { return json.RawMessage(v) }

func TestCharacterize_TranscribesObservedBehaviorIntoGoldenLock(t *testing.T) {
	probes := []CharProbe{
		{Name: "happy", Call: "IMPL.f(2)"},
		{Name: "zero", Call: "IMPL.f(0)"},
		{Name: "raises", Call: "IMPL.f(-1)"}, // legacy raises ⇒ no observable output
	}
	// Legacy artifact's ACTUAL behavior (the thing we don't trust but
	// must preserve): f(2)→4, f(0)→0, f(-1)→<nothing observed>.
	trace := &core.RuntimeTrace{
		ObjectID: "Squarer",
		Calls: []core.RuntimeCall{
			{Outputs: map[string]json.RawMessage{"result": raw("4")}},
			{Outputs: map[string]json.RawMessage{"result": raw("0")}},
			{Outputs: map[string]json.RawMessage{}},
		},
	}

	res, err := Characterize(context.Background(), CharRequest{
		ObjectID:     "Squarer",
		ImplSymbol:   "square",
		Lang:         "python",
		ArtifactPath: "/legacy/squarer.py",
		CodeHash:     "abcdef0123456789deadbeef",
		Produces:     []string{"result"},
		Environment:  map[string]string{"python": "3.13", "os": "darwin"},
		IntroducedBy: "task-42",
	}, stubSynth(probes), fakeHarness(trace))
	if err != nil {
		t.Fatalf("Characterize: %v", err)
	}

	// Golden lock: 2 cases locked from observation, with EXACT equals.
	if len(res.Lock.Cases) != 2 {
		t.Fatalf("want 2 locked cases, got %d", len(res.Lock.Cases))
	}
	got := map[string]string{}
	for _, c := range res.Lock.Cases {
		if len(c.Expect) != 1 || c.Expect[0].Port != "result" {
			t.Fatalf("case %q: expected one 'result' expectation, got %+v", c.Name, c.Expect)
		}
		got[c.Name] = string(c.Expect[0].Equals)
	}
	if got["happy"] != "4" || got["zero"] != "0" {
		t.Fatalf("transcription wrong: %+v", got)
	}

	// Honest "未覆盖范围": the raising probe is NOT invented into a
	// pass — it is explicitly unlocked (设计文档 Part 10.2).
	if len(res.Lock.Unlocked) != 1 || res.Lock.Unlocked[0] != "raises" {
		t.Fatalf("want ['raises'] unlocked, got %v", res.Lock.Unlocked)
	}

	// Finite evidence: immutable record pins artifact hash + env.
	if res.Finite.CodeHash != "abcdef0123456789deadbeef" {
		t.Fatalf("finite codehash not pinned: %q", res.Finite.CodeHash)
	}
	if res.Finite.Environment["python"] != "3.13" {
		t.Fatalf("finite env snapshot lost: %+v", res.Finite.Environment)
	}
	if res.Finite.Outcomes["raises"] == "" || res.Finite.Outcomes["happy"] != "observed" {
		t.Fatalf("finite outcomes wrong: %+v", res.Finite.Outcomes)
	}

	// Reproducible evidence exists and is tied to the same suite id.
	if res.Reproducible.SuiteID != res.Finite.SuiteID {
		t.Fatalf("repro/finite suite id mismatch: %q vs %q", res.Reproducible.SuiteID, res.Finite.SuiteID)
	}

	// Oracle invariant (设计文档 Part 2.4 禁止): confidence derived
	// from evidence + assumptions, never free-floating.
	if len(res.Oracle.EvidenceRefs) != 2 {
		t.Fatalf("oracle must reference finite+reproducible, got %v", res.Oracle.EvidenceRefs)
	}
	if len(res.Oracle.ConditionalOn) != len(res.Assumptions) || len(res.Assumptions) != 2 {
		t.Fatalf("oracle conditional_on must equal the introduced assumptions: cond=%v assum=%d",
			res.Oracle.ConditionalOn, len(res.Assumptions))
	}
	if res.Oracle.Source != "Characterization" {
		t.Fatalf("oracle source must be Characterization, got %q", res.Oracle.Source)
	}

	// Confidence: coverage = 2/3, NOT collapsed to a scalar; report
	// lists measured + explicitly-unmeasured dimensions.
	if cov := res.Oracle.Confidence.CoverageScore; cov < 0.66 || cov > 0.67 {
		t.Fatalf("coverage should be 2/3, got %.4f", cov)
	}
	rep := strings.Join(res.Oracle.Confidence.Report(), " | ")
	if !strings.Contains(rep, "coverage = 0.667") {
		t.Fatalf("report missing measured coverage: %s", rep)
	}
	if !strings.Contains(rep, "not measured") {
		t.Fatalf("report must honestly mark unmeasured dimensions: %s", rep)
	}
}

// countingHarness returns a different value for the "flaky" probe on
// each successive run, simulating nondeterministic legacy code, while
// the "stable" probe is constant.
func countingHarness() RunHarness {
	n := 0
	return func(_ context.Context, _ HarnessRequest) (*core.RuntimeTrace, error) {
		n++
		return &core.RuntimeTrace{Calls: []core.RuntimeCall{
			{Outputs: map[string]json.RawMessage{"result": raw("7")}},                               // stable
			{Outputs: map[string]json.RawMessage{"result": raw(jsonInt(n))}},                          // flaky: 1,2,3…
		}}, nil
	}
}

func jsonInt(i int) string { return string(rune('0' + i)) }

func TestCharacterize_NondeterminismGuard_RefusesToLockCoinFlips(t *testing.T) {
	probes := []CharProbe{
		{Name: "stable", Call: "IMPL.f(1)"},
		{Name: "flaky", Call: "IMPL.rand()"},
	}
	res, err := Characterize(context.Background(), CharRequest{
		ObjectID:     "Rng",
		ImplSymbol:   "rng",
		Lang:         "python",
		ArtifactPath: "/legacy/rng.py",
		CodeHash:     "cafebabecafebabe",
		Produces:     []string{"result"},
		Stability:    3, // observe 3×; only ports stable across all 3 lock
	}, stubSynth(probes), countingHarness())
	if err != nil {
		t.Fatalf("Characterize: %v", err)
	}

	// Stable probe locks; flaky probe is REFUSED (not frozen as golden).
	if len(res.Lock.Cases) != 1 || res.Lock.Cases[0].Name != "stable" {
		t.Fatalf("only the stable probe may lock, got %+v", res.Lock.Cases)
	}
	if len(res.Lock.Unlocked) != 1 || res.Lock.Unlocked[0] != "flaky" {
		t.Fatalf("flaky probe must be Unlocked, got %v", res.Lock.Unlocked)
	}
	if oc := res.Finite.Outcomes["flaky"]; !strings.Contains(oc, "nondeterministic") {
		t.Fatalf("flaky outcome must explain nondeterminism, got %q", oc)
	}

	// Escalation-candidate assumption introduced + bound into the Oracle
	// (设计文档 Part 3.2/3.3).
	var nd *Assumption
	for i := range res.Assumptions {
		if res.Assumptions[i].ID == "A_nondeterminism_Rng" {
			nd = &res.Assumptions[i]
		}
	}
	if nd == nil {
		t.Fatalf("expected A_nondeterminism_Rng assumption, got %+v", res.Assumptions)
	}
	hasTag := false
	for _, tg := range nd.Tags {
		if tg == "escalation-candidate" {
			hasTag = true
		}
	}
	if !hasTag {
		t.Fatalf("nondeterminism assumption must be tagged escalation-candidate, got %v", nd.Tags)
	}
	condHas := false
	for _, c := range res.Oracle.ConditionalOn {
		if c == "A_nondeterminism_Rng" {
			condHas = true
		}
	}
	if !condHas {
		t.Fatalf("Oracle.ConditionalOn must include the nondeterminism assumption, got %v", res.Oracle.ConditionalOn)
	}
}

func TestCharacterize_NoProbesIsAnError(t *testing.T) {
	_, err := Characterize(context.Background(), CharRequest{
		ObjectID: "X", ArtifactPath: "/x.py",
	}, stubSynth(nil), fakeHarness(nil))
	if err == nil {
		t.Fatal("expected error when synthesizer yields no probes")
	}
}

func TestCharacterize_FewerTracesThanProbesAreHonestlyUnlocked(t *testing.T) {
	probes := []CharProbe{{Name: "a", Call: "IMPL.f()"}, {Name: "b", Call: "IMPL.g()"}}
	trace := &core.RuntimeTrace{Calls: []core.RuntimeCall{
		{Outputs: map[string]json.RawMessage{"r": raw("1")}},
	}}
	res, err := Characterize(context.Background(), CharRequest{
		ObjectID: "P", ArtifactPath: "/p.py", Lang: "python", Produces: []string{"r"},
	}, stubSynth(probes), fakeHarness(trace))
	if err != nil {
		t.Fatalf("Characterize: %v", err)
	}
	if len(res.Lock.Cases) != 1 || res.Lock.Cases[0].Name != "a" {
		t.Fatalf("only probe 'a' should lock, got %+v", res.Lock.Cases)
	}
	if len(res.Lock.Unlocked) != 1 || res.Lock.Unlocked[0] != "b" {
		t.Fatalf("probe 'b' (no trace) must be honestly unlocked, got %v", res.Lock.Unlocked)
	}
}
