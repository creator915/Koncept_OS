package typecalc

import (
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
const EvidenceDir = ".kcpos/typecalc-evidence"

// EvidenceRecord mirrors the JSON layout written by RecordEvidence and
// read by callers (gate, hooks). Use json.Unmarshal with this struct
// shape to inspect kind/lang/ok.
type EvidenceRecord struct {
	ObjectID  string `json:"objectId"`
	Kind      string `json:"kind"` // "compile" | "test"
	Lang      string `json:"lang"`
	OK        bool   `json:"ok"`
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
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	raw, _ := json.MarshalIndent(rec, "", "  ")
	return os.WriteFile(filepath.Join(EvidenceDir, objectID+".json"), raw, 0o644)
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
