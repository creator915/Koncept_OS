package agent

import (
	"testing"

	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// TestFilterToolsByCaps_NonExposure pins the capability-non-exposure
// layer of forensic §7.2: under an active contract, `bash` is not even
// advertised to the model; with no contract, behavior is unchanged.
func TestFilterToolsByCaps_NonExposure(t *testing.T) {
	full := map[string]toolcall.Tool{
		"bash":             {},
		"typecalc_compile": {},
		"read_file":        {}, // path verb — kept (per-call gated)
		"grep":             {},
		"graph_validate":   {},
	}

	// No contract → unchanged (the human/interactive default).
	if got := filterToolsByCaps(full, nil); len(got) != len(full) {
		t.Fatalf("nil caps must not filter: got %d want %d", len(got), len(full))
	}
	if _, ok := filterToolsByCaps(full, core.CapSet{})["bash"]; !ok {
		t.Fatal("empty caps must keep bash (no contract = unchanged behavior)")
	}

	// Contract active → bash gone from the advertised set entirely.
	got := filterToolsByCaps(full, core.CapsBlackbox)
	if _, ok := got["bash"]; ok {
		t.Fatal("under CapsBlackbox, bash MUST NOT be advertised (capability non-exposure)")
	}
	for _, keep := range []string{"typecalc_compile", "grep", "graph_validate", "read_file"} {
		if _, ok := got[keep]; !ok {
			t.Fatalf("CapsBlackbox dropped a permitted tool %q (filter too aggressive)", keep)
		}
	}
}
