package agent

import (
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

func TestAuthorizeToolCall_NilCapsAllowsAll(t *testing.T) {
	// Top-level loops pass nil caps — gate must short-circuit to allow.
	if denied := authorizeToolCall(nil, "bash", `{"command":"rm -rf /"}`); denied != nil {
		t.Fatalf("nil caps must disable the gate, got: %v", denied.Payload)
	}
	if denied := authorizeToolCall(core.CapSet{}, "bash", `{}`); denied != nil {
		t.Fatalf("empty caps must disable the gate, got: %v", denied.Payload)
	}
}

func TestAuthorizeToolCall_ReadFile_GlobConstraint(t *testing.T) {
	caps := core.CapSet{"read_file:K/defs/*"}
	args := `{"path":"K/defs/Foo.ts"}`
	if denied := authorizeToolCall(caps, "read_file", args); denied != nil {
		t.Fatalf("should allow K/defs/* read, got denial: %v", denied.Payload)
	}
	if denied := authorizeToolCall(caps, "read_file", `{"path":"src/main.ts"}`); denied == nil {
		t.Fatal("should deny read outside K/defs/")
	}
}

func TestAuthorizeToolCall_WriteFile_BothShapes(t *testing.T) {
	caps := core.CapSet{"write_file:src/*"}
	// `path` form
	if denied := authorizeToolCall(caps, "write_file", `{"path":"src/x.ts"}`); denied != nil {
		t.Fatalf("should allow src/* write: %v", denied.Payload)
	}
	// `file_path` form (used by edit tool)
	if denied := authorizeToolCall(caps, "edit", `{"file_path":"src/x.ts"}`); denied != nil {
		t.Fatalf("should allow edit src/*: %v", denied.Payload)
	}
}

func TestAuthorizeToolCall_RunToolWildcard(t *testing.T) {
	caps := core.CapSet{"run_tool:*"}
	for _, name := range []string{"graph_validate", "session_show", "checkpoint_freeze"} {
		if denied := authorizeToolCall(caps, name, "{}"); denied != nil {
			t.Errorf("run_tool:* should allow %s: %v", name, denied.Payload)
		}
	}
}

func TestAuthorizeToolCall_NarrowRunTool(t *testing.T) {
	caps := core.CapSet{"run_tool:graph_validate"}
	if denied := authorizeToolCall(caps, "graph_validate", "{}"); denied != nil {
		t.Fatalf("specific run_tool should be allowed: %v", denied.Payload)
	}
	if denied := authorizeToolCall(caps, "bash", "{}"); denied == nil {
		t.Fatal("bash should be denied under run_tool:graph_validate")
	}
}

func TestRenderPermissionDenied_CarriesContext(t *testing.T) {
	caps := core.CapSet{"run_tool:graph_validate"}
	denied := authorizeToolCall(caps, "bash", "{}")
	if denied == nil {
		t.Fatal("expected denial")
	}
	out := renderPermissionDenied(denied, "bash")
	if !strings.Contains(out, "PermissionDenied") {
		t.Errorf("rendering missing tag: %s", out)
	}
	if !strings.Contains(out, "bash") {
		t.Errorf("rendering missing tool name: %s", out)
	}
	if !strings.Contains(out, "graph_validate") {
		t.Errorf("rendering missing available cap: %s", out)
	}
}
