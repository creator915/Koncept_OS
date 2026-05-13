package protocol

import (
	"strings"
	"testing"
)

const sampleSpec = `# Project title

> preamble paragraph
> with multiple lines

## 1. Overview

Lead paragraph for chapter 1.

### 1.1 Sub-section

Body of sub-section 1.1.

### 1.2 Another sub-section

Body of 1.2.

## 2. Architecture

Architecture body.

## 3. Detailed Design

Detailed design body.

### 3.1 Modules

Modules description.
`

func TestParse_FindsAllChapters(t *testing.T) {
	chapters, preamble := Parse(sampleSpec)
	if len(chapters) != 6 { // 3 H2 + 3 H3
		t.Fatalf("expected 6 chapters, got %d: %v", len(chapters), chapters)
	}
	if !strings.Contains(preamble, "preamble paragraph") {
		t.Errorf("preamble should contain text before first H2; got %q", preamble)
	}
}

func TestParse_AssignsCorrectIDs(t *testing.T) {
	chapters, _ := Parse(sampleSpec)
	wantIDs := []string{"1", "1.1", "1.2", "2", "3", "3.1"}
	for i, want := range wantIDs {
		if chapters[i].ID != want {
			t.Errorf("chapter[%d].ID = %q, want %q (title=%q)", i, chapters[i].ID, want, chapters[i].Title)
		}
	}
}

func TestParse_BodyExcludesNextChapterHeading(t *testing.T) {
	chapters, _ := Parse(sampleSpec)
	overview := chapters[0] // §1
	if !strings.Contains(overview.Body, "Lead paragraph for chapter 1") {
		t.Errorf("overview body should contain lead paragraph; got %q", overview.Body)
	}
	if !strings.Contains(overview.Body, "### 1.1 Sub-section") {
		t.Errorf("§1 should include its sub-sections (1.1, 1.2); got %q", overview.Body)
	}
	if strings.Contains(overview.Body, "## 2. Architecture") {
		t.Errorf("§1 must NOT contain the next H2 heading; got %q", overview.Body)
	}
}

func TestParse_LineRangesMakeSense(t *testing.T) {
	chapters, _ := Parse(sampleSpec)
	for i, c := range chapters {
		if c.StartLine < 1 {
			t.Errorf("chapter[%d] start line %d < 1", i, c.StartLine)
		}
		if c.EndLine < c.StartLine {
			t.Errorf("chapter[%d] end line %d < start %d", i, c.EndLine, c.StartLine)
		}
	}
}

func TestFind_ByID(t *testing.T) {
	chapters, _ := Parse(sampleSpec)
	ch, ok := Find(chapters, "1.2")
	if !ok {
		t.Fatal("expected to find §1.2")
	}
	if ch.Title != "Another sub-section" {
		t.Errorf("§1.2 title = %q, want %q", ch.Title, "Another sub-section")
	}
}

func TestFind_BySectionPrefix(t *testing.T) {
	chapters, _ := Parse(sampleSpec)
	ch, ok := Find(chapters, "§3")
	if !ok {
		t.Fatal("expected to find §3 with leading § prefix")
	}
	if ch.ID != "3" {
		t.Errorf("matched chapter id = %q, want %q", ch.ID, "3")
	}
}

func TestFind_ByTitleFallback(t *testing.T) {
	chapters, _ := Parse(sampleSpec)
	ch, ok := Find(chapters, "architecture")
	if !ok {
		t.Fatal("expected case-insensitive title fallback to match")
	}
	if ch.ID != "2" {
		t.Errorf("matched id = %q, want %q", ch.ID, "2")
	}
}

func TestFind_Missing(t *testing.T) {
	chapters, _ := Parse(sampleSpec)
	if _, ok := Find(chapters, "999"); ok {
		t.Error("expected miss for non-existent id")
	}
}

func TestValidate_HappyPath(t *testing.T) {
	chapters, _ := Parse(sampleSpec)
	issues := Validate(chapters, DefaultValidationConfig())
	if len(issues) != 0 {
		t.Errorf("expected valid sample to produce no issues; got %v", issues)
	}
}

func TestValidate_FlagsMissingID(t *testing.T) {
	bad := `# Title

## Overview

body
`
	chapters, _ := Parse(bad)
	issues := Validate(chapters, DefaultValidationConfig())
	if !hasIssue(issues, "missing-id") {
		t.Errorf("expected missing-id issue; got %v", issues)
	}
}

func TestValidate_FlagsDuplicateID(t *testing.T) {
	bad := `# Title

## 1. Foo
foo body

## 1. Bar
bar body
`
	chapters, _ := Parse(bad)
	issues := Validate(chapters, DefaultValidationConfig())
	if !hasIssue(issues, "duplicate-id") {
		t.Errorf("expected duplicate-id issue; got %v", issues)
	}
}

func TestValidate_FlagsOversize(t *testing.T) {
	// build a sample with one huge chapter
	huge := strings.Repeat("xxxx ", 5000) // ~5K tokens latin
	bad := "# Title\n\n## 1. Huge\n" + huge + "\n\n## 2. Small\nsmall body\n"
	chapters, _ := Parse(bad)
	cfg := DefaultValidationConfig()
	cfg.MaxChapterTokens = 1000 // tighter for the test
	issues := Validate(chapters, cfg)
	if !hasIssue(issues, "oversize") {
		t.Errorf("expected oversize issue; got %v", issues)
	}
}

func TestValidate_FlagsTooFewChapters(t *testing.T) {
	bad := `# Title

## Only chapter

body
`
	chapters, _ := Parse(bad)
	issues := Validate(chapters, DefaultValidationConfig())
	if !hasIssue(issues, "no-chapters") {
		t.Errorf("expected no-chapters issue when only 1 chapter; got %v", issues)
	}
}

func TestApproxTokens_LatinVsCJK(t *testing.T) {
	cLatin := Chapter{Body: strings.Repeat("a", 1000)}
	cCJK := Chapter{Body: strings.Repeat("中", 1000)}
	if cLatin.ApproxTokens() == 0 {
		t.Error("latin estimate should be non-zero")
	}
	// CJK bytes are 3 in UTF-8, so 1000 chars = 3000 bytes. /2 = 1500 tokens.
	// Latin 1000 bytes /4 = 250 tokens.
	if cCJK.ApproxTokens() <= cLatin.ApproxTokens() {
		t.Errorf("CJK-heavy chapter (%d tokens) should estimate higher than latin (%d) for same char count", cCJK.ApproxTokens(), cLatin.ApproxTokens())
	}
}

func hasIssue(issues []ValidationIssue, code string) bool {
	for _, iss := range issues {
		if iss.Code == code {
			return true
		}
	}
	return false
}
