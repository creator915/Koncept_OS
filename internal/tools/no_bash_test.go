package tools

import "testing"

// TestBuiltins_NeverExposesUniversalBash is the regression guard for the
// unconditional removal of the universal `bash` tool. The model-facing
// arbitrary-shell tool must NOT exist in the global catalog in ANY mode:
// no capability contract, no flag, no escape hatch can bring it back.
// Shell capability is reachable only through the command-locked
// sub-tools (compile/run_local/probe), which take typed argv/stdin and
// never a command string.
//
// If this test fails, someone re-registered a general shell tool —
// that re-opens the entire forensic cheat surface (curl/git/strings the
// reference). Do not "fix" it by deleting this test.
func TestBuiltins_NeverExposesUniversalBash(t *testing.T) {
	b := Builtins()

	if _, ok := b["bash"]; ok {
		t.Fatalf(`global Builtins() exposes a "bash" tool — universal shell must be removed unconditionally`)
	}

	// Belt-and-suspenders: reject any tool whose advertised spec name is
	// a general shell, even if registered under a different map key.
	banned := map[string]bool{"bash": true, "sh": true, "shell": true, "exec": true, "system": true}
	for key, tl := range b {
		name := tl.Spec.Function.Name
		if banned[name] {
			t.Fatalf("tool registered under key %q advertises banned general-shell name %q", key, name)
		}
	}
}
