package typecalc

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EvidenceDir is the on-disk record of which graph entities have been
// mechanically validated via typecalc compile/test. The agent-side
// `typecalc-use` enforcement hook checks for the presence of
// <objectID>.json before allowing graph_merge_object status=confirmed —
// without this trail, "confirmed" is just a string the LLM typed, not a
// verified state.
//
// Layout (one file per kind so newer evidence does not clobber older):
//
//	<id>.json           kind=compile | kind=test  (existing schema)
//	<id>.spec.json      auto-generated description (kind=spec)
//	<id>.accepted.json  reviewer verdict           (kind=accepted)
const EvidenceDir = ".kcpos/typecalc-evidence"

// SpecEvidencePath returns the path to the auto-generated description
// record for objectID. The file may or may not exist; callers should
// treat absence as "not yet described".
func SpecEvidencePath(objectID string) string {
	return filepath.Join(EvidenceDir, objectID+".spec.json")
}

// AcceptedEvidencePath returns the path to the reviewer-verdict record.
func AcceptedEvidencePath(objectID string) string {
	return filepath.Join(EvidenceDir, objectID+".accepted.json")
}

// WaiverEvidencePath returns the path to a per-object waiver record.
// Waivers exist to acknowledge that mechanical verification is not
// possible (kind=insufficient evidence) and capture why we accept the
// object anyway. The gate refuses to confirm an Insufficient object
// without a matching waiver.
func WaiverEvidencePath(objectID string) string {
	return filepath.Join(EvidenceDir, objectID+".waiver.json")
}

// WaiverEvidence is the human-decision record that lets an Insufficient
// object pass the gate. It MUST contain a Reason explaining how the
// object will be verified out-of-band (manual review, screenshot,
// downstream integration test, etc.). Auto-generated waivers are
// rejected — the field is checked for non-emptiness against a
// stop-list of hand-wavy phrases.
type WaiverEvidence struct {
	ObjectID  string    `json:"objectId"`
	Kind      string    `json:"kind"` // always "waiver"
	Reason    string    `json:"reason"`
	Verifier  string    `json:"verifier,omitempty"` // who/what does the out-of-band check
	Timestamp time.Time `json:"timestamp"`
}

// TestsEvidencePath returns the path to the spec-synthesized test
// suite. When the agent calls typecalc_synthesize_tests, the generated
// suite is stored here so a later review can verify the tests came from
// the spec (not the impl) and detect drift via TestsHash.
func TestsEvidencePath(objectID string) string {
	return filepath.Join(EvidenceDir, objectID+".tests.json")
}

// RuntimeTracePath is the per-object runtime call log produced by the
// instrumented test suite. Tests synthesized by typecalc_synthesize_tests
// are instructed to append every call's inputs and outputs here so the
// runtime check (B-side: "ports / value range / timing") can compare
// observed values against the graph's valueSpace / produces / temporal
// declarations.
func RuntimeTracePath(objectID string) string {
	return filepath.Join(".kcpos", "typecalc-runtime", objectID+".json")
}

// SpecEvidence is the post-implementation description an LLM produces
// after reading the impl + signature. It supplements (does not replace)
// the original `intent` field on the graph; downstream review compares
// the two.
//
// SourceHash is a SHA-256 of the impl content — if the impl changes
// after the description was written, callers should consider the
// description stale and regenerate.
type SpecEvidence struct {
	ObjectID    string    `json:"objectId"`
	Kind        string    `json:"kind"` // always "spec"
	Description string    `json:"description"`
	SourceHash  string    `json:"sourceHash"`
	Timestamp   time.Time `json:"timestamp"`
}

// StaticIssue is one finding from the mechanical (non-LLM) check that
// runs before the reasonableness review. Examples: produces declared
// but never returned, valueSpace empty on a confirmed attribute, impl
// missing.
type StaticIssue struct {
	Code    string `json:"code"`
	Where   string `json:"where"`
	Message string `json:"message"`
}

// ReviewVerdict is the LLM's reasonableness judgement. `Verdict` is
// "pass" | "fail"; `Confidence` is a self-stated 0–1.
type ReviewVerdict struct {
	Verdict    string   `json:"verdict"`
	Reasons    []string `json:"reasons"`
	Confidence float64  `json:"confidence"`
}

// AcceptedEvidence is the combined output of the static check + the
// reasonableness review. `OK` is true iff there are no static issues
// AND the LLM verdict is "pass". RuntimeIssues, when non-empty, also
// flips OK to false — runtime port-signal mismatches are mechanical
// failures, not opinions.
type AcceptedEvidence struct {
	ObjectID       string        `json:"objectId"`
	Kind           string        `json:"kind"` // always "accepted"
	OK             bool          `json:"ok"`
	StaticIssues   []StaticIssue `json:"staticIssues"`
	RuntimeIssues  []StaticIssue `json:"runtimeIssues,omitempty"`
	Reasonableness ReviewVerdict `json:"reasonableness"`
	SourceHash     string        `json:"sourceHash"`
	SpecHash       string        `json:"specHash"`
	TestsHash      string        `json:"testsHash,omitempty"`
	Timestamp      time.Time     `json:"timestamp"`
}

// TestsEvidence is the spec-derived test suite produced by
// typecalc_synthesize_tests. It is stored separately from the
// description (SpecEvidence) so the reviewer can confirm tests came
// from intent + description, not from the impl source.
//
// SpecHash is the source hash that was current at synthesis time; if
// the impl drifts, the test suite is also (likely) stale.
//
// Two encodings supported:
//
//   - **Cases (preferred, schema-driven)**: structured test cases the
//     language harness renders into runtime test code. The LLM only
//     declares what to call and what to expect; the harness handles
//     trace logging, assertion ordering ("appendTrace BEFORE assert"),
//     port snapshotting, etc. This eliminates the class of issues
//     where each LLM-written test framework differs and trace logging
//     is lost on assertion failure.
//   - **TestCode (legacy fallback)**: raw test source the LLM wrote
//     directly. Used for languages without a harness implementation.
type TestsEvidence struct {
	ObjectID  string     `json:"objectId"`
	Kind      string     `json:"kind"` // always "tests"
	Lang      string     `json:"lang"`
	SpecHash  string     `json:"specHash"`
	Timestamp time.Time  `json:"timestamp"`
	Cases     []TestCase `json:"cases,omitempty"`    // schema-driven (preferred)
	TestCode  string     `json:"testCode,omitempty"` // legacy raw source
}

// TestCase is one entry in a schema-driven test suite. The harness
// renders setup → call → snapshot ports → appendTrace → assertions in
// that fixed order, so trace logging cannot be skipped by an
// assertion-throwing-first bug.
type TestCase struct {
	Name   string         `json:"name"`
	Setup  []SetupOp      `json:"setup,omitempty"`
	Call   string         `json:"call"`
	Expect []Expectation  `json:"expect,omitempty"`
}

// SetupOp is a pre-call port assignment. For JS, it sets globalThis[Set]
// to Value; analogous semantics for other harnessed languages.
type SetupOp struct {
	Set   string          `json:"set"`
	Value json.RawMessage `json:"value"`
}

// Expectation is one assertion against an output port. Exactly one of
// the comparator fields should be set; behaviour is undefined when
// multiple are provided.
type Expectation struct {
	Port    string          `json:"port"`
	Equals  json.RawMessage `json:"equals,omitempty"`
	Between *[2]float64     `json:"between,omitempty"`
	Type    string          `json:"type,omitempty"` // number|string|boolean|object|array
	Enum    []json.RawMessage `json:"enum,omitempty"`
	Truthy  *bool           `json:"truthy,omitempty"`
}

// RuntimeCall is one observed invocation of the impl during testing.
// Inputs and outputs are arbitrary JSON values keyed by attribute name
// (the synthesized test code is taught to use port names matching the
// graph's consumes/produces lists).
type RuntimeCall struct {
	// Frame is optional — only meaningful for objects with a temporal
	// declaration. Empty string means "non-temporal".
	Frame   string                     `json:"frame,omitempty"`
	Inputs  map[string]json.RawMessage `json:"inputs"`
	Outputs map[string]json.RawMessage `json:"outputs"`
}

// RuntimeTrace is the JSON layout the synthesized tests append to
// .kcpos/typecalc-runtime/<id>.json. Each call records the function's
// inputs and outputs so the static-check pass can verify port presence
// and values against the graph's declarations.
//
// ImplHash (D3) records which impl version produced this trace.
// evidence-stale rule fails the object when the current impl hash
// differs — preventing the leftover-trace bug where old runs pass for
// a new impl.
type RuntimeTrace struct {
	ObjectID string        `json:"objectId"`
	ImplHash string        `json:"implHash,omitempty"`
	Calls    []RuntimeCall `json:"calls"`
}

// HashSource returns the canonical SHA-256 (hex) of a content blob.
// Used to detect when impl or spec has drifted out from under an
// existing accepted record.
func HashSource(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}

// WriteSpec persists a SpecEvidence under EvidenceDir. Empty objectID
// is a no-op (returns nil) — symmetric with RecordEvidence.
func WriteSpec(rec *SpecEvidence) error {
	if rec == nil || rec.ObjectID == "" {
		return nil
	}
	if err := os.MkdirAll(EvidenceDir, 0o755); err != nil {
		return fmt.Errorf("mkdir evidence dir: %w", err)
	}
	rec.Kind = "spec"
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(SpecEvidencePath(rec.ObjectID), raw, 0o644)
}

// ReadSpec loads a previously-written SpecEvidence. Returns (nil, false)
// for missing or malformed files.
func ReadSpec(objectID string) (*SpecEvidence, bool) {
	if objectID == "" {
		return nil, false
	}
	raw, err := os.ReadFile(SpecEvidencePath(objectID))
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	var rec SpecEvidence
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, false
	}
	return &rec, true
}

// WriteAccepted persists an AcceptedEvidence under EvidenceDir.
func WriteAccepted(rec *AcceptedEvidence) error {
	if rec == nil || rec.ObjectID == "" {
		return nil
	}
	if err := os.MkdirAll(EvidenceDir, 0o755); err != nil {
		return fmt.Errorf("mkdir evidence dir: %w", err)
	}
	rec.Kind = "accepted"
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(AcceptedEvidencePath(rec.ObjectID), raw, 0o644)
}

// ReadAccepted loads a previously-written AcceptedEvidence.
func ReadAccepted(objectID string) (*AcceptedEvidence, bool) {
	if objectID == "" {
		return nil, false
	}
	raw, err := os.ReadFile(AcceptedEvidencePath(objectID))
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	var rec AcceptedEvidence
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, false
	}
	return &rec, true
}

// WriteTests persists a TestsEvidence under EvidenceDir.
func WriteTests(rec *TestsEvidence) error {
	if rec == nil || rec.ObjectID == "" {
		return nil
	}
	if err := os.MkdirAll(EvidenceDir, 0o755); err != nil {
		return fmt.Errorf("mkdir evidence dir: %w", err)
	}
	rec.Kind = "tests"
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(TestsEvidencePath(rec.ObjectID), raw, 0o644)
}

// ReadTests loads a previously-written TestsEvidence.
func ReadTests(objectID string) (*TestsEvidence, bool) {
	if objectID == "" {
		return nil, false
	}
	raw, err := os.ReadFile(TestsEvidencePath(objectID))
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	var rec TestsEvidence
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, false
	}
	return &rec, true
}

// WriteWaiver persists a WaiverEvidence under EvidenceDir.
func WriteWaiver(rec *WaiverEvidence) error {
	if rec == nil || rec.ObjectID == "" {
		return nil
	}
	if err := os.MkdirAll(EvidenceDir, 0o755); err != nil {
		return fmt.Errorf("mkdir evidence dir: %w", err)
	}
	rec.Kind = "waiver"
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(WaiverEvidencePath(rec.ObjectID), raw, 0o644)
}

// ReadWaiver loads a previously-written WaiverEvidence.
func ReadWaiver(objectID string) (*WaiverEvidence, bool) {
	if objectID == "" {
		return nil, false
	}
	raw, err := os.ReadFile(WaiverEvidencePath(objectID))
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	var rec WaiverEvidence
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, false
	}
	return &rec, true
}

// ReadRuntimeTrace loads a per-object runtime trace from
// .kcpos/typecalc-runtime/<id>.json. Returns (nil, false) when the
// file is missing or malformed — the runtime check rule treats
// absence as its own issue (`runtime-trace-missing`).
func ReadRuntimeTrace(objectID string) (*RuntimeTrace, bool) {
	if objectID == "" {
		return nil, false
	}
	raw, err := os.ReadFile(RuntimeTracePath(objectID))
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	var rec RuntimeTrace
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, false
	}
	return &rec, true
}

// EvidenceRecord mirrors the JSON layout written by RecordEvidence and
// read by callers (gate, hooks). Use json.Unmarshal with this struct
// shape to inspect kind/lang/ok.
//
// Log is the test runner's combined stdout+stderr from the most recent
// pass. Reviewers (typecalc_review) read this from the evidence rather
// than taking it as a string argument — that way the agent has no
// affordance to substitute a doctored log.
//
// ImplHash (D3) binds this evidence to a specific impl source state.
// The static-check rule `evidence-stale` fails any object whose
// current impl hash doesn't match the one recorded here. This makes
// evidence freshness a structural invariant — agents cannot keep
// stale Pass evidence and call it good after editing the impl.
type EvidenceRecord struct {
	ObjectID  string `json:"objectId"`
	Kind      string `json:"kind"` // "compile" | "test" | "insufficient"
	Lang      string `json:"lang"`
	OK        bool   `json:"ok"`
	Log       string `json:"log,omitempty"`
	ImplHash  string `json:"implHash,omitempty"`
	Timestamp string `json:"timestamp"`
}

// RecordEvidence writes a small JSON record under EvidenceDir attesting
// that the named entity passed a typecalc check. Callers are typically
// the typecalc_compile / typecalc_test agent tools and the auto-typecalc
// helpers in write_file / graph_merge_object.
//
// Empty objectID is a no-op (returns nil) — the helper sites pass through
// missing object_id arguments rather than guarding at every call site.
func RecordEvidence(objectID, kind, lang string, ok bool) error {
	return RecordEvidenceWithLog(objectID, kind, lang, ok, "")
}

// RecordEvidenceWithLog is the variant that also persists the test
// runner's combined log. typecalc_review reads this when judging
// reasonableness, so we keep it on disk rather than passing it through
// agent string args.
func RecordEvidenceWithLog(objectID, kind, lang string, ok bool, log string) error {
	return RecordEvidenceFull(objectID, kind, lang, ok, log, "")
}

// RecordEvidenceFull is the most-complete writer, including the impl
// hash so the evidence-stale rule can detect drift.
func RecordEvidenceFull(objectID, kind, lang string, ok bool, log, implHash string) error {
	if objectID == "" {
		return nil
	}
	if err := os.MkdirAll(EvidenceDir, 0o755); err != nil {
		return fmt.Errorf("mkdir evidence dir: %w", err)
	}
	rec := EvidenceRecord{
		ObjectID:  objectID,
		Kind:      kind,
		Lang:      lang,
		OK:        ok,
		Log:       log,
		ImplHash:  implHash,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	raw, _ := json.MarshalIndent(rec, "", "  ")
	return os.WriteFile(filepath.Join(EvidenceDir, objectID+".json"), raw, 0o644)
}

// ReadEvidence loads the existing compile/test evidence for objectID.
// Returns (nil, false) for missing or malformed files.
func ReadEvidence(objectID string) (*EvidenceRecord, bool) {
	if objectID == "" {
		return nil, false
	}
	raw, err := os.ReadFile(filepath.Join(EvidenceDir, objectID+".json"))
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	var rec EvidenceRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, false
	}
	return &rec, true
}

// DetectEffectiveLang closes the "HTML loophole" identified in the
// analysis report (problem 7.2): an HTML file whose content includes a
// `<script>` block is in practice a JavaScript container, and the
// test-evidence requirement should apply to the JS inside. When called
// with declared=LangHTML and content containing `<script>`, this returns
// LangJavaScript so downstream gate rules (typecalc-test-required) treat
// the file as JS and demand a real test.
//
// For other languages, returns declared unchanged. For pure HTML (no
// embedded script), keeps HTML — there's no JS to test.
func DetectEffectiveLang(content string, declared Lang) Lang {
	if declared != LangHTML {
		return declared
	}
	if HasInlineScript(content) {
		return LangJavaScript
	}
	return declared
}

// HasInlineScript reports whether the content contains a non-empty
// `<script>...</script>` block. Accepts any attributes on the open tag
// (e.g. `<script type="module">`) but requires a closing `</script>`
// with at least one non-whitespace character between.
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
