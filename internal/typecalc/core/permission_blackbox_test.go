package core

import "testing"

// These tests are the CI fait-accompli for the 2026-05-17 PB-kcpos
// forensic finding (docs/experiments/pb-kcpos-FORENSIC-2026-05-18.md
// §7). They pin the *security property* of CapsBlackbox: a general
// shell / arbitrary-exec tool is mechanically unreachable under this
// profile. Eroding the profile (adding run_tool:* or run_tool:bash, or
// a wildcard that re-admits a shell) BREAKS THE BUILD — that is the
// whole point: the constraint is a checked invariant, not a convention.

// TestCapsBlackbox_DeniesShell is the load-bearing assertion. Every
// observed cheat in the forensic 10 (curl / git clone / go get /
// docker exec strings|readelf / overwriting the reference binary) went
// through the single general shell tool, which maps to
// run_tool:bash. If this ever passes-through, the profile is void.
func TestCapsBlackbox_DeniesShell(t *testing.T) {
	if CapsBlackbox.Authorize("run_tool", "bash") == nil {
		t.Fatal("CapsBlackbox MUST deny run_tool:bash — a shell under this profile reopens the entire forensic cheat surface")
	}
	// Defense-shaped names that must also never resolve to an exec
	// capability under this profile.
	for _, name := range []string{"sh", "zsh", "curl", "wget", "git", "go", "docker", "python3", "exec"} {
		if CapsBlackbox.Authorize("run_tool", name) == nil {
			t.Fatalf("CapsBlackbox unexpectedly authorizes run_tool:%s — only the explicitly-listed verification tools may be admitted", name)
		}
	}
}

// TestCapsBlackbox_NoExecWildcard guards against the erosion path:
// someone adds "run_tool:*" / "run_tool:**" / a bare "run_tool" /
// "spawn_agent:*" "for convenience". Any of those silently re-admits a
// shell; this test makes that a red build instead of a quiet diff.
func TestCapsBlackbox_NoExecWildcard(t *testing.T) {
	for _, c := range CapsBlackbox {
		v, a := c.Verb(), c.Arg()
		if v == "run_tool" && (a == "" || a == "*" || a == "**") {
			t.Fatalf("CapsBlackbox contains exec wildcard %q — this re-admits run_tool:bash; deny-by-default requires explicit per-tool grants", string(c))
		}
		if v == "spawn_agent" {
			t.Fatalf("CapsBlackbox contains %q — a black-box reconstruction is single-agent; spawn would allow widening", string(c))
		}
	}
}

// TestCapsBlackbox_AdmitsVerificationChain proves the profile is
// actually usable for the thing it exists for: running PB through the
// kcpos verification chain rather than a bare bash agent. (If these
// fail, the family-prefix globs are wrong and the profile is useless,
// which would be its own dishonesty.)
func TestCapsBlackbox_AdmitsVerificationChain(t *testing.T) {
	allow := []string{
		// 先决A: command-locked shell replacements MUST be admitted, or
		// the profile is unusable (which would be its own dishonesty).
		"compile", "run_local", "probe",
		"typecalc_compile", "typecalc_test", "typecalc_describe",
		"typecalc_synthesize_tests", "typecalc_review",
		"graph_validate", "graph_create_object", "graph_merge_attribute",
		"session_start", "session_gate_check", "session_status",
		"checkpoint_fill", "checkpoint_add_item", "confirm_object",
		"gate_object", "runtime_smoke", "list_dir", "grep", "glob",
		"git_status", "markdown_validate",
	}
	for _, name := range allow {
		if CapsBlackbox.Authorize("run_tool", name) != nil {
			t.Fatalf("CapsBlackbox must admit verification-chain tool run_tool:%s (family-prefix glob broken?)", name)
		}
	}
	// Path tools are gated per-call by path, not blocked wholesale.
	if CapsBlackbox.Authorize("read_file", "README.md") != nil {
		t.Fatal("CapsBlackbox must allow reading workspace files")
	}
	if CapsBlackbox.Authorize("write_file", "main.go") != nil {
		t.Fatal("CapsBlackbox must allow writing workspace source")
	}
}

// TestPresetByName_Blackbox: the contract selector must resolve, and a
// typo must NOT silently fall back to anything (fail-closed is enforced
// at the call site; here we just assert the lookup contract).
func TestPresetByName_Blackbox(t *testing.T) {
	cs, ok := PresetByName("blackbox")
	if !ok {
		t.Fatal(`PresetByName("blackbox") must resolve — the --contract selector depends on it`)
	}
	if cs.Authorize("run_tool", "bash") == nil {
		t.Fatal("preset resolved by name must be the shell-denying CapsBlackbox")
	}
	if _, ok := PresetByName("blackbx-typo"); ok {
		t.Fatal("unknown preset name must NOT resolve (caller fails closed on this)")
	}
}
