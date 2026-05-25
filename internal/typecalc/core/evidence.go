package core

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// EvidenceDir is the v8.x split-per-kind directory. v9.0 unified the
// schema into a single bundle under BundleDir (.kcpos/typecalc/<id>.json,
// see bundle.go). EvidenceDir is kept only as a constant for any
// remaining legacy references; new code paths use BundlePath.
//
// 2026-05-11 v9.0 — split files (.spec.json, .tests.json, etc.) no
// longer exist; ReadSpec / WriteSpec / ReadEvidence etc. now load and
// store sections of the unified bundle.
const EvidenceDir = ".kcpos/typecalc"

// Path helpers — v9.0 returns the unified bundle path for all kinds.
// External tooling that wants to inspect a single section should
// just read the bundle and pick the section.
func SpecEvidencePath(objectID string) string     { return BundlePath(objectID) }
func AcceptedEvidencePath(objectID string) string { return BundlePath(objectID) }
func TestsEvidencePath(objectID string) string    { return BundlePath(objectID) }
func EvidenceRecordPath(objectID string) string   { return BundlePath(objectID) }

// WaiverEvidencePath removed in v9.2 (waiver mechanism gone).

// RuntimeTracePath is kept for backward-compatible API surface but
// now resolves to the same bundle path; the trace lives under the
// RuntimeTrace section of the bundle.
func RuntimeTracePath(objectID string) string { return BundlePath(objectID) }

// HashSource returns the canonical SHA-256 (hex) of a content blob.
// Used to detect when impl or spec has drifted out from under an
// existing accepted record.
func HashSource(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}

// SymbolFragmentHash returns the SHA-256 of the function-body fragment
// belonging to `symbol` within `content`. v9.0.2 introduced per-object
// staleness scoping: when the impl is HTML and multiple graph objects
// share one index.html, hashing the whole file means editing one
// function invalidates every object's evidence — the spec-stale storm.
// Hashing just the relevant fragment lets unrelated edits leave this
// object's spec intact.
//
// Returns (hash, true) when extraction succeeds. Returns
// (HashSource(content), false) when extraction fails (non-HTML impl,
// symbol not found, etc.) so callers can fall back to the whole-file
// hash without branching.
//
// Extraction strategy (deliberately narrow): regex match of
// `function <symbol>(...)` followed by a brace-balanced body. Misses
// arrow-function definitions, methods on objects, and other styles —
// those keep the whole-file fallback, which is correct (over-conservative)
// rather than silently wrong.
func SymbolFragmentHash(content, implPath, symbol string) (string, bool) {
	frag, ok := ExtractSymbolFragment(content, implPath, symbol)
	if !ok {
		return HashSource(content), false
	}
	return HashSource(frag), true
}

// ExtractSymbolFragment locates the JS function body for `symbol`
// inside `content`. Returns ("", false) when not applicable or not
// found.
func ExtractSymbolFragment(content, implPath, symbol string) (string, bool) {
	if symbol == "" || content == "" {
		return "", false
	}
	if !isHTMLPath(implPath) {
		return "", false
	}
	// Find `function <symbol>(` — must be at a word boundary on the
	// left so we don't match `myfunction`.
	target := "function " + symbol
	idx := strings.Index(content, target)
	if idx < 0 {
		return "", false
	}
	// Validate the character after `target` is `(` or whitespace then `(`.
	rest := content[idx+len(target):]
	i := 0
	for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t') {
		i++
	}
	if i >= len(rest) || rest[i] != '(' {
		return "", false
	}
	// Walk to the `)` matching the opening `(` (no nesting expected
	// in argument list, but be defensive).
	depth := 0
	end := i
	for end < len(rest) {
		switch rest[end] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				goto foundParenEnd
			}
		}
		end++
	}
	return "", false
foundParenEnd:
	// Skip whitespace until the opening `{` of the body.
	body := rest[end+1:]
	j := 0
	for j < len(body) && (body[j] == ' ' || body[j] == '\t' || body[j] == '\n' || body[j] == '\r') {
		j++
	}
	if j >= len(body) || body[j] != '{' {
		return "", false
	}
	// Balance braces. Naive (no string/comment handling) — good enough
	// for hashing; a false-positive matching `}` inside a string would
	// just stop the fragment early, producing a different hash than
	// `}`'s real owner — that's still stable across reads of the same
	// content, so it doesn't break staleness logic.
	depth = 0
	k := j
	for k < len(body) {
		switch body[k] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return target + rest[:end+1] + body[j:k+1], true
			}
		}
		k++
	}
	return "", false
}

func isHTMLPath(p string) bool {
	if p == "" {
		return false
	}
	lower := strings.ToLower(p)
	return strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".htm")
}

// --- Section types (shared with bundle.go) ---

// WaiverEvidence — removed in v9.2 along with the waiver mechanism.
// See the trailing comment block at the bottom of this file for the
// historical writer/reader stubs that were also removed.

// SpecEvidence is the v8.x flat struct.
// SymbolHash (v9.0.2): per-object fragment hash for single-file-impl
// projects — see EvidenceBundle.SymbolHash. Optional.
type SpecEvidence struct {
	ObjectID    string           `json:"objectId"`
	Kind        string           `json:"kind"`
	Description string           `json:"description"`
	Contract    []ContractClause `json:"contract,omitempty"`
	SourceHash  string           `json:"sourceHash"`
	SymbolHash  string           `json:"symbolHash,omitempty"`
	Timestamp   time.Time        `json:"timestamp"`
}

// StaticIssue is one finding from the mechanical (non-LLM) checker.
type StaticIssue struct {
	Code    string `json:"code"`
	Where   string `json:"where"`
	Message string `json:"message"`
}

// ReviewVerdict is the LLM's reasonableness verdict.
type ReviewVerdict struct {
	Verdict    string   `json:"verdict"`
	Reasons    []string `json:"reasons"`
	Confidence float64  `json:"confidence"`
}

// AcceptedEvidence is the v8.x flat struct.
type AcceptedEvidence struct {
	ObjectID       string        `json:"objectId"`
	Kind           string        `json:"kind"`
	OK             bool          `json:"ok"`
	StaticIssues   []StaticIssue `json:"staticIssues"`
	RuntimeIssues  []StaticIssue `json:"runtimeIssues,omitempty"`
	Reasonableness ReviewVerdict `json:"reasonableness"`
	SourceHash     string        `json:"sourceHash"`
	SpecHash       string        `json:"specHash"`
	TestsHash      string        `json:"testsHash,omitempty"`
	Timestamp      time.Time     `json:"timestamp"`
}

// TestsEvidence is the v8.x flat struct.
//
// ContractHash (2026-05-25 audit B2): a hash of the spec.Contract
// content at synth time. The synth cache MUST check both SpecHash
// (impl-content drift) AND ContractHash (contract drift via
// re-describe with same impl). Without it, re-describe with new
// clauses + same impl → cache hits → stale testCode with broken
// ContractRefs → contract-traceability gate fails → agent has no way
// to bust the cache.
//
// ContractRefs (2026-05-25, testCode-mode coverage): top-level list
// of clause IDs the testCode body covers as a whole. For schema-cases
// languages (JavaScript) every TestCase.ContractRefs already carries
// per-case granularity and this list is unused; for raw-testCode
// languages (C / Go / Python) the LLM emits a flat coverage
// declaration here because individual test functions in the testCode
// blob have no structured place to hang refs. The contract-
// traceability gate unions both sources when checking coverage so
// the same rule applies across all languages.
//
// LastSynthFailure (2026-05-25): see TestsSection.LastSynthFailure
// — H_repair_graph's reason-driven detector reads this to classify
// the structural failure (IO-boundary, over-coarse, ambiguous).
type TestsEvidence struct {
	ObjectID         string     `json:"objectId"`
	Kind             string     `json:"kind"`
	Lang             string     `json:"lang"`
	SpecHash         string     `json:"specHash"`
	ContractHash     string     `json:"contractHash,omitempty"`
	Timestamp        time.Time  `json:"timestamp"`
	Cases            []TestCase `json:"cases,omitempty"`
	TestCode         string     `json:"testCode,omitempty"`
	ContractRefs     []string   `json:"contractRefs,omitempty"`
	LastSynthFailure string     `json:"lastSynthFailure,omitempty"`
}

// HashContract returns a stable hash of a Contract slice. Sorts by ID
// before hashing so reordering by the LLM doesn't bust the cache when
// clause content is unchanged. Empty/nil contract hashes to the same
// stable value (so legacy bundles' empty ContractHash matches).
func HashContract(clauses []ContractClause) string {
	if len(clauses) == 0 {
		return ""
	}
	sorted := make([]ContractClause, len(clauses))
	copy(sorted, clauses)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	buf, err := json.Marshal(sorted)
	if err != nil {
		return ""
	}
	return HashSource(string(buf))
}

// TestCase is one entry in a schema-driven test suite.
//
// ContractRefs (2026-05-25, Step 3 of contract landing): the IDs of
// SpecEvidence.Contract clauses this case verifies. Required for the
// Step 4 [contract-traceability] gate: empty or unknown-ID refs fail
// confirm. One case may cite multiple clauses (a happy-path test
// often covers both an example and an invariant).
//
// Empty for legacy tests written before Step 3 — Step 4 grandfathers
// those via the SpecHash check (no Contract on disk → traceability
// rule skipped).
type TestCase struct {
	Name         string        `json:"name"`
	Setup        []SetupOp     `json:"setup,omitempty"`
	Call         string        `json:"call"`
	Expect       []Expectation `json:"expect,omitempty"`
	ContractRefs []string      `json:"contractRefs,omitempty"`
}

// SetupOp is a pre-call port assignment.
type SetupOp struct {
	Set   string          `json:"set"`
	Value json.RawMessage `json:"value"`
}

// Expectation is one assertion against an output port.
type Expectation struct {
	Port    string            `json:"port"`
	Equals  json.RawMessage   `json:"equals,omitempty"`
	Between *[2]float64       `json:"between,omitempty"`
	Type    string            `json:"type,omitempty"`
	Enum    []json.RawMessage `json:"enum,omitempty"`
	Truthy  *bool             `json:"truthy,omitempty"`
}

// RuntimeCall is one observed invocation of the impl during testing.
type RuntimeCall struct {
	Frame   string                     `json:"frame,omitempty"`
	Inputs  map[string]json.RawMessage `json:"inputs"`
	Outputs map[string]json.RawMessage `json:"outputs"`
}

// RuntimeTrace mirrors the v8.x flat layout. v9.0 stores this as the
// RuntimeTrace section inside the bundle; this type is kept for callers
// that work with the snapshot directly.
type RuntimeTrace struct {
	ObjectID string        `json:"objectId"`
	ImplHash string        `json:"implHash,omitempty"`
	Calls    []RuntimeCall `json:"calls"`
}

// EvidenceRecord is the v8.x flat compile/test result. v9.0 derives
// this from the bundle's Compile / Test sections on demand.
type EvidenceRecord struct {
	ObjectID  string `json:"objectId"`
	Kind      string `json:"kind"` // "compile" | "test" | "insufficient"
	Lang      string `json:"lang"`
	OK        bool   `json:"ok"`
	Log       string `json:"log,omitempty"`
	ImplHash  string `json:"implHash,omitempty"`
	Timestamp string `json:"timestamp"`
}

// --- v9.0 bundle-backed read/write API ---
//
// These keep the v8.x function names so callers in tools/, session/,
// graph/, etc. compile without churn. Each function loads the bundle,
// reads/writes one section, and (for writes) persists the whole bundle
// atomically.

// WriteSpec persists a SpecEvidence by storing it as the bundle's
// Spec section. The flat SpecEvidence.SourceHash is promoted to the
// bundle-level SourceHash (if non-empty), maintaining the v8.x staleness
// semantic that "spec hash drift = whole-object staleness".
//
// Step 5 single-source-of-truth (2026-05-25): characterization clauses
// in the existing Spec.Contract are MERGED with rec.Contract — the
// caller doesn't have to read-modify-write to preserve them. This
// makes characterization clauses survive any describe call without
// special-case logic in every caller (the prior B3 patch shape).
//
// Merge semantics:
//   - non-characterization clauses in existing Contract → REPLACED by
//     rec.Contract's same-kind clauses (describe is the source of
//     truth for example/invariant)
//   - characterization clauses in existing Contract → PRESERVED unless
//     rec.Contract carries a clause with the same ID (in which case
//     the caller is deliberately re-writing it)
//
// The invalidate path (tools/fs/write.go) is the one place that
// EXPLICITLY strips characterization clauses by writing a Spec rec
// with no char clauses + filtering them out before this merge —
// invalidate must always win.
func WriteSpec(rec *SpecEvidence) error {
	if rec == nil || rec.ObjectID == "" {
		return nil
	}
	rec.Kind = "spec"
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}
	b := LoadOrInitBundle(rec.ObjectID)
	merged := mergePreservingChar(b.Spec, rec.Contract)
	b.Spec = &SpecSection{
		Description: rec.Description,
		Contract:    merged,
		SpecHash:    rec.SourceHash,
		Timestamp:   rec.Timestamp,
	}
	if rec.SourceHash != "" {
		b.SourceHash = rec.SourceHash
	}
	if rec.SymbolHash != "" {
		b.SymbolHash = rec.SymbolHash
	}
	return SaveBundle(b)
}

// mergePreservingChar returns a Contract slice combining new clauses
// with characterization clauses from prior Spec that weren't
// explicitly overwritten by new (same ID). Order: new clauses first,
// preserved char clauses appended. nil prior → just newClauses.
func mergePreservingChar(prior *SpecSection, newClauses []ContractClause) []ContractClause {
	if prior == nil || len(prior.Contract) == 0 {
		return newClauses
	}
	newIDs := make(map[string]bool, len(newClauses))
	for _, c := range newClauses {
		newIDs[c.ID] = true
	}
	out := newClauses
	for _, c := range prior.Contract {
		if c.Kind != "characterization" {
			continue // describe owns non-char; let new replace
		}
		if newIDs[c.ID] {
			continue // caller explicitly rewrote this char clause
		}
		out = append(out, c)
	}
	return out
}

// ReadSpec returns the bundle's Spec section reshaped as the v8.x
// flat SpecEvidence for callers that haven't migrated.
func ReadSpec(objectID string) (*SpecEvidence, bool) {
	b, ok := ReadBundle(objectID)
	if !ok || b.Spec == nil {
		return nil, false
	}
	return &SpecEvidence{
		ObjectID:    objectID,
		Kind:        "spec",
		Description: b.Spec.Description,
		Contract:    b.Spec.Contract,
		SourceHash:  b.Spec.SpecHash,
		SymbolHash:  b.SymbolHash, // v9.0.2 — bundle-level field
		Timestamp:   b.Spec.Timestamp,
	}, true
}

// WriteAccepted stores the reviewer verdict as the Accepted section.
func WriteAccepted(rec *AcceptedEvidence) error {
	if rec == nil || rec.ObjectID == "" {
		return nil
	}
	rec.Kind = "accepted"
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}
	b := LoadOrInitBundle(rec.ObjectID)
	b.Accepted = &AcceptedSection{
		OK:             rec.OK,
		StaticIssues:   rec.StaticIssues,
		RuntimeIssues:  rec.RuntimeIssues,
		Reasonableness: rec.Reasonableness,
		SpecHash:       rec.SpecHash,
		TestsHash:      rec.TestsHash,
		Timestamp:      rec.Timestamp,
	}
	if rec.SourceHash != "" {
		b.SourceHash = rec.SourceHash
	}
	return SaveBundle(b)
}

// ReadAccepted returns the Accepted section reshaped as the v8.x flat
// AcceptedEvidence.
func ReadAccepted(objectID string) (*AcceptedEvidence, bool) {
	b, ok := ReadBundle(objectID)
	if !ok || b.Accepted == nil {
		return nil, false
	}
	a := b.Accepted
	return &AcceptedEvidence{
		ObjectID:       objectID,
		Kind:           "accepted",
		OK:             a.OK,
		StaticIssues:   a.StaticIssues,
		RuntimeIssues:  a.RuntimeIssues,
		Reasonableness: a.Reasonableness,
		SourceHash:     b.SourceHash,
		SpecHash:       a.SpecHash,
		TestsHash:      a.TestsHash,
		Timestamp:      a.Timestamp,
	}, true
}

// WriteTests stores the synthesized test suite as the Tests section.
func WriteTests(rec *TestsEvidence) error {
	if rec == nil || rec.ObjectID == "" {
		return nil
	}
	rec.Kind = "tests"
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}
	b := LoadOrInitBundle(rec.ObjectID)
	b.Tests = &TestsSection{
		Lang:             rec.Lang,
		SpecHash:         rec.SpecHash,
		ContractHash:     rec.ContractHash,
		Cases:            rec.Cases,
		TestCode:         rec.TestCode,
		ContractRefs:     rec.ContractRefs,
		LastSynthFailure: rec.LastSynthFailure,
		Timestamp:        rec.Timestamp,
	}
	return SaveBundle(b)
}

// ReadTests returns the Tests section reshaped as the v8.x flat
// TestsEvidence.
func ReadTests(objectID string) (*TestsEvidence, bool) {
	b, ok := ReadBundle(objectID)
	if !ok || b.Tests == nil {
		return nil, false
	}
	t := b.Tests
	return &TestsEvidence{
		ObjectID:         objectID,
		Kind:             "tests",
		Lang:             t.Lang,
		SpecHash:         t.SpecHash,
		ContractHash:     t.ContractHash,
		Timestamp:        t.Timestamp,
		Cases:            t.Cases,
		TestCode:         t.TestCode,
		ContractRefs:     t.ContractRefs,
		LastSynthFailure: t.LastSynthFailure,
	}, true
}

// WriteWaiver / ReadWaiver — removed in v9.2. The waiver mechanism is
// gone; no code path writes or consults waiver records.

// ReadRuntimeTrace returns the RuntimeTrace section reshaped as the
// v8.x flat RuntimeTrace.
func ReadRuntimeTrace(objectID string) (*RuntimeTrace, bool) {
	b, ok := ReadBundle(objectID)
	if !ok || b.RuntimeTrace == nil {
		return nil, false
	}
	return &RuntimeTrace{
		ObjectID: objectID,
		ImplHash: b.SourceHash,
		Calls:    b.RuntimeTrace.Calls,
	}, true
}

// RecordEvidence writes the most basic compile/test result. v9.0
// stores this in either the Compile or Test bundle section depending
// on kind. For "test"-kind, this is the "I ran the test runner and it
// returned X" record (verdict OK/false + log).
func RecordEvidence(objectID, kind, lang string, ok bool) error {
	return RecordEvidenceFull(objectID, kind, lang, ok, "", "")
}

// RecordEvidenceWithLog is the variant that also persists the test
// runner's combined log.
func RecordEvidenceWithLog(objectID, kind, lang string, ok bool, log string) error {
	return RecordEvidenceFull(objectID, kind, lang, ok, log, "")
}

// RecordEvidenceFull is the most-complete writer.
func RecordEvidenceFull(objectID, kind, lang string, ok bool, log, implHash string) error {
	return RecordEvidenceWithSymbol(objectID, kind, lang, ok, log, implHash, "")
}

// RecordEvidenceWithSymbol is the v9.0.2 variant that also stores a
// per-object fragment hash. Pass symbolHash="" to leave the existing
// bundle.SymbolHash untouched (use the plain RecordEvidenceFull when
// the call site can't compute a fragment hash).
func RecordEvidenceWithSymbol(objectID, kind, lang string, ok bool, log, implHash, symbolHash string) error {
	if objectID == "" {
		return nil
	}
	b := LoadOrInitBundle(objectID)
	if implHash != "" {
		b.SourceHash = implHash
	}
	if symbolHash != "" {
		b.SymbolHash = symbolHash
	}
	ts := time.Now().UTC()
	switch kind {
	case "compile", "insufficient":
		// v9.0 quirk: insufficient evidence at the compile step lives
		// in the Compile section with Kind="insufficient". The Test
		// section is separate — both can coexist (HTML projects
		// often have compile=insufficient and test=test).
		b.Compile = &CompileSection{Lang: lang, Kind: kind, OK: ok, Log: log, Timestamp: ts}
	case "test":
		b.Test = &TestSection{Lang: lang, Kind: kind, OK: ok, Log: log, Timestamp: ts}
	default:
		// Unknown kind — keep behavior compatible by stashing in Compile.
		b.Compile = &CompileSection{Lang: lang, Kind: kind, OK: ok, Log: log, Timestamp: ts}
	}
	return SaveBundle(b)
}

// ReadEvidence returns the most-recent compile/test record. v9.0
// prefers the Test section over Compile (since test evidence supersedes
// compile evidence in the gate's requirement chain). Callers that
// specifically want compile evidence should read the bundle directly.
func ReadEvidence(objectID string) (*EvidenceRecord, bool) {
	b, ok := ReadBundle(objectID)
	if !ok {
		return nil, false
	}
	if b.Test != nil {
		return &EvidenceRecord{
			ObjectID:  objectID,
			Kind:      b.Test.Kind,
			Lang:      b.Test.Lang,
			OK:        b.Test.OK,
			Log:       b.Test.Log,
			ImplHash:  b.SourceHash,
			Timestamp: b.Test.Timestamp.Format(time.RFC3339),
		}, true
	}
	if b.Compile != nil {
		return &EvidenceRecord{
			ObjectID:  objectID,
			Kind:      b.Compile.Kind,
			Lang:      b.Compile.Lang,
			OK:        b.Compile.OK,
			Log:       b.Compile.Log,
			ImplHash:  b.SourceHash,
			Timestamp: b.Compile.Timestamp.Format(time.RFC3339),
		}, true
	}
	return nil, false
}

// SetRuntimeTrace overwrites the RuntimeTrace section. Called by the
// harness when it finishes a typecalc_test run. Always overwrites —
// v8.7 append-from-disk pollution is structurally prevented now (v9.0
// has no "load and append" path; each run rewrites the section
// wholesale).
func SetRuntimeTrace(objectID string, calls []RuntimeCall) error {
	if objectID == "" {
		return nil
	}
	b := LoadOrInitBundle(objectID)
	b.RuntimeTrace = &RuntimeTraceSection{Calls: calls}
	return SaveBundle(b)
}

// SetRuntimeSmoke overwrites the RuntimeSmoke section. Called by
// runtime_smoke after the headless Chromium driver returns. Always
// overwrites — every smoke run is the new ground truth for that
// deliverable.
func SetRuntimeSmoke(objectID string, section *RuntimeSmokeSection) error {
	if objectID == "" || section == nil {
		return nil
	}
	if section.Timestamp.IsZero() {
		section.Timestamp = time.Now().UTC()
	}
	b := LoadOrInitBundle(objectID)
	b.RuntimeSmoke = section
	return SaveBundle(b)
}

// DetectEffectiveLang closes the "HTML loophole": HTML files whose
// content includes a `<script>` block are treated as JS for the purposes
// of test-evidence requirements.
func DetectEffectiveLang(content string, declared Lang) Lang {
	if declared != LangHTML {
		return declared
	}
	if HasInlineScript(content) {
		return LangJavaScript
	}
	return declared
}

// HasInlineScript reports whether content contains a non-empty
// `<script>...</script>` block.
func HasInlineScript(content string) bool {
	open := strings.Index(strings.ToLower(content), "<script")
	if open < 0 {
		return false
	}
	close := strings.Index(strings.ToLower(content[open:]), "</script>")
	if close < 0 {
		return false
	}
	gt := strings.Index(content[open:], ">")
	if gt < 0 {
		return false
	}
	body := content[open+gt+1 : open+close]
	return strings.TrimSpace(body) != ""
}

// Re-export filepath so external test files can build the unified path
// directly when constructing fixtures.
var _ = filepath.Join

// _ silences os import use; the package keeps os for atomic writes via
// SaveBundle and for the EvidenceDir constant's mkdir callers.
var _ = os.MkdirAll
