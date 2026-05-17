package characterize

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// ProbeSpec is what the synthesizer needs to invent input probes for an
// untrusted artifact. Note the asymmetry vs greenfield synthesize: we
// pass the RECOVERED signature/description (white-box read of the code,
// 设计文档 原则 D), and we explicitly do NOT trust any Expect the
// synthesizer emits — only its Call/Setup stimuli are used. The lock's
// expectations come exclusively from observation (Feathers step 4).
type ProbeSpec struct {
	ObjectID    string
	ImplSymbol  string
	Lang        string
	Signature   string
	Description string
	Consumes    []string
	Produces    []string
}

// SynthProbes is the seam over test-input synthesis. The adapter in
// app/services binds this to the real synthesize package; unit tests
// bind a deterministic stub. It returns stimuli only.
type SynthProbes func(ctx context.Context, spec ProbeSpec) ([]CharProbe, error)

// HarnessRequest asks the runner to render `probes` into a runnable
// suite bound to the UNTRUSTED legacy artifact at ArtifactPath and run
// it, returning the observed runtime trace. This is Feathers steps 1+3:
// "把代码放进 test harness" then "让失败信息告诉你真实行为".
type HarnessRequest struct {
	ObjectID     string
	ImplSymbol   string
	Lang         string
	ArtifactPath string
	Produces     []string
	// PortObservation tells the harness HOW to read each produced port
	// (e.g. "return" → the call's return value). Without it the harness
	// defaults every port to "global", so a pure return-valued legacy
	// function would characterize as zero-locked. Threaded identically
	// to the greenfield chain (it needs the same map).
	PortObservation map[string]string
	Probes          []CharProbe
}

// RunHarness is the seam over render+execute. Returns the trace of what
// the legacy artifact actually did. A nil trace with nil error means
// "ran but observed nothing" (caller treats every probe as unlocked).
type RunHarness func(ctx context.Context, req HarnessRequest) (*core.RuntimeTrace, error)

// CharResult is the full output of one characterization pass: the
// golden lock plus the Finite/Reproducible evidence and the Oracle
// binding them under explicit assumptions (设计文档 Part 2.4 / 2.4b).
type CharResult struct {
	Lock        CharLock            `json:"lock"`
	Finite      TestRunRecord       `json:"finite"`      // immutable record of this observation
	Reproducible ExecutableTestSuite `json:"reproducible"` // re-runnable regression lock
	Assumptions []Assumption        `json:"assumptions"` // conditional_on of the Oracle
	Oracle      Oracle              `json:"oracle"`
}

// CharRequest is the engine's input. Environment is passed in (not read
// via os/exec) to keep the engine pure and unit-testable; the real
// adapter fills actual runtime/os versions.
type CharRequest struct {
	ObjectID     string
	ImplSymbol   string
	Lang         string
	ArtifactPath string
	CodeHash     string            // sha256 of the artifact's current bytes
	Signature    string            // recovered (white-box) signature
	Description  string            // recovered behavior catalogue
	Consumes     []string
	Produces     []string
	// PortObservation maps each produced port to its runtime extractor
	// ("return", "return.<path>", "global", …). Passed through to the
	// harness so return-valued legacy functions are observable.
	PortObservation map[string]string
	Environment     map[string]string // env-snapshot the observation is conditional on
	IntroducedBy    string            // task id, for assumption provenance

	// Stability is how many times each probe is observed against the
	// artifact. >1 enables the nondeterminism guard (设计文档 Part 3.2
	// trigger CharacterizationUnstableAcrossRuns; closes ITERATION-v1.1
	// I5): a port whose observed value differs across runs is NOT
	// locked (locking one coin-flip as golden would be a confidently
	// wrong oracle); it is reported Unlocked(nondeterministic) and an
	// escalation-candidate assumption is introduced. 0 or 1 = single
	// observation (back-compatible default).
	Stability int
}

// Characterize runs the Feathers Characterization Test 5-step loop
// (设计文档 Part 6.6) over an untrusted legacy artifact:
//
//	1. 把代码放进 test harness         → RunHarness binds artifact as IMPL
//	2. 写明知会失败的断言               → SynthProbes (stimuli only, no trusted Expect)
//	3. 失败信息告诉你真实行为           → observed RuntimeTrace
//	4. 把断言改成实际产生的值           → transcribe Outputs → golden Expect (deterministic, no LLM)
//	5. 重复                            → over the whole probe set
//
// It returns a CharResult whose Oracle's confidence is DERIVED from
// (assumptions, evidence) — never asserted by an LLM (设计文档 Part 2.4
// 禁止 list, 原则 C).
func Characterize(ctx context.Context, in CharRequest, synth SynthProbes, run RunHarness) (*CharResult, error) {
	if in.ObjectID == "" || in.ArtifactPath == "" {
		return nil, fmt.Errorf("characterize: ObjectID and ArtifactPath are required")
	}
	sym := in.ImplSymbol
	if sym == "" {
		sym = in.ObjectID
	}

	// Step 2 — synthesize stimuli (no trusted expectations).
	probes, err := synth(ctx, ProbeSpec{
		ObjectID:    in.ObjectID,
		ImplSymbol:  sym,
		Lang:        in.Lang,
		Signature:   in.Signature,
		Description: in.Description,
		Consumes:    in.Consumes,
		Produces:    in.Produces,
	})
	if err != nil {
		return nil, fmt.Errorf("characterize: synth probes: %w", err)
	}
	if len(probes) == 0 {
		return nil, fmt.Errorf("characterize: synthesizer produced no probes for %s", in.ObjectID)
	}

	// Steps 1+3 — run the probes against the UNTRUSTED artifact and
	// observe what it actually does. With Stability>1 we observe N times
	// and only lock probes whose output is stable across ALL runs
	// (设计文档 Part 3.2 trigger CharacterizationUnstableAcrossRuns;
	// closes ITERATION-v1.1 I5 — locking one coin-flip as golden would
	// be a confidently wrong oracle). N=1 is the back-compatible single
	// observation: the stability comparison can never trip.
	n := in.Stability
	if n < 1 {
		n = 1
	}
	runs := make([][]core.RuntimeCall, 0, n)
	for k := 0; k < n; k++ {
		trace, err := run(ctx, HarnessRequest{
			ObjectID:        in.ObjectID,
			ImplSymbol:      sym,
			Lang:            in.Lang,
			ArtifactPath:    in.ArtifactPath,
			Produces:        in.Produces,
			PortObservation: in.PortObservation,
			Probes:          probes,
		})
		if err != nil {
			return nil, fmt.Errorf("characterize: run harness (observation %d/%d): %w", k+1, n, err)
		}
		var calls []core.RuntimeCall
		if trace != nil {
			calls = trace.Calls
		}
		runs = append(runs, calls)
	}

	// Step 4 — transcribe observed outputs into golden expectations.
	// Match probe i ↔ run[k].Calls[i] by order (one appendTrace per
	// case). A probe is locked only if EVERY run produced an observable
	// output AND those outputs are identical across runs.
	lock := CharLock{
		ObjectID:    in.ObjectID,
		ImplSymbol:  sym,
		Lang:        in.Lang,
		ArtifactRef: in.ArtifactPath,
		CodeHash:    in.CodeHash,
		CreatedAt:   time.Now().UTC(),
	}
	outcomes := map[string]string{}
	nondeterministic := false
	for i, p := range probes {
		var first []core.Expectation
		keys := make([]string, 0, n)
		observableEverywhere := true
		for k := 0; k < n; k++ {
			calls := runs[k]
			if i >= len(calls) {
				observableEverywhere = false
				break
			}
			exp := transcribe(calls[i].Outputs, in.Produces)
			if len(exp) == 0 {
				observableEverywhere = false
				break
			}
			if first == nil {
				first = exp
			}
			keys = append(keys, expectKey(exp))
		}
		if !observableEverywhere {
			// Honest "未覆盖范围" (设计文档 Part 10.2): nothing observable
			// — not characterized, and we don't invent a value.
			lock.Unlocked = append(lock.Unlocked, p.Name)
			outcomes[p.Name] = "no-observable-output (raised, void, side-effect-only, or not reached)"
			continue
		}
		if !allEqual(keys) {
			// Coin-flip behavior: refuse to freeze one outcome as golden.
			lock.Unlocked = append(lock.Unlocked, p.Name)
			outcomes[p.Name] = fmt.Sprintf("nondeterministic (output differs across %d runs) — NOT locked", n)
			nondeterministic = true
			continue
		}
		lock.Cases = append(lock.Cases, core.TestCase{
			Name:   p.Name,
			Setup:  p.Setup,
			Call:   p.Call,
			Expect: first,
		})
		if n > 1 {
			outcomes[p.Name] = fmt.Sprintf("observed (stable across %d runs)", n)
		} else {
			outcomes[p.Name] = "observed"
		}
	}

	now := time.Now().UTC()
	short := shortHash(in.CodeHash)
	suiteID := "chsuite-" + in.ObjectID + "-" + short

	finite := TestRunRecord{
		SuiteID:     suiteID,
		ExecutedAt:  now,
		ArtifactRef: in.ArtifactPath,
		CodeHash:    in.CodeHash,
		Environment: cloneEnv(in.Environment),
		Outcomes:    outcomes,
	}

	// Reproducible evidence — the golden suite as a re-runnable lock.
	// HarnessSource is filled by the adapter after it renders the lock
	// (engine stays pure; rendering needs the harness package). We
	// record everything else needed to re-render deterministically.
	reproducible := ExecutableTestSuite{
		SuiteID:    suiteID,
		Lang:       in.Lang,
		ImplSymbol: sym,
		Manual: fmt.Sprintf(
			"Re-render %d golden cases against the current %s artifact and run; "+
				"any divergence from the locked Expect is a behavior regression "+
				"(Feathers Method Use Rule, 设计文档 Part 6.6).",
			len(lock.Cases), in.Lang),
		CreatedAt: now,
	}

	assumptions := buildAssumptions(in, now)
	if nondeterministic {
		by := in.IntroducedBy
		if by == "" {
			by = "characterize-stage"
		}
		// 设计文档 Part 3.2 trigger CharacterizationUnstableAcrossRuns +
		// Part 3.3: escalation is a customer-facing event — MVP records
		// the candidate (does NOT auto-fork a branch). The unstable
		// ports are already Unlocked above; this makes WHY explicit and
		// auditable rather than silently dropping them.
		assumptions = append(assumptions, Assumption{
			ID:           "A_nondeterminism_" + in.ObjectID,
			Statement:    "One or more probes observed UNSTABLE output across repeated runs; those ports are deliberately NOT locked. Escalation candidate: a stable characterization may require escalating above BusinessLogic (Runtime/OS — nondeterminism source). Surfaced to the customer per Part 3.3; not auto-forked in MVP.",
			Layer:        LayerBusinessLogic,
			Status:       AssumptionActive,
			Tags:         []string{"nondeterminism", "escalation-candidate", "characterization"},
			Scope:        in.ArtifactPath,
			Symbols:      []string{sym},
			IntroducedBy: by,
			IntroducedAt: now,
		})
	}
	condIDs := make([]string, 0, len(assumptions))
	for _, a := range assumptions {
		condIDs = append(condIDs, a.ID)
	}

	finiteID := "ev-finite-" + suiteID
	reproID := "ev-repro-" + suiteID

	oracle := Oracle{
		ID: "oracle-char-" + in.ObjectID + "-" + short,
		Property: fmt.Sprintf(
			"%s currently exhibits the behavior locked by %d characterization cases "+
				"(%d probes unlocked) — this is the RECOVERED contract, not a judgment "+
				"of correctness (Feathers: char tests have no moral authority).",
			sym, len(lock.Cases), len(lock.Unlocked)),
		Confidence:    deriveConfidence(len(lock.Cases), len(probes)),
		ConditionalOn: condIDs,
		EvidenceRefs:  []string{finiteID, reproID},
		Source:        "Characterization", // 设计文档 OracleSource::Characterization
		EvaluatedAt:   now,
	}

	return &CharResult{
		Lock:         lock,
		Finite:       finite,
		Reproducible: reproducible,
		Assumptions:  assumptions,
		Oracle:       oracle,
	}, nil
}

// transcribe converts one observed Outputs map into golden equals
// expectations — the deterministic core of Feathers step 4. Ports are
// emitted in a stable order (sorted) so the lock is reproducible. Only
// ports the agent declared in `produces` are locked when produces is
// non-empty; otherwise every observed port is locked.
func transcribe(obs map[string]json.RawMessage, produces []string) []core.Expectation {
	if len(obs) == 0 {
		return nil
	}
	want := map[string]bool{}
	for _, p := range produces {
		want[p] = true
	}
	keys := make([]string, 0, len(obs))
	for k := range obs {
		if len(want) > 0 && !want[k] {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]core.Expectation, 0, len(keys))
	for _, k := range keys {
		v := obs[k]
		if len(v) == 0 {
			continue
		}
		raw := append(json.RawMessage(nil), v...)
		out = append(out, core.Expectation{Port: k, Equals: raw})
	}
	return out
}

// expectKey is a stable string identity for a transcribed expectation
// set, used to compare observations across runs. transcribe already
// sorts ports, so JSON marshalling is order-stable.
func expectKey(exp []core.Expectation) string {
	b, _ := json.Marshal(exp)
	return string(b)
}

// allEqual reports whether every key is identical (and the slice is
// non-empty). Used by the nondeterminism guard.
func allEqual(keys []string) bool {
	if len(keys) == 0 {
		return false
	}
	for _, k := range keys[1:] {
		if k != keys[0] {
			return false
		}
	}
	return true
}

// buildAssumptions returns the assumption set the observation is
// conditional on (设计文档 原则 C: 所有可信度都是在某假设集合下的).
// MVP introduces exactly the premises the doc says the characterization
// stage legitimately rests on: an env snapshot and an artifact-version
// pin. Both at LayerBusinessLogic — they are not yet questioned (no
// layer escalation in the MVP front stage).
func buildAssumptions(in CharRequest, now time.Time) []Assumption {
	envParts := make([]string, 0, len(in.Environment))
	keys := make([]string, 0, len(in.Environment))
	for k := range in.Environment {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		envParts = append(envParts, k+"="+in.Environment[k])
	}
	envStr := "(none recorded)"
	if len(envParts) > 0 {
		envStr = fmt.Sprintf("%v", envParts)
	}
	by := in.IntroducedBy
	if by == "" {
		by = "characterize-stage"
	}
	return []Assumption{
		{
			ID:           "A_env_snapshot_" + in.ObjectID,
			Statement:    "The observed behavior holds under the recorded environment snapshot: " + envStr,
			Layer:        LayerBusinessLogic,
			Status:       AssumptionActive,
			Tags:         []string{"env-snapshot", "characterization"},
			Scope:        in.ArtifactPath,
			IntroducedBy: by,
			IntroducedAt: now,
		},
		{
			ID:           "A_artifact_hash_" + in.ObjectID,
			Statement:    "The lock characterizes the artifact at content hash " + in.CodeHash + "; a different hash invalidates this Oracle until re-characterized.",
			Layer:        LayerBusinessLogic,
			Status:       AssumptionActive,
			Tags:         []string{"artifact-hash", "characterization"},
			Scope:        in.ArtifactPath,
			Symbols:      []string{in.ImplSymbol},
			IntroducedBy: by,
			IntroducedAt: now,
		},
	}
}

// deriveConfidence builds the ConfidenceVector from measured quantities
// only (设计文档 原则 C / Part 2.4 禁止: confidence 数值与 evidence 脱钩
// 是非法的). The ONLY dimension the MVP front stage can honestly measure
// is probe coverage of the recovered surface. IndependenceScore stays 0
// (single observation channel = one harness) and is reported as "not
// measured" rather than fabricated. No scalar collapse (Part 10.2).
func deriveConfidence(locked, totalProbes int) ConfidenceVector {
	cov := 0.0
	if totalProbes > 0 {
		cov = float64(locked) / float64(totalProbes)
	}
	return ConfidenceVector{CoverageScore: cov}
}

// Report renders the confidence vector per-dimension, listing ONLY
// measured dimensions (设计文档 Part 10.2: 覆盖范围 / 未覆盖范围 must be
// explicit; never compress to a single score). Unmeasured dimensions
// are stated as such, not hidden and not zero-washed into an average.
func (c ConfidenceVector) Report() []string {
	var r []string
	add := func(name string, v float64, measured bool) {
		if measured {
			r = append(r, fmt.Sprintf("%s = %.3f", name, v))
		} else {
			r = append(r, name+" = (not measured in MVP front stage)")
		}
	}
	add("coverage", c.CoverageScore, true)
	add("independence", c.IndependenceScore, c.IndependenceScore > 0)
	add("statistical(mutation)", c.StatisticalScore, c.StatisticalScore > 0)
	add("epa.permeability", c.ErrorPermeability, c.ErrorPermeability > 0)
	add("sbfl.suspiciousness", c.SBFLSuspiciousness, c.SBFLSuspiciousness > 0)
	return r
}

func cloneEnv(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func shortHash(h string) string {
	if len(h) >= 12 {
		return h[:12]
	}
	if h == "" {
		return "nohash"
	}
	return h
}
