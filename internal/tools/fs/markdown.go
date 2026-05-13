package fs

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
	"github.com/creator915/Koncept_OS/internal/domain/protocol"
)

// MarkdownInlineThresholdTokens is the size above which read_file
// stops returning the full markdown body inline and instead returns
// the parsed outline plus instructions to use markdown_section. Set
// just below the median LLM context-window budget per tool result so
// even back-to-back inline reads of medium files don't blow up the
// transcript.
const MarkdownInlineThresholdTokens = 5000

// markdownOutlineTool returns a navigable outline of any markdown
// file's H2+ chapters. Agents call this to get a chapter-id list
// (~1-2K tokens regardless of source size) then fetch individual
// chapters via markdown_section. Generalizes the v9.0.4 chapter-
// granular access mechanism to any long markdown — not just SPEC.md.
func markdownOutlineTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: true,
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "markdown_outline",
				Description: "Return the chapter outline (H2+ headings) of a markdown file: each chapter's id (numeric prefix), title, line range, and approximate token count. Use this BEFORE reading large markdown documents so you can fetch chapters individually via markdown_section instead of paying the full-document cost on every turn. Works on any markdown path — SPEC.md, DESIGN.md, README.md, third-party docs, etc.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{"type": "string", "description": "Markdown file path."},
					},
					"required": []string{"path"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			path, _ := args["path"].(string)
			if path == "" {
				return "", fmt.Errorf("path required")
			}
			chapters, _, err := protocol.ParseFile(path)
			if err != nil {
				return "", err
			}
			if len(chapters) == 0 {
				return fmt.Sprintf("markdown_outline %s: no H2+ chapters found. The file may be unstructured or shorter than expected; consider read_file instead.", path), nil
			}
			return renderOutline(path, chapters), nil
		},
	}
}

// markdownSectionTool returns one chapter's body by id.
func markdownSectionTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: true,
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "markdown_section",
				Description: "Read a single chapter (by its section id from markdown_outline, e.g. \"3.2\") from a markdown file. Returns only that chapter's body — heading included — keeping the result bounded regardless of the surrounding document's size. Use this in any agent (parent or child) that needs to reason about a specific section without paying the full document's context cost.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path":       map[string]interface{}{"type": "string", "description": "Markdown file path."},
						"section_id": map[string]interface{}{"type": "string", "description": "Section id from markdown_outline (e.g. \"3.2\", \"5.4.1\"). The literal heading title also works as a fallback."},
					},
					"required": []string{"path", "section_id"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			path, _ := args["path"].(string)
			sectionID, _ := args["section_id"].(string)
			if path == "" || sectionID == "" {
				return "", fmt.Errorf("path and section_id are required")
			}
			chapters, _, err := protocol.ParseFile(path)
			if err != nil {
				return "", err
			}
			ch, ok := protocol.Find(chapters, sectionID)
			if !ok {
				return "", fmt.Errorf("markdown_section %s: no chapter with id %q. Use markdown_outline %s to list available ids.", path, sectionID, path)
			}
			return renderSection(*ch), nil
		},
	}
}

// markdownValidateTool runs structural checks on a markdown file. Used
// at session_start (or any time the SPEC structure changes) so the
// agent can decide whether chapter-granular access is viable, or
// whether the document must be restructured first.
func markdownValidateTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: true,
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "markdown_validate",
				Description: "Check that a markdown file is well-structured for chapter-granular access: every chapter has a numeric prefix id, ids are unique, no single chapter exceeds the per-chapter token cap (default 5000). Returns PASS or a list of issues. Run this on long SPEC/DESIGN docs before path-B decomposition so child agents won't hit oversize chapters.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path":                 map[string]interface{}{"type": "string", "description": "Markdown file path."},
						"max_chapter_tokens":   map[string]interface{}{"type": "integer", "description": "Override the per-chapter token cap (default 5000)."},
					},
					"required": []string{"path"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			path, _ := args["path"].(string)
			if path == "" {
				return "", fmt.Errorf("path required")
			}
			chapters, _, err := protocol.ParseFile(path)
			if err != nil {
				return "", err
			}
			cfg := protocol.DefaultValidationConfig()
			if v, ok := args["max_chapter_tokens"].(float64); ok && v > 0 {
				cfg.MaxChapterTokens = int(v)
			}
			issues := protocol.Validate(chapters, cfg)
			if len(issues) == 0 {
				return fmt.Sprintf("markdown_validate %s: PASS (%d chapters, all under %d tokens, ids unique)", path, len(chapters), cfg.MaxChapterTokens), nil
			}
			var b strings.Builder
			fmt.Fprintf(&b, "markdown_validate %s: FAIL (%d issues)\n", path, len(issues))
			for _, iss := range issues {
				fmt.Fprintf(&b, "  [%s] %s: %s\n", iss.Code, iss.Where, iss.Message)
			}
			return b.String(), nil
		},
	}
}

// renderOutline formats parsed chapters as the outline text shown to
// the agent. Includes a header banner so future read_file interceptions
// can reuse the same format.
func renderOutline(path string, chapters []protocol.Chapter) string {
	var b strings.Builder
	totalTokens := 0
	for _, c := range chapters {
		totalTokens += c.ApproxTokens()
	}
	fmt.Fprintf(&b, "markdown_outline %s — %d chapters, ~%d tokens total\n\n", path, len(chapters), totalTokens)
	// Group by H2 → H3 nesting for readability. Each H2 is its own
	// top-level entry; deeper headings indent one space per level.
	for _, c := range chapters {
		indent := strings.Repeat("  ", c.Level-2)
		id := c.ID
		if id == "" {
			id = "—"
		}
		fmt.Fprintf(&b, "%s§%s %s (lines %d-%d, ~%d tokens)\n", indent, id, c.Title, c.StartLine, c.EndLine, c.ApproxTokens())
	}
	b.WriteString("\nFetch a chapter with markdown_section path=" + path + " section_id=<id>.")
	return b.String()
}

// renderSection returns a chapter body wrapped with a heading line so
// the agent has the same context cue it would see in the source.
func renderSection(c protocol.Chapter) string {
	hashes := strings.Repeat("#", c.Level)
	header := hashes + " "
	if c.ID != "" {
		header += c.ID + " "
	}
	header += c.Title
	return fmt.Sprintf("%s\n\n%s\n\n---\n(source lines %d-%d, ~%d tokens)\n", header, c.Body, c.StartLine, c.EndLine, c.ApproxTokens())
}

// sortedIDs returns chapter ids in document order — used by tests.
func sortedIDs(chapters []protocol.Chapter) []string {
	ids := make([]string, 0, len(chapters))
	for _, c := range chapters {
		ids = append(ids, c.ID)
	}
	sort.Strings(ids)
	return ids
}

// MaybeOutlineMarkdown is the helper read_file uses to decide whether
// to return the full body or an outline. It returns (outline, true)
// when the file is markdown AND its parsed chapters' total approx
// tokens exceed threshold AND the agent didn't request --force.
//
// Returning ("", false) tells read_file to proceed with the default
// full-body return. Returning ("...", true) instructs read_file to
// substitute the outline instead.
func MaybeOutlineMarkdown(path string, content []byte, force bool) (string, bool) {
	if force {
		return "", false
	}
	if !looksLikeMarkdownPath(path) {
		return "", false
	}
	// Cheap byte-size heuristic before we pay for parsing: ~4 bytes per
	// token Latin / ~2 bytes per token CJK heavy; skip when both estimates
	// fall below threshold.
	if len(content) < MarkdownInlineThresholdTokens*2 {
		return "", false
	}
	chapters, _ := protocol.Parse(string(content))
	if len(chapters) < 2 {
		// Not chapter-structured enough to be useful as an outline; fall
		// through to inline read (and let the agent live with the cost).
		return "", false
	}
	totalTokens := 0
	for _, c := range chapters {
		totalTokens += c.ApproxTokens()
	}
	if totalTokens <= MarkdownInlineThresholdTokens {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "(read_file auto-outlined — %s is %d tokens > %d threshold)\n\n", path, totalTokens, MarkdownInlineThresholdTokens)
	b.WriteString(renderOutline(path, chapters))
	b.WriteString("\n\nTo force-read the whole file anyway, call read_file with force=true (NOT recommended for >5K-token markdown — see protocol anti-pattern \"long-markdown-bulk-read\").")
	return b.String(), true
}

func looksLikeMarkdownPath(p string) bool {
	if p == "" {
		return false
	}
	lower := strings.ToLower(p)
	return strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".markdown")
}

// Reference: keep sortedIDs reachable for the unused-detector since it
// is currently only used by tests. (Tests may live in _test.go files
// that the production build doesn't see.)
var _ = sortedIDs
