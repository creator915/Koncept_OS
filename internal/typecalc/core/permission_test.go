package core

import "testing"

func TestCapability_Matches(t *testing.T) {
	c := Capability("read_file:K/defs/*")
	if !c.Matches("read_file", "K/defs/foo.ts") {
		t.Fatal("should match glob")
	}
	if c.Matches("write_file", "K/defs/foo.ts") {
		t.Fatal("verb mismatch should fail")
	}
	if c.Matches("read_file", "src/foo.ts") {
		t.Fatal("path outside glob should fail")
	}

	wild := Capability("run_tool:*")
	if !wild.Matches("run_tool", "anything") {
		t.Fatal("wildcard should match anything")
	}
}

func TestCapSet_Authorize(t *testing.T) {
	caps := CapSet{
		"read_file:K/defs/*",
		"run_tool:graph_validate",
	}
	if denied := caps.Authorize("read_file", "K/defs/x.ts"); denied != nil {
		t.Fatalf("should authorize, got: %v", denied.Payload)
	}
	if denied := caps.Authorize("run_tool", "bash"); denied == nil {
		t.Fatal("should deny run_tool:bash")
	}
}

func TestCapSet_Subset(t *testing.T) {
	parent := CapSet{"read_file:*", "run_tool:*", "write_file:src/*"}
	child := CapSet{"read_file:*", "run_tool:*"}
	if !child.Subset(parent) {
		t.Fatal("child should be subset of parent")
	}
	rebel := CapSet{"read_file:*", "spawn_agent:*"}
	if rebel.Subset(parent) {
		t.Fatal("rebel has caps parent lacks; should not be subset")
	}
	if err := rebel.SubsetCheck(parent); err == nil {
		t.Fatal("SubsetCheck should error")
	}
}

func TestPresetByName(t *testing.T) {
	if _, ok := PresetByName("implementer"); !ok {
		t.Fatal("implementer preset missing")
	}
	if _, ok := PresetByName("nonexistent"); ok {
		t.Fatal("unknown preset should not match")
	}
}
