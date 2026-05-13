package agent

import (
	"testing"

	"github.com/creator915/Koncept_OS/internal/tools"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

func TestResolveChildCaps_RoleResolves(t *testing.T) {
	r := &SubAgentRunner{parentCaps: core.CapsRoot}
	caps, err := r.resolveChildCaps(tools.SubAgentRequest{Role: "implementer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) == 0 {
		t.Fatal("implementer preset should be non-empty")
	}
}

func TestResolveChildCaps_UnknownRoleErrors(t *testing.T) {
	r := &SubAgentRunner{parentCaps: core.CapsRoot}
	if _, err := r.resolveChildCaps(tools.SubAgentRequest{Role: "doesnotexist"}); err == nil {
		t.Fatal("unknown role should error")
	}
}

func TestResolveChildCaps_RejectsSupersetOfParent(t *testing.T) {
	// Parent only has run_tool:graph_validate; child asks for run_tool:* —
	// must be refused.
	r := &SubAgentRunner{parentCaps: core.CapSet{"run_tool:graph_validate"}}
	_, err := r.resolveChildCaps(tools.SubAgentRequest{
		Caps: []string{"run_tool:graph_validate", "run_tool:bash"},
	})
	if err == nil {
		t.Fatal("child caps wider than parent must be refused")
	}
}

func TestResolveChildCaps_AllowsSubset(t *testing.T) {
	r := &SubAgentRunner{parentCaps: core.CapSet{
		"read_file:*", "run_tool:*",
	}}
	caps, err := r.resolveChildCaps(tools.SubAgentRequest{
		Caps: []string{"read_file:K/defs/*"},
	})
	if err != nil {
		t.Fatalf("subset should be allowed: %v", err)
	}
	if len(caps) != 1 || string(caps[0]) != "read_file:K/defs/*" {
		t.Fatalf("caps not preserved: %v", caps)
	}
}

func TestResolveChildCaps_InheritsWhenUnscoped(t *testing.T) {
	// Parent unscoped + no role/caps in request → child also unscoped.
	r := &SubAgentRunner{}
	caps, err := r.resolveChildCaps(tools.SubAgentRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) != 0 {
		t.Fatalf("unscoped parent + no req → unscoped child, got: %v", caps)
	}
}

func TestResolveChildCaps_NoSubsetCheckWhenParentUnscoped(t *testing.T) {
	// Parent unscoped, child requests specific caps — accept (the parent
	// implicitly has everything; the child voluntarily narrows itself).
	r := &SubAgentRunner{}
	caps, err := r.resolveChildCaps(tools.SubAgentRequest{
		Role: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) == 0 {
		t.Fatal("tester preset should resolve")
	}
}
