// Package characterize implements the brownfield characterization core
// of the Legacy Code Maintenance Agent design (屎山代码维护Agent设计文档
// v1.0). It is the document's prescribed MVP vertical slice — NOT the
// full 11-part architecture (the doc forbids filling [NOT YET DESIGNED]
// parts for the sake of looking complete).
//
// What this package owns, mapped to the design doc:
//
//   - Feathers Characterization Test 5-step loop  → Part 6.6
//   - Method Use Rule (hard rule)                 → Part 6.6
//   - Finite vs Reproducible Evidence             → Part 2.4b
//   - Oracle + conditional_on + ConfidenceVector  → Part 2.4 / 原则 C
//   - Assumption (string + metadata, 11.A 选项3)  → Part 2.1 / 11.A
//
// Paradigm fit with kcpos: kcpos is a greenfield constructor (build an
// object toward a GIVEN spec, verify via synthesize→harness→test→gate).
// Brownfield inverts that — the contract is not given, it must be
// RECOVERED from an untrusted artifact before it is safe to modify.
// This package is therefore a SPEC-PRODUCING front stage: it consumes
// a legacy artifact and emits a characterized, confidence-stratified
// contract that the existing kcpos chain then consumes unchanged. The
// "everything is a TypedValue verified against a contract" paradigm is
// preserved; only the contract's provenance changes (derived, not
// given).
package characterize

import (
	"time"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// AssumptionLayer is the stratified-confidence layer an assumption
// lives at (设计文档 Part 2.1 / Part 3). The agent works at
// LayerBusinessLogic by default; everything at or below the active
// floor is implicitly 1.0 and not questioned until an explicit,
// non-skipping layer escalation (Part 3.1) occurs.
type AssumptionLayer string

const (
	LayerHardware        AssumptionLayer = "Hardware"
	LayerOperatingSystem AssumptionLayer = "OperatingSystem"
	LayerCompiler        AssumptionLayer = "Compiler"
	LayerRuntime         AssumptionLayer = "Runtime"
	LayerLibrary         AssumptionLayer = "Library"
	LayerApplication     AssumptionLayer = "Application"
	LayerBusinessLogic   AssumptionLayer = "BusinessLogic"
)

// AssumptionStatus tracks an assumption's lifecycle (设计文档 Part 2.1).
// Refuted/Superseded assumptions are never deleted — storage keeps the
// full history (Part 2.4b "Evidence 永远不被删除").
type AssumptionStatus string

const (
	AssumptionActive     AssumptionStatus = "Active"
	AssumptionRefuted    AssumptionStatus = "Refuted"
	AssumptionSuperseded AssumptionStatus = "Superseded"
)

// Assumption is a declared premise of the agent's work (设计文档
// Part 2.1). 11.A is resolved here at 选项 3 (the doc's stated
// 当前倾向): a human-readable Statement PLUS machine-parseable
// metadata. We deliberately do NOT attempt a fully typed statement
// schema (选项 2) — the doc itself notes "经验上很多假设不容易 typed"
// and that the schema choice should be made on first contact, which is
// now: the only assumptions the characterization stage introduces are
// environment/version-snapshot premises, which metadata covers without
// a DSL.
type Assumption struct {
	ID        string           `json:"id"`
	Statement string           `json:"statement"`
	Layer     AssumptionLayer  `json:"layer"`
	Status    AssumptionStatus `json:"status"`

	// Machine-parseable metadata (11.A 选项3). Tags classify the
	// assumption (e.g. "env-snapshot", "artifact-hash"); Scope is the
	// code scope it constrains; Symbols are related identifiers.
	Tags    []string `json:"tags,omitempty"`
	Scope   string   `json:"scope,omitempty"`
	Symbols []string `json:"symbols,omitempty"`

	IntroducedBy string    `json:"introducedBy"`
	IntroducedAt time.Time `json:"introducedAt"`

	// DependsOn lists more-basic assumptions this one rests on
	// (设计文档 Part 2.1 depends_on). Refuting a dependency cascades.
	DependsOn []string `json:"dependsOn,omitempty"`
}

// ConfidenceVector is the multi-dimensional confidence of an Oracle
// (设计文档 Part 2.4). HARD design constraint from 原则 C and Part
// 10.2 ("不允许压缩到单一分数"): there is intentionally NO method that
// collapses this to one float. Confidence is reported per-dimension,
// each value conditional on the Oracle's assumption set. Unused
// dimensions stay at their zero value and are simply absent from the
// report — honest about what was and was not measured.
type ConfidenceVector struct {
	// StatisticalScore — from mutation testing etc. (not in MVP scope;
	// stays 0, reported as "not measured").
	StatisticalScore float64 `json:"statisticalScore,omitempty"`
	// CoverageScore — branch/path/probe coverage of the characterization.
	// The MVP's primary measured dimension.
	CoverageScore float64 `json:"coverageScore,omitempty"`
	// IndependenceScore — oracle diversity (how many independent
	// observation channels back the same property).
	IndependenceScore float64 `json:"independenceScore,omitempty"`
	// ErrorPermeability / ErrorExposure — from EPA (设计文档 Part 4.1).
	// Out of MVP scope; the doc places EPA/SBFL in the convergence /
	// regression-triage phase, not the characterization front stage.
	ErrorPermeability float64 `json:"errorPermeability,omitempty"`
	ErrorExposure     float64 `json:"errorExposure,omitempty"`
	// SBFLSuspiciousness / BayesianPosterior — from SBFL/Liblit
	// (设计文档 Part 4.2). Dormant until a char-test regresses; see
	// package doc. Out of MVP scope.
	SBFLSuspiciousness float64 `json:"sbflSuspiciousness,omitempty"`
	BayesianPosterior  float64 `json:"bayesianPosterior,omitempty"`
}

// EvidenceKind distinguishes the two ontological forms of Evidence
// (设计文档 Part 2.4b). The distinction is load-bearing: a property
// backed only by Finite evidence cannot be strengthened without
// reconstructing reproducible conditions; Reproducible evidence can be
// re-activated indefinitely (the form-B flywheel).
type EvidenceKind string

const (
	// EvidenceFinite — an immutable record of something that already
	// happened (a specific run, N observations). Re-interpretable under
	// new assumptions but never mutated.
	EvidenceFinite EvidenceKind = "Finite"
	// EvidenceReproducible — a capability that PRODUCES finite evidence
	// when activated (here: a re-runnable golden test suite). Potentially
	// infinite; this is what makes the characterization a regression lock
	// reusable across every future modification.
	EvidenceReproducible EvidenceKind = "Reproducible"
)

// TestRunRecord is the Finite evidence produced by running probes
// against the untrusted legacy artifact once (设计文档 Part 2.4b
// FiniteEvidence::TestRunRecord). Immutable.
type TestRunRecord struct {
	SuiteID     string            `json:"suiteId"`
	ExecutedAt  time.Time         `json:"executedAt"`
	ArtifactRef string            `json:"artifactRef"` // legacy file path
	CodeHash    string            `json:"codeHash"`    // sha256 of the legacy artifact at observation time
	Environment map[string]string `json:"environment"` // lang, runtime version, os — the conditional_on env snapshot
	Outcomes    map[string]string `json:"outcomes"`    // probe name → "observed" | error text
}

// ExecutableTestSuite is the Reproducible evidence: the rendered golden
// harness, re-runnable against any future (modified) version of the
// artifact (设计文档 Part 2.4b ReproducibleEvidence::ExecutableTestSuite).
// This is the Method-Use-Rule lock — modifying a characterized symbol
// is only safe while this suite still passes.
type ExecutableTestSuite struct {
	SuiteID       string    `json:"suiteId"`
	Lang          string    `json:"lang"`
	HarnessSource string    `json:"harnessSource"` // rendered runnable test code
	ImplSymbol    string    `json:"implSymbol"`
	Manual        string    `json:"manual"` // how to run it (设计文档 OperationManual)
	CreatedAt     time.Time `json:"createdAt"`
}

// Oracle binds a recovered property to the assumptions it is
// conditional on and the evidence that backs it (设计文档 Part 2.4).
// System invariant enforced by the engine: confidence MUST be derived
// from (conditional_on, evidence) — an Oracle with no evidence refs is
// illegal (Part 2.4 禁止 list).
type Oracle struct {
	ID            string           `json:"id"`
	Property      string           `json:"property"` // human-readable recovered behavior statement
	Confidence    ConfidenceVector `json:"confidence"`
	ConditionalOn []string         `json:"conditionalOn"` // Assumption IDs — assumptions change ⇒ recompute
	EvidenceRefs  []string         `json:"evidenceRefs"`  // EvidenceIDs (Finite + Reproducible)
	Source        string           `json:"source"`        // "Characterization" (设计文档 OracleSource::Characterization)
	EvaluatedAt   time.Time        `json:"evaluatedAt"`
}

// CharProbe is one input probe BEFORE observation: the call to make and
// the setup to make it under, with NO trusted expectation. This is
// Feathers step 2 ("写一个明知会失败的断言") — we have the stimulus but
// not yet the locked response. The synthesizer produces these from the
// recovered signature WITHOUT seeing trusted behavior.
type CharProbe struct {
	Name  string         `json:"name"`
	Setup []core.SetupOp `json:"setup,omitempty"`
	Call  string         `json:"call"`
}

// CharLock is the golden characterization suite: probes whose Expect
// clauses have been filled from the legacy artifact's OBSERVED outputs
// (Feathers step 4 "把断言改成实际产生的值"). It carries its own
// provenance — what artifact, what hash, under what assumptions — so a
// later run can detect drift and so the lock's authority is auditable
// (设计文档 Part 10.2 诚实置信度: coverage / 未覆盖 / 假设 must travel
// with the artifact).
type CharLock struct {
	ObjectID   string          `json:"objectId"`
	ImplSymbol string          `json:"implSymbol"`
	Lang       string          `json:"lang"`
	ArtifactRef string         `json:"artifactRef"`
	CodeHash   string          `json:"codeHash"`
	Cases      []core.TestCase `json:"cases"` // golden: Expect filled from observation
	// Unlocked records probes that produced NO observable output (e.g.
	// the call raised, or the port was side-effect-only). Honest
	// surface: these behaviors are NOT characterized — the doc's "未覆盖
	// 范围" must be explicit, not silently dropped.
	Unlocked  []string  `json:"unlocked,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}
