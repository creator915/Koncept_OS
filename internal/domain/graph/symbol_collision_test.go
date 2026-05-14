package graph

import (
	"strings"
	"testing"
)

// Tests for the v9.6.2 PascalCase symbol-collision guard added to
// AddAttribute / AddObject. Rationale: 2026-05-14 walk batch hit
// "PreviewContent redeclared" because attribute id "preview_content"
// (→ Go type PreviewContent) and object id "PreviewContent" (→ Go
// function PreviewContent) shared a symbol. The guard rejects the
// second create_* call so the conflict surfaces at graph mutation
// time, not at compile time.

func TestToPascalCase(t *testing.T) {
	cases := []struct{ in, want string }{
		{"preview_content", "PreviewContent"},
		{"PreviewContent", "PreviewContent"},
		{"preview-content", "PreviewContent"},
		{"flags_config", "FlagsConfig"},
		{"args", "Args"},
		{"Args", "Args"},
		{"a_b_c", "ABC"},
		{"", ""},
		{"_", ""},
	}
	for _, c := range cases {
		if got := toPascalCase(c.in); got != c.want {
			t.Errorf("toPascalCase(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSymbolCollides_NoExistingEntities(t *testing.T) {
	g := NewGraph()
	if got := g.SymbolCollides("Foo"); got != "" {
		t.Errorf("empty graph: want no collision, got %q", got)
	}
}

func TestSymbolCollides_AttributeBlocksObject(t *testing.T) {
	g := NewGraph()
	if err := g.AddAttribute("preview_content", NewAttribute("K/defs/preview_content.go", "")); err != nil {
		t.Fatal(err)
	}
	if got := g.SymbolCollides("PreviewContent"); got != "preview_content" {
		t.Errorf("want collision with preview_content, got %q", got)
	}
	// AddObject must fail with a helpful message.
	err := g.AddObject("PreviewContent", NewObject("K/defs/PreviewContent.go", "compute preview"))
	if err == nil {
		t.Fatal("expected AddObject to reject colliding id")
	}
	if !strings.Contains(err.Error(), "PreviewContent") {
		t.Errorf("error should mention the colliding symbol: %v", err)
	}
	if !strings.Contains(err.Error(), "preview_content") {
		t.Errorf("error should mention the pre-existing id: %v", err)
	}
}

func TestSymbolCollides_ObjectBlocksAttribute(t *testing.T) {
	g := NewGraph()
	if err := g.AddObject("PreviewContent", NewObject("K/defs/PreviewContent.go", "compute preview")); err != nil {
		t.Fatal(err)
	}
	err := g.AddAttribute("preview_content", NewAttribute("K/defs/preview_content.go", ""))
	if err == nil {
		t.Fatal("expected AddAttribute to reject colliding id")
	}
	if !strings.Contains(err.Error(), "redeclared") {
		t.Errorf("error should reference the redeclare risk: %v", err)
	}
}

func TestSymbolCollides_DistinctSymbolsAllowed(t *testing.T) {
	g := NewGraph()
	if err := g.AddAttribute("preview_content", NewAttribute("K/defs/preview_content.go", "")); err != nil {
		t.Fatal(err)
	}
	// PreviewResult is a different PascalCase symbol — must succeed.
	if err := g.AddObject("PreviewResult", NewObject("K/defs/PreviewResult.go", "build preview")); err != nil {
		t.Fatalf("unrelated symbol should be allowed: %v", err)
	}
}

func TestSymbolCollides_HyphenCase(t *testing.T) {
	g := NewGraph()
	if err := g.AddAttribute("flag-config", NewAttribute("K/defs/flag_config.go", "")); err != nil {
		t.Fatal(err)
	}
	// "flag_config" normalises to the same symbol as "flag-config".
	err := g.AddAttribute("flag_config", NewAttribute("K/defs/flag_config.go", ""))
	if err == nil {
		t.Fatal("expected hyphen vs underscore collision to be detected")
	}
}
