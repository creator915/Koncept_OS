package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleLargeMarkdown is sized above MarkdownInlineThresholdTokens
// (~5000) so MaybeOutlineMarkdown decides to outline. We use latin
// padding so byte-to-token math is the simple ÷4.
func sampleLargeMarkdown() string {
	pad := strings.Repeat("filler word ", 1500) // ~18KB → ~4.5K tokens per chapter when we use 2
	return "# Title\n\n" +
		"## 1. First chapter\n\n" + pad + "\n\n" +
		"## 2. Second chapter\n\n" + pad + "\n\n" +
		"## 3. Third chapter\n\n" + pad + "\n"
}

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMaybeOutlineMarkdown_LargeMarkdownTriggersOutline(t *testing.T) {
	content := []byte(sampleLargeMarkdown())
	out, swapped := MaybeOutlineMarkdown("foo.md", content, false)
	if !swapped {
		t.Fatal("expected outline swap for large markdown")
	}
	if !strings.Contains(out, "auto-outlined") {
		t.Errorf("outline should announce itself; got: %s", out)
	}
	for _, want := range []string{"§1 First chapter", "§2 Second chapter", "§3 Third chapter"} {
		if !strings.Contains(out, want) {
			t.Errorf("outline missing chapter %q; got: %s", want, out)
		}
	}
	if !strings.Contains(out, "markdown_section") {
		t.Error("outline should reference markdown_section tool")
	}
}

func TestMaybeOutlineMarkdown_ForceBypassesOutline(t *testing.T) {
	content := []byte(sampleLargeMarkdown())
	_, swapped := MaybeOutlineMarkdown("foo.md", content, true)
	if swapped {
		t.Error("force=true must bypass auto-outline")
	}
}

func TestMaybeOutlineMarkdown_SmallMarkdownPassesThrough(t *testing.T) {
	content := []byte("# Title\n\n## 1. Tiny\n\nshort body\n")
	_, swapped := MaybeOutlineMarkdown("foo.md", content, false)
	if swapped {
		t.Error("small markdown should be returned as-is, not outlined")
	}
}

func TestMaybeOutlineMarkdown_NonMarkdownIgnored(t *testing.T) {
	content := []byte(strings.Repeat("a", 50000))
	_, swapped := MaybeOutlineMarkdown("foo.txt", content, false)
	if swapped {
		t.Error("non-markdown extensions should pass through")
	}
}

func TestMaybeOutlineMarkdown_UnstructuredMarkdownPassesThrough(t *testing.T) {
	// 30KB of prose, no headings — chapter-granular access wouldn't help.
	content := []byte(strings.Repeat("just prose without any headings ", 1000))
	_, swapped := MaybeOutlineMarkdown("foo.md", content, false)
	if swapped {
		t.Error("unstructured markdown (no H2+) should pass through, not be outlined")
	}
}

func TestReadFile_AutoOutlinesLargeMarkdown(t *testing.T) {
	path := writeTempFile(t, "big.md", sampleLargeMarkdown())
	tool := readFileTool()
	out, err := tool.Run(context.Background(), map[string]interface{}{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "auto-outlined") {
		t.Errorf("read_file on large markdown should auto-outline; got: %s", out)
	}
}

func TestReadFile_ForceOverrideReturnsFullBody(t *testing.T) {
	path := writeTempFile(t, "big.md", sampleLargeMarkdown())
	tool := readFileTool()
	out, err := tool.Run(context.Background(), map[string]interface{}{"path": path, "force": true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "auto-outlined") {
		t.Error("force=true must return the raw body, not the outline")
	}
	if !strings.Contains(out, "filler word") {
		t.Error("force=true should include actual body content")
	}
}

func TestMarkdownOutlineTool_HappyPath(t *testing.T) {
	path := writeTempFile(t, "doc.md", sampleLargeMarkdown())
	tool := markdownOutlineTool()
	out, err := tool.Run(context.Background(), map[string]interface{}{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "§1 First chapter") {
		t.Errorf("outline missing §1; got: %s", out)
	}
}

func TestMarkdownSectionTool_FetchesBody(t *testing.T) {
	path := writeTempFile(t, "doc.md", sampleLargeMarkdown())
	tool := markdownSectionTool()
	out, err := tool.Run(context.Background(), map[string]interface{}{"path": path, "section_id": "2"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Second chapter") {
		t.Errorf("section §2 missing expected title; got: %s", out)
	}
	if !strings.Contains(out, "filler word") {
		t.Error("section should include the body content")
	}
	if strings.Contains(out, "First chapter") || strings.Contains(out, "Third chapter") {
		t.Error("section §2 should not bleed into §1 or §3")
	}
}

func TestMarkdownSectionTool_UnknownIDIsError(t *testing.T) {
	path := writeTempFile(t, "doc.md", sampleLargeMarkdown())
	tool := markdownSectionTool()
	_, err := tool.Run(context.Background(), map[string]interface{}{"path": path, "section_id": "999"})
	if err == nil {
		t.Fatal("expected error for unknown section id")
	}
	if !strings.Contains(err.Error(), "no chapter with id") {
		t.Errorf("error message should mention missing id; got: %v", err)
	}
}

func TestMarkdownValidateTool_ReportsPASS(t *testing.T) {
	path := writeTempFile(t, "doc.md", sampleLargeMarkdown())
	tool := markdownValidateTool()
	out, err := tool.Run(context.Background(), map[string]interface{}{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	// 3 chapters, each chapter ~4.5K tokens. Default cap is 5000. Should pass.
	if !strings.Contains(out, "PASS") {
		t.Errorf("expected PASS for well-formed markdown; got: %s", out)
	}
}

func TestMarkdownValidateTool_ReportsOversizeWithLowerCap(t *testing.T) {
	path := writeTempFile(t, "doc.md", sampleLargeMarkdown())
	tool := markdownValidateTool()
	out, err := tool.Run(context.Background(), map[string]interface{}{
		"path":               path,
		"max_chapter_tokens": float64(100), // very tight cap
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "FAIL") {
		t.Errorf("expected FAIL with tight cap; got: %s", out)
	}
	if !strings.Contains(out, "oversize") {
		t.Errorf("expected oversize code in issues; got: %s", out)
	}
}
