// Package sbfl implements the Spectrum-Based Fault Localization half of
// the design document's black-box analysis engine (屎山代码维护Agent
// 设计文档 v1.0 Part 4.2 / 4.4). The doc hands down exact formulas
// (Ochiai, DStar) and a Liblit-style Bayesian complement — these are
// fully specified, not [NOT YET DESIGNED]; only EPA's fault-injection
// budget (Part 4.1) is the heavier sibling, left for the next pass.
//
// Role in the system: SBFL is the convergence-stage regression-triage
// tool (设计文档 Part 4.2.3, and ITERATION-v1.1 I3 — the missing
// drift→regression bridge). When a characterization golden lock is
// re-run after a modification and some cases regress, the per-case
// pass/fail + per-case executed-element spectrum feeds SBFL to rank
// WHICH code element most likely broke the locked behavior. SBFL is
// backward reasoning (from observed failures to suspected cause), the
// dual of EPA's forward reasoning (设计文档 Part 4.2.4 / 6.7).
//
// SBFL is DORMANT in the front stage (ITERATION-v1.1 I3): with no
// pass/fail partition there is nothing to localize. This package is
// therefore a pure algorithm over an explicitly supplied Spectrum; the
// coverage-collection adapter (instrumenting a re-run of the lock) is
// the next layer, not a deferral.
package sbfl

import (
	"math"
	"sort"
	"time"

	"github.com/creator915/Koncept_OS/internal/legacy/characterize"
)

// TestRun is one execution: did it pass, and which code elements
// (statements / branches / functions — caller's granularity) did it
// cover. Element ids are opaque strings.
type TestRun struct {
	Name     string
	Passed   bool
	Executed []string
}

// Spectrum is the coverage spectrum across a suite (设计文档 Part 2.4b
// SbflTraceRecord: passing_traces / failing_traces / coverage_spectrum).
type Spectrum struct {
	Runs []TestRun
}

// Score is the per-element suspiciousness. Multiple metrics travel
// together and are NOT collapsed (设计文档 原则 C / Part 10.2): Ochiai
// and DStar are co-reported; the caller decides. ef/ep are surfaced for
// auditability (Part 10.4).
type Score struct {
	Element  string
	Ochiai   float64
	DStar    float64
	Bayesian float64 // Liblit-style: P(exec|fail) - P(exec|pass)
	Ef, Ep   int     // failing / passing runs that executed this element
}

// counts tallies the four spectrum cells per element plus suite totals.
type counts struct {
	ef, ep int
}

// Rank computes Ochiai + DStar + Bayesian suspiciousness for every
// element and returns them sorted most-suspicious-first. dstarStar is
// the DStar exponent hyperparameter (设计文档 Part 4.2.2: "常用 2 或
// 3"); pass 0 to default to 2.
//
// Formulas verbatim from 设计文档 Part 4.2.2:
//
//	Ochiai(e) = e_f / sqrt((e_f + e_p) * (e_f + n_f))
//	DStar(e)  = (e_f)^*  / (e_p + n_f - e_f)
//
// where n_f = total failing runs. A 0 denominator yields 0 (an element
// no failing run touches has no suspiciousness — honest, not NaN).
func Rank(s Spectrum, dstarStar float64) []Score {
	if dstarStar == 0 {
		dstarStar = 2
	}
	nf, np := 0, 0
	per := map[string]*counts{}
	for _, r := range s.Runs {
		if r.Passed {
			np++
		} else {
			nf++
		}
		seen := map[string]bool{}
		for _, e := range r.Executed {
			if seen[e] {
				continue
			}
			seen[e] = true
			c := per[e]
			if c == nil {
				c = &counts{}
				per[e] = c
			}
			if r.Passed {
				c.ep++
			} else {
				c.ef++
			}
		}
	}

	out := make([]Score, 0, len(per))
	for elem, c := range per {
		sc := Score{Element: elem, Ef: c.ef, Ep: c.ep}

		// Ochiai.
		denOch := math.Sqrt(float64(c.ef+c.ep) * float64(c.ef+nf))
		if denOch > 0 {
			sc.Ochiai = float64(c.ef) / denOch
		}

		// DStar (D* = e_f^star / (e_p + n_f - e_f)).
		denD := float64(c.ep + nf - c.ef)
		if denD > 0 {
			sc.DStar = math.Pow(float64(c.ef), dstarStar) / denD
		} else if c.ef > 0 {
			// All-failing, none-passing, every fail touches it: maximal
			// suspicion. +Inf is not auditable; report a large finite.
			sc.DStar = math.Pow(float64(c.ef), dstarStar)
		}

		// Liblit-style Bayesian complement (设计文档 Part 4.2.1):
		// P(executed | fail) - P(executed | success).
		pExecFail, pExecPass := 0.0, 0.0
		if nf > 0 {
			pExecFail = float64(c.ef) / float64(nf)
		}
		if np > 0 {
			pExecPass = float64(c.ep) / float64(np)
		}
		sc.Bayesian = pExecFail - pExecPass

		out = append(out, sc)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Ochiai != out[j].Ochiai {
			return out[i].Ochiai > out[j].Ochiai
		}
		if out[i].DStar != out[j].DStar {
			return out[i].DStar > out[j].DStar
		}
		return out[i].Element < out[j].Element // stable
	})
	return out
}

// OracleFromScore lifts an SBFL Score into a characterize.Oracle
// (设计文档 Part 4.2.4: SBFL 产出进入 Oracle). The suspiciousness lands
// in ConfidenceVector.SBFLSuspiciousness — a SEPARATE dimension, never
// merged into a scalar (原则 C). conditionalOn carries the current
// branch's active assumption ids (设计文档 Part 4.4 Step 4: SBFL
// Oracles are conditional on the trace set + runtime assumptions). The
// suite/spectrum that produced it is the evidence ref.
func OracleFromScore(sc Score, evidenceRef string, conditionalOn []string) characterize.Oracle {
	return characterize.Oracle{
		ID:       "oracle-sbfl-" + sc.Element,
		Property: "code element " + sc.Element + " is a suspected source of the observed regression (backward reasoning; SBFL has no proof — it ranks suspicion)",
		Confidence: characterize.ConfidenceVector{
			SBFLSuspiciousness: sc.Ochiai,
			BayesianPosterior:  clamp01(sc.Bayesian),
		},
		ConditionalOn: conditionalOn,
		EvidenceRefs:  []string{evidenceRef},
		Source:        "SBFL", // 设计文档 OracleSource::SBFL
		EvaluatedAt:   time.Now().UTC(),
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
