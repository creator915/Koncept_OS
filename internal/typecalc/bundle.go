package typecalc

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
	Obstacle     *ObstacleSection     `json:"obstacle,omitempty"`
	Waiver       *WaiverSection       `json:"waiver,omitempty"`
	Cycles       *CyclesSection       `json:"cycles,omitempty"`
	RuntimeTrace *RuntimeTraceSection `json:"runtimeTrace,omitempty"`
}

// SpecSection records the auto-generated description from
// typecalc_describe. SpecHash binds it to the impl source at the time
// of generation; if the parent bundle's SourceHash drifts, the spec
// is stale (the static_check stale-spec rule continues to read this).
type SpecSection struct {
	Description string    `json:"description"`
	SpecHash    string    `json:"specHash,omitempty"` // = parent SourceHash at spec time
	Timestamp   time.Time `json:"timestamp"`
}

// TestsSection records the spec-synthesized test cases (and/or raw
// test code for languages without a harness).
type TestsSection struct {
	Lang      string     `json:"lang"`
	SpecHash  string     `json:"specHash"` // spec that drove the synthesis
	Cases     []TestCase `json:"cases,omitempty"`
	TestCode  string     `json:"testCode,omitempty"`
	Timestamp time.Time  `json:"timestamp"`
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

// ObstacleSection captures the agent's explicit "I cannot make this
// object converge automatically" signal. Must be paired with a
// WaiverSection for the gate to accept.
type ObstacleSection struct {
	Reason              string    `json:"reason"`
	CyclesWhenObstacled int       `json:"cyclesWhenObstacled"`
	Timestamp           time.Time `json:"timestamp"`
}

// WaiverSection captures the human-equivalent decision to accept the
// object despite obstacle (or Insufficient evidence).
//
// Kind discriminates waivers that don't represent a verification gap
// vs those that do. v9.0.1 (pong-03 R1-R4 deadlock) showed that
// HTML-single-file projects accumulate legitimate "harness can't reach
// this code in Node" waivers — when most of the graph is in that
// bucket, the waiver-flood gate would block a perfectly correct
// project. Two values:
//
//   - "structural" — kcpos cannot mechanically verify this in principle
//     (DOM I/O, Canvas, side-effect-only, language without runner).
//     Doesn't count toward waiver-flood; gate accepts unconditionally.
//   - "pragmatic"  — the underlying issue is fixable but the agent
//     chose to bypass for now (test harness binding, time pressure,
//     etc.). Counts toward waiver-flood. This is the v8.5 default for
//     legacy waivers without explicit kind.
//
// Empty Kind is treated as "pragmatic" for backwards-compat with v9.0
// waivers written before this field existed.
type WaiverSection struct {
	Reason    string    `json:"reason"`
	Verifier  string    `json:"verifier,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// Waiver kinds recognised by the v9.0.1 gate.
const (
	WaiverKindStructural = "structural"
	WaiverKindPragmatic  = "pragmatic"
)

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
