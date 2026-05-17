// Package typing implements white-box progressive typing — the
// formal-lite half of the design document (屎山代码维护Agent设计文档
// v1.0 Part 5). White box only expresses NECESSARY properties: a
// type-checked invariant is 100% reliable, but ONLY conditional on the
// typing assumptions it introduced (Part 5.4 / 原则 C). Get the typing
// wrong → an assumption is refuted → the Oracle dies and the branch
// forks. That conditional-on discipline is the whole point; without it
// "confidence 1.0" would be a lie.
//
// Complement, not replacement (Part 5.6): typing catches a different
// bug class than the black-box engine (sbfl/epa) — they STACK. Part
// 5.3 honestly lists what typing CANNOT reach (cross-object invariants,
// temporal ordering, cross-process, statistical/SLO, subtle races,
// info-flow); this package never claims those.
//
// Layering: this is the pure proposal/oracle core over identified
// opportunities. The AST scanner that finds the 8 categories in real
// source is the adapter layer (same split as sbfl/epa), not a deferral.
package typing

import (
	"fmt"
	"time"

	"github.com/creator915/Koncept_OS/internal/legacy/characterize"
)

// InvariantCategory — the 8 typing-opportunity classes from 设计文档
// Part 5.2. Detectors are added per category by the scanner adapter.
type InvariantCategory string

const (
	CatRefinement   InvariantCategory = "Refinement"   // NonNegative/Positive/Bounded/Percent/units
	CatTypeState    InvariantCategory = "TypeState"    // File Closed→Open, Conn Unconnected→Authed
	CatNewtype      InvariantCategory = "Newtype"      // UserId vs OrderId, Raw vs Sanitized
	CatNullability  InvariantCategory = "Nullability"  // Optional[T] forcing None handling
	CatErrorHandling InvariantCategory = "ErrorHandling" // Result[T,E] forcing failure handling
	CatImmutability InvariantCategory = "Immutability" // readonly/Final/Frozen
	CatThreadAffinity InvariantCategory = "ThreadAffinity" // Send/Sync/@ThreadSafe
	CatLinear       InvariantCategory = "Linear"       // Token / unique_ptr lifetime
)

// Opportunity is one identified place a type could pin an invariant
// (设computed by the scanner adapter; consumed pure here). Assumptions
// is what TYPING this introduces — each becomes an Active assumption
// the resulting Oracle is conditional on (设计文档 Part 5.1).
type Opportunity struct {
	ID         string
	Category   InvariantCategory
	Scope      string   // code location, e.g. "transfer(amount)"
	Invariant  string    // e.g. "amount > 0 at every call site"
	Introduces []string  // human-readable assumption statements
	// Cost/benefit signals for the Part 5.5 backlog ranking.
	CallerChurn int // estimated call sites needing change
	BugClasses  int // bug classes the invariant eliminates
}

// Proposal is a candidate written to the backlog — NOT auto-applied
// (设计文档 Part 5.5: 不直接改, 作为候选, 客户审阅). Score follows the
// 11.G 当前倾向 placeholder: stake×benefit / (range+assumptions).
type Proposal struct {
	Opportunity Opportunity
	Assumptions []characterize.Assumption
	Score       float64
}

// Propose turns an opportunity into a backlog proposal: it
// materializes the introduced typing assumptions (Active, Application
// layer — typing is an application-level commitment) and scores it.
// stake is the caller-declared stake (Part 11.I); 0 ⇒ treat as 1
// (neutral) so a missing stake never zeroes the score.
func Propose(o Opportunity, stake float64, introducedBy string) Proposal {
	if introducedBy == "" {
		introducedBy = "typing-stage"
	}
	now := time.Now().UTC()
	as := make([]characterize.Assumption, 0, len(o.Introduces))
	for i, s := range o.Introduces {
		as = append(as, characterize.Assumption{
			ID:           fmt.Sprintf("A_typing_%s_%d", o.ID, i+1),
			Statement:    s,
			Layer:        characterize.LayerApplication,
			Status:       characterize.AssumptionActive,
			Tags:         []string{"typing", string(o.Category)},
			Scope:        o.Scope,
			IntroducedBy: introducedBy,
			IntroducedAt: now,
		})
	}
	if stake <= 0 {
		stake = 1
	}
	// 11.G placeholder: stake × benefit / (callerChurn + #assumptions).
	denom := float64(o.CallerChurn + len(o.Introduces))
	if denom <= 0 {
		denom = 1
	}
	score := stake * float64(o.BugClasses) / denom
	return Proposal{Opportunity: o, Assumptions: as, Score: score}
}

// OracleFromProposal lifts an APPLIED typing into a characterize.Oracle
// (设计文档 Part 5.4). The compiler/type-checker guards the invariant,
// so statistical_score and independence_score are 1.0 — but the Oracle
// is conditional_on the typing assumptions PLUS A_compiler (assume the
// type checker is correct). Refute any ⇒ Oracle invalid ⇒ fork branch.
// The 1.0 confidence is honest ONLY because it travels with that
// conditional set (原则 C / Part 5.4 注).
func OracleFromProposal(p Proposal, compilerAssumptionID, evidenceRef string) characterize.Oracle {
	cond := make([]string, 0, len(p.Assumptions)+1)
	for _, a := range p.Assumptions {
		cond = append(cond, a.ID)
	}
	if compilerAssumptionID == "" {
		compilerAssumptionID = "A_compiler"
	}
	cond = append(cond, compilerAssumptionID)
	return characterize.Oracle{
		ID:       "oracle-type-" + p.Opportunity.ID,
		Property: p.Opportunity.Invariant + " — guaranteed at the type layer (compile-time), conditional on the typing assumptions",
		Confidence: characterize.ConfidenceVector{
			StatisticalScore:  1.0, // compiler-enforced
			IndependenceScore: 1.0, // independent of runtime tests
		},
		ConditionalOn: cond,
		EvidenceRefs:  []string{evidenceRef},
		Source:        "Type", // 设计文档 OracleSource::Type
		EvaluatedAt:   time.Now().UTC(),
	}
}

// UnreachableByTyping is Part 5.3 verbatim — the honest list of what
// white-box typing does NOT attempt. Surfaced so a caller never
// mistakes typing silence for a guarantee (the doc's 诚实 ethos).
func UnreachableByTyping() []string {
	return []string{
		"cross-object invariants (e.g. order.total == sum(items.price))",
		"temporal/ordering invariants (e.g. no read after close) in most languages",
		"cross-process / cross-service invariants",
		"statistical / performance (SLO-class) invariants",
		"some subtle concurrency races",
		"high-level security semantics (information-flow non-leakage)",
	}
}
