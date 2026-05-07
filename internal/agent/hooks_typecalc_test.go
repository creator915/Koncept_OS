package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTypecalcUse_FlagsConfirmWithoutEvidence(t *testing.T) {
	chdirTo(t, t.TempDir())
	h := &typecalcUseHook{}
	args := `{"id":"InitGame","patch":"{\"status\":\"confirmed\",\"impl\":\"index.html\"}"}`
	out := h.After("graph_merge_object", args, "ok")
	if out == "" {
		t.Fatal("expected violation when confirming without typecalc evidence")
	}
	if !strings.Contains(out, "typecalc_compile") {
		t.Errorf("violation should suggest typecalc_compile: %s", out)
	}
	if !strings.Contains(out, "InitGame") {
		t.Errorf("violation should reference object id: %s", out)
	}
}

func TestTypecalcUse_PassesWhenEvidencePresent(t *testing.T) {
	dir := t.TempDir()
	evidenceDir := filepath.Join(dir, ".kcpos", "typecalc-evidence")
	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidenceDir, "InitGame.json"),
		[]byte(`{"objectId":"InitGame","kind":"compile","ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	chdirTo(t, dir)

	h := &typecalcUseHook{}
	args := `{"id":"InitGame","patch":"{\"status\":\"confirmed\",\"impl\":\"index.html\"}"}`
	if v := h.After("graph_merge_object", args, "ok"); v != "" {
		t.Errorf("evidence file present, should pass: %s", v)
	}
}

func TestTypecalcUse_IgnoresOtherStatuses(t *testing.T) {
	chdirTo(t, t.TempDir())
	h := &typecalcUseHook{}
	for _, status := range []string{"declared", "implementing"} {
		args := `{"id":"X","patch":"{\"status\":\"` + status + `\"}"}`
		if v := h.After("graph_merge_object", args, "ok"); v != "" {
			t.Errorf("status=%s should not require typecalc evidence: %s", status, v)
		}
	}
}

func TestTypecalcUse_IgnoresNonMergeTools(t *testing.T) {
	chdirTo(t, t.TempDir())
	h := &typecalcUseHook{}
	if v := h.After("graph_create_object", `{"id":"X"}`, "ok"); v != "" {
		t.Errorf("non-merge tool should be ignored: %s", v)
	}
}
