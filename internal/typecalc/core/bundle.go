package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// BundleDir is the v9.0 unified evidence directory. One file per
// object: .kcpos/typecalc/<id>.json. Replaces the v8.x split where
// each object had up to 8 sibling files (.json, .spec.json, .tests.json,
// .accepted.json, .obstacle.json, .waiver.json, .cycles.json + a
// runtime trace at .kcpos/typecalc-runtime/<id>.json).
//
// Why fold:
//   - Atomic reads — gate doesn't open 4-5 files to check one object
//   - Single staleness check via Bundle.SourceHash — no more drift
//     between per-step ImplHash fields (v8.7 pong-03 hit this when
//     trace.implHash and evidence.implHash diverged)
//   - Smaller blast radius for schema changes — extend the section,
//     not the directory layout
//   - Discoverability — one ls lists everything about an object
//
// Greenfield: v9.0 does not migrate v8.x split files. Run kcpos against
// a fresh project tree or delete .kcpos/typecalc-evidence/ first.
const BundleDir = ".kcpos/typecalc"

// CurrentBundleVersion is the schema version tag written into every
// new bundle. Bump on backwards-incompatible changes; readers refuse
// to load older versions (forcing re-derivation rather than silent
// best-effort mapping).
const CurrentBundleVersion = 1

// BundlePath returns the on-disk path for objectID's evidence bundle.
func BundlePath(objectID string) string {
	return filepath.Join(BundleDir, objectID+".json")
}

// EvidenceBundle is the unified per-object record. Sub-sections are
// optional pointers — nil means "that step has not run yet".
//
// Staleness anchors (v9.0.2):
//   - SourceHash: SHA-256 of the WHOLE impl file. Drifts whenever any
//     byte of the file changes. Pre-v9.0.2 this was the only anchor;
//     in single-file-impl projects (HTML deliverables) that meant
//     editing one function invalidated every object's evidence — the
//     "spec-stale storm" pong-03 hit.
//   - SymbolHash: SHA-256 of just THIS object's impl fragment (the
//     body of `function <implSymbol>(...)` extracted from the file).
//     Drifts only when THIS function's body changes. Set by compile /
//     test writers when the impl is HTML and an implSymbol (or
//     defaulted-to-ObjectID symbol) can be located. Falls back to
//     SourceHash when fragment extraction fails (non-HTML impl, symbol
//     not found in source, etc.). Static check prefers SymbolHash for
//     spec-stale / evidence-stale so changes to other objects don't
//     cascade.
type EvidenceBundle struct {
	ObjectID   string    `json:"objectId"`
	Version    int       `json:"version"`
	SourceHash string    `json:"sourceHash,omitempty"`
	SymbolHash string    `json:"symbolHash,omitempty"`
	UpdatedAt  time.Time `json:"updatedAt"`

	Spec         *SpecSection         `json:"spec,omitempty"`
	Tests        *TestsSection        `json:"tests,omitempty"`
	Compile      *CompileSection      `json:"compile,omitempty"`
	Test         *TestSection         `json:"test,omitempty"`
	Accepted     *AcceptedSection     `json:"accepted,omitempty"`
	Cycles       *CyclesSection       `json:"cycles,omitempty"`
	RuntimeTrace *RuntimeTraceSection `json:"runtimeTrace,omitempty"`

	// v9.2 — Obstacle / Waiver sections removed. Old bundles containing
	// these fields will simply lose them on the next re-save (Go JSON
	// drops unknown fields on unmarshal). This is intentional: the
	// concept of "verified by waiver" no longer exists.

	// RuntimeSmoke (v9.1) records the result of booting the deliverable
	// in a real browser (playwright + headless Chromium). Required for
	// HTML deliverables by the gate's [runtime-smoke-required] rule —
	// browser-only side effects (canvas rendering, requestAnimationFrame,
	// DOM events) cannot be verified by the vm.Script harness. Empty for
	// non-HTML deliverables.
	RuntimeSmoke *RuntimeSmokeSection `json:"runtimeSmoke,omitempty"`

	// Characterization (屎山代码维护Agent v1.0 Part 6.6 / 2.4b) records
	// the brownfield characterization lock for an UNTRUSTED legacy
	// artifact: the golden behavior recovered by observation, plus the
	// Finite/Reproducible evidence and conditional confidence backing it.
	// Purely additive: nil on every greenfield object (the normal kcpos
	// chain never sets it), so greenfield gate/readers are unaffected.
	// Present only when an object entered via the characterize front
	// stage. The gate's Method-Use-Rule (legacy-path-only) consults it.
	Characterization *CharacterizationSection `json:"characterization,omitempty"`
}

// RuntimeSmokeSection captures one runtime_smoke invocation. OK reflects
// whether the page loaded cleanly with no uncaught errors and (when a
// canvas is present) rendered any non-black pixels.
type RuntimeSmokeSection struct {
	OK                bool                  `json:"ok"`
	LoadFired         bool                  `json:"loadFired"`
	LoadDurationMs    int                   `json:"loadDurationMs"`
	PageErrors        []RuntimeSmokeError   `json:"pageErrors,omitempty"`
	ConsoleErrors     []RuntimeSmokeError   `json:"consoleErrors,omitempty"`
	RequestFailures   []RuntimeSmokeRequest `json:"requestFailures,omitempty"`
	Canvas            *RuntimeSmokeCanvas   `json:"canvas,omitempty"`
	ScreenshotPath    string                `json:"screenshotPath,omitempty"`
	PlaywrightVersion string                `json:"playwrightVersion,omitempty"`
	Timestamp         time.Time             `json:"timestamp"`
}

// RuntimeSmokeError is one captured page-level or console-level error.
type RuntimeSmokeError struct {
	Message  string `json:"message"`
	Stack    string `json:"stack,omitempty"`
	Source   string `json:"source,omitempty"`
	Location string `json:"location,omitempty"`
}

// RuntimeSmokeRequest is one failed subresource load.
type RuntimeSmokeRequest struct {
	URL     string `json:"url"`
	Failure string `json:"failure"`
}

// RuntimeSmokeCanvas reports whether the page's <canvas> rendered
// anything. Found=true and NonBlackPixels>0 ⇒ OK=true.
type RuntimeSmokeCanvas struct {
	Found          bool `json:"found"`
	Width          int  `json:"width"`
	Height         int  `json:"height"`
	NonBlackPixels int  `json:"nonBlackPixels"`
	OK             bool `json:"ok"`
}

// SpecSection records the auto-generated description from
// typecalc_describe. SpecHash binds it to the impl source at the time
// of generation; if the parent bundle's SourceHash drifts, the spec
// is stale (the static_check stale-spec rule continues to read this).
//
// Contract (2026-05-25, Step 1 of contract landing): structured
// clause-level view of the spec. Description remains as the prose
// summary; Contract is the testable decomposition. Empty for legacy
// bundles — readers must tolerate nil. Populated by typecalc_describe
// in Step 2; consumed by typecalc_synthesize_tests + the
// contract-traceability gate in Steps 3+4.
type SpecSection struct {
	Description string           `json:"description"`
	Contract    []ContractClause `json:"contract,omitempty"`
	SpecHash    string           `json:"specHash,omitempty"` // = parent SourceHash at spec time
	Timestamp   time.Time        `json:"timestamp"`
}

// ContractClause is one atomic, testable expectation derived from the
// spec. Three kinds, mutually exclusive:
//
//   - "example": a concrete (input, output) pair lifted from explicit
//     requirements (e.g. README "calling fib(7) returns 13"). Generates
//     a deterministic test case.
//   - "invariant": a structural property that's testable WITHOUT
//     knowing the full answer (idempotence, sort-after-sort = sort,
//     parse→print→parse round-trip, money conservation, valid state
//     transition). Generates property-style tests.
//   - "characterization": a behavior observed via probe/run_local and
//     LOCKED as expected. Used in both brownfield (lock legacy) and
//     greenfield (lock the design's first accepted behavior). Each
//     characterization clause carries the observation's source so
//     replay/audit can re-derive it.
//
// ID is the trace anchor — TestCase.ContractRefs cite IDs from this
// list; the confirm gate refuses any case whose ContractRefs is empty
// or references an unknown ID (Step 4).
//
// Source is "where this clause came from": "spec:S§N" for requirement
// excerpts, "char:probe_<n>" for characterization, "user:line<n>" for
// human-authored, "equiv:reference" for cross-impl equivalence clauses
// (Step 5). Free-form string; intended for human + tool inspection,
// not parsed by the gate.
//
// Optional marks clauses that don't *require* coverage to pass the
// gate (e.g. an "out-of-scope" example listed for context). Default
// false.
//
// Detail (Step 5 single-source refactor, 2026-05-25): lossless audit
// payload for clauses that carry rich structured data — primarily
// characterization clauses where the full CharResult / equiv-oracle
// battery + probe trace lives here. Opaque to the gate (which reads
// only ID/Kind/Body/Source/Optional); kept so audits can reconstruct
// the agent's evidence path WITHOUT re-running characterize.
// Empty for the typical example/invariant clause whose body is the
// entire data.
type ContractClause struct {
	ID       string          `json:"id"`
	Kind     string          `json:"kind"`
	Body     string          `json:"body"`
	Source   string          `json:"source,omitempty"`
	Optional bool            `json:"optional,omitempty"`
	Detail   json.RawMessage `json:"detail,omitempty"`
}

// TestsSection records the spec-synthesized test cases (and/or raw
// test code for languages without a harness).
//
// ContractHash (2026-05-25 audit B2): hash of spec.Contract at synth
// time. Synth cache check must match BOTH SpecHash AND ContractHash;
// a re-describe with same impl but new clauses bumps ContractHash and
// busts the cache. Empty for legacy bundles (no Contract → empty hash
// → matches empty Contract on cache check).
//
// ContractRefs (2026-05-25): testCode-mode coverage declaration —
// see TestsEvidence.ContractRefs for the full rationale.
//
// LastSynthFailure (2026-05-25, reason-driven repair): when synth
// returns CANNOT_SYNTHESIZE (LLM honestly admits it can't produce
// tests for the given contract), the full reason text is persisted
// here for H_repair_graph's reason parser. Empty when synth succeeded
// or never ran. The LLM's stated reason is the AUTHORITATIVE
// structural diagnosis (vs. derived signals like clause-kind ratios)
// — if the LLM says "needs ./probe to drive stdin", that IS the
// reason the object is wrong-shape. The repair handler matches reason
// tokens to specific graph-mutation proposals.
type TestsSection struct {
	Lang             string     `json:"lang"`
	SpecHash         string     `json:"specHash"` // spec that drove the synthesis
	ContractHash     string     `json:"contractHash,omitempty"`
	Cases            []TestCase `json:"cases,omitempty"`
	TestCode         string     `json:"testCode,omitempty"`
	ContractRefs     []string   `json:"contractRefs,omitempty"`
	LastSynthFailure string     `json:"lastSynthFailure,omitempty"`
	Timestamp        time.Time  `json:"timestamp"`
}

// CompileSection records the most recent typecalc_compile result.
// For HTML / Rust / Java the section may be present with Kind ==
// "insufficient" (the lang has no in-tree compiler) — that's a
// legitimate evidence value, not a failure.
type CompileSection struct {
	Lang      string    `json:"lang"`
	Kind      string    `json:"kind"` // "compile" | "insufficient"
	OK        bool      `json:"ok"`
	Log       string    `json:"log,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// TestSection records the most recent typecalc_test result.
type TestSection struct {
	Lang      string    `json:"lang"`
	Kind      string    `json:"kind"` // "test" | "insufficient"
	OK        bool      `json:"ok"`
	Log       string    `json:"log,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// AcceptedSection records the reasonableness review verdict.
type AcceptedSection struct {
	OK             bool          `json:"ok"`
	StaticIssues   []StaticIssue `json:"staticIssues,omitempty"`
	RuntimeIssues  []StaticIssue `json:"runtimeIssues,omitempty"`
	Reasonableness ReviewVerdict `json:"reasonableness"`
	SpecHash       string        `json:"specHash,omitempty"`
	TestsHash      string        `json:"testsHash,omitempty"`
	Timestamp      time.Time     `json:"timestamp"`
}

// ObstacleSection / WaiverSection / WaiverKindStructural / WaiverKindPragmatic
// — all removed in v9.2. The obstacle/waiver pair was the universal
// verification escape hatch (5/5 Terraria v9.0.6 instances rode
// structural waivers into "confirmed" while 4/5 shipped broken). With
// it gone, the gate is binary pass/fail with no transfer space.

// CyclesSection tracks how many failed reviews this object has
// accumulated since the last reset. Used by CycleCap enforcement.
type CyclesSection struct {
	Count       int       `json:"count"`
	PrevIssues  []string  `json:"prevIssues,omitempty"`
	PrevImplKey string    `json:"prevImplKey,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// RuntimeTraceSection embeds the harness execution trace. Replaces
// the v8.x separate .kcpos/typecalc-runtime/<id>.json file.
type RuntimeTraceSection struct {
	Calls []RuntimeCall `json:"calls"`
}

// ReadBundle loads the per-object bundle. Returns (nil, false) for
// missing/malformed files. Schema version mismatch also returns
// (nil, false) — callers should treat it as "no usable evidence" and
// re-derive rather than try to handle older shapes.
func ReadBundle(objectID string) (*EvidenceBundle, bool) {
	if objectID == "" {
		return nil, false
	}
	raw, err := os.ReadFile(BundlePath(objectID))
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	var b EvidenceBundle
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, false
	}
	if b.Version != CurrentBundleVersion {
		return nil, false
	}
	return &b, true
}

// LoadOrInitBundle returns the existing bundle or a freshly initialized
// one. Use this in writers that produce one section without disturbing
// others — pattern: load → mutate one section → save.
func LoadOrInitBundle(objectID string) *EvidenceBundle {
	if b, ok := ReadBundle(objectID); ok {
		return b
	}
	return &EvidenceBundle{
		ObjectID:  objectID,
		Version:   CurrentBundleVersion,
		UpdatedAt: time.Now().UTC(),
	}
}

// SaveBundle writes the bundle to disk atomically (write to tmp, rename).
// Bumps UpdatedAt automatically.
func SaveBundle(b *EvidenceBundle) error {
	if b == nil || b.ObjectID == "" {
		return nil
	}
	if b.Version == 0 {
		b.Version = CurrentBundleVersion
	}
	b.UpdatedAt = time.Now().UTC()
	if err := os.MkdirAll(BundleDir, 0o755); err != nil {
		return fmt.Errorf("mkdir bundle dir: %w", err)
	}
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	target := BundlePath(b.ObjectID)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// SetSourceHash atomically updates the bundle's SourceHash. Used when
// the impl content changes — callers may also want to clear stale
// downstream sections (Tests, Test, Accepted) but that decision is
// per-step; this helper only carries the hash forward.
func SetSourceHash(objectID, hash string) error {
	b := LoadOrInitBundle(objectID)
	b.SourceHash = hash
	return SaveBundle(b)
}
