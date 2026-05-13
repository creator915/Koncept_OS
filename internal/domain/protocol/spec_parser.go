// Package spec parses markdown specification documents into navigable
// chapters so agent tools can serve one chapter at a time. Pre-v9.0.4
// agents had to read_file the whole SPEC.md (often 1500+ lines / 28K+
// tokens) into their conversation context just to begin reasoning. The
// 2026-05-11 Terraria batch demonstrated this is structurally fragile:
// for a 3x bigger SPEC the read alone would overflow the LLM stream
// before any decomposition could start. Chapter-granular access lets
// the parent agent see only an outline (~1-2K tokens regardless of SPEC
// size) and each child agent see only the chapters it needs for its
// assigned object.
package protocol

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Chapter is one heading-bounded section of a markdown SPEC. Lines are
// 1-based, inclusive on both ends. Body is the markdown text from the
// line AFTER the heading to the line BEFORE the next heading at the
// same or higher level — heading text itself lives in Title.
type Chapter struct {
	ID       string // section identifier, e.g. "1", "3.2", "5.4.1"; "" when the heading has no numeric prefix
	Title    string // full heading text minus the leading "#"s and any numeric prefix
	Level    int    // markdown heading level (2 = ##, 3 = ###, etc.); H1 is treated as the doc preamble and not surfaced as a chapter
	StartLine int   // 1-based line of the heading
	EndLine   int   // 1-based line BEFORE the next sibling-or-higher heading (or last line of file)
	Body      string // chapter body, excluding the heading line itself
}

// ApproxTokens estimates the token count of Body using the heuristic
// 1 token ≈ 4 bytes (English) / 2 bytes (CJK heavy). The estimate is
// conservative-high so callers planning a context budget don't
// under-allocate. Exact tokenization is the LLM's job; this just
// helps the outline render warning labels on suspiciously large
// chapters.
func (c Chapter) ApproxTokens() int {
	if c.Body == "" {
		return 0
	}
	cjk := 0
	for _, r := range c.Body {
		if r > 0x4E00 && r < 0x9FFF { // CJK Unified Ideographs
			cjk++
		}
	}
	if cjk*2 > len(c.Body) {
		// CJK-heavy: ~2 bytes per token
		return len(c.Body) / 2
	}
	// Latin: ~4 bytes per token
	return len(c.Body) / 4
}

// headingRe matches markdown ATX headings at the start of a line.
// Captures: 1=hashes, 2=text-after-space.
var headingRe = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*$`)

// numericPrefixRe extracts a leading dotted-number prefix from a
// heading title (e.g. "3.2 Schema" → "3.2"; "Schema" → "").
var numericPrefixRe = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)*)\s*[.、]?\s+`)

// Parse splits a markdown document into chapters. Only H2..H6 are
// surfaced as chapters; H1 (typically the document title) is dropped
// since SPECs canonically have exactly one H1. Returns chapters in
// document order, plus the file-level preamble (everything before the
// first heading) as the second return value — useful when callers want
// to also expose top-of-document metadata.
func Parse(content string) (chapters []Chapter, preamble string) {
	lines := strings.Split(content, "\n")
	// Find heading positions (only H2+).
	type rawHeading struct {
		level     int
		lineIndex int // 0-based
		title     string
	}
	var headings []rawHeading
	for i, line := range lines {
		m := headingRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		level := len(m[1])
		if level < 2 {
			continue // ignore H1
		}
		headings = append(headings, rawHeading{level: level, lineIndex: i, title: strings.TrimSpace(m[2])})
	}
	if len(headings) == 0 {
		return nil, content
	}
	preamble = strings.Join(lines[:headings[0].lineIndex], "\n")

	for idx, h := range headings {
		// End line: line BEFORE the next heading of the SAME or HIGHER
		// level (smaller number) — meaning sub-headings are still part
		// of THIS chapter's body. This matches the natural "include
		// sub-sections" reading of section §X.
		endIdx := len(lines) - 1
		for j := idx + 1; j < len(headings); j++ {
			if headings[j].level <= h.level {
				endIdx = headings[j].lineIndex - 1
				break
			}
		}

		id, cleanTitle := splitIDAndTitle(h.title)
		bodyStart := h.lineIndex + 1
		body := ""
		if bodyStart <= endIdx {
			body = strings.Join(lines[bodyStart:endIdx+1], "\n")
		}
		chapters = append(chapters, Chapter{
			ID:        id,
			Title:     cleanTitle,
			Level:     h.level,
			StartLine: h.lineIndex + 1,
			EndLine:   endIdx + 1,
			Body:      body,
		})
	}
	return chapters, preamble
}

// ParseFile reads a markdown file and returns its parsed chapters. A
// thin convenience for tools that work from path arguments.
func ParseFile(path string) ([]Chapter, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", path, err)
	}
	chapters, preamble := Parse(string(raw))
	return chapters, preamble, nil
}

// Find returns the chapter whose ID matches sectionID. ID matching is
// case-insensitive on the title fallback. Returns nil + false when no
// chapter matches.
func Find(chapters []Chapter, sectionID string) (*Chapter, bool) {
	if sectionID == "" {
		return nil, false
	}
	q := strings.TrimSpace(sectionID)
	q = strings.TrimPrefix(q, "§")
	q = strings.TrimSpace(q)
	for i := range chapters {
		if chapters[i].ID == q {
			return &chapters[i], true
		}
	}
	// Fallback: case-insensitive title match.
	low := strings.ToLower(q)
	for i := range chapters {
		if strings.ToLower(chapters[i].Title) == low {
			return &chapters[i], true
		}
	}
	return nil, false
}

// splitIDAndTitle separates a heading text into its numeric prefix
// (the section ID) and the remaining title. "3.2 graph schema" →
// ("3.2", "graph schema"). When no numeric prefix is present, the ID
// is empty and the whole heading becomes the title.
func splitIDAndTitle(raw string) (id, title string) {
	if m := numericPrefixRe.FindStringSubmatchIndex(raw); m != nil {
		id = raw[m[2]:m[3]]
		title = strings.TrimSpace(raw[m[1]:])
		return id, title
	}
	return "", strings.TrimSpace(raw)
}

// ValidationIssue is one problem found by Validate.
type ValidationIssue struct {
	Code    string // short tag: "no-chapters", "duplicate-id", "oversize", "missing-id"
	Where   string // chapter id / title for context
	Message string // human-readable explanation
}

// ValidationConfig controls structural checks. Defaults aim at the
// kcpos use case (parent + child agents reading chapters) but callers
// can tune for different LLM budgets.
type ValidationConfig struct {
	// MaxChapterTokens is the hard cap on a single chapter's
	// approximate token count. v9.0.4 default 5000 — comfortably fits
	// a single chapter as one tool result in either parent or child
	// context, leaving room for surrounding tools/transcript.
	MaxChapterTokens int
	// MinChapters is the minimum count of H2+ chapters a SPEC must have
	// for chapter-granular access to be useful. Below this threshold
	// the whole file is small enough to read directly.
	MinChapters int
	// RequireIDs demands every chapter heading carry a numeric prefix
	// (e.g. "## 3. Foo" yes, "## Foo" no). Helps agents reference
	// chapters by stable id across SPEC edits.
	RequireIDs bool
}

// DefaultValidationConfig matches v9.0.4 protocol expectations.
func DefaultValidationConfig() ValidationConfig {
	return ValidationConfig{
		MaxChapterTokens: 5000,
		MinChapters:      2,
		RequireIDs:       true,
	}
}

// Validate runs structural checks on a parsed SPEC. Returns the issues
// list (empty when valid). A non-empty list means the SPEC isn't ready
// to use under the chapter-granular access model; the agent (or
// session_start hook) should refuse to proceed until the SPEC is
// restructured.
func Validate(chapters []Chapter, cfg ValidationConfig) []ValidationIssue {
	var out []ValidationIssue
	if len(chapters) < cfg.MinChapters {
		out = append(out, ValidationIssue{
			Code:    "no-chapters",
			Message: fmt.Sprintf("SPEC has %d chapters; chapter-granular access requires ≥%d (use H2+ markdown headings like `## 1. Foo` to delimit sections)", len(chapters), cfg.MinChapters),
		})
	}
	idSeen := map[string]string{}
	titleSeen := map[string]string{}
	for _, c := range chapters {
		if cfg.RequireIDs && c.ID == "" {
			out = append(out, ValidationIssue{
				Code:    "missing-id",
				Where:   c.Title,
				Message: fmt.Sprintf("chapter %q has no numeric prefix — required for stable cross-edit references. Add e.g. \"## N. %s\".", c.Title, c.Title),
			})
		}
		if c.ID != "" {
			if prior, dup := idSeen[c.ID]; dup {
				out = append(out, ValidationIssue{
					Code:    "duplicate-id",
					Where:   c.ID,
					Message: fmt.Sprintf("chapter id %q used twice (prior heading: %q, this heading: %q) — IDs must be unique", c.ID, prior, c.Title),
				})
			} else {
				idSeen[c.ID] = c.Title
			}
		}
		titleKey := strings.ToLower(c.Title)
		if prior, dup := titleSeen[titleKey]; dup {
			out = append(out, ValidationIssue{
				Code:    "duplicate-title",
				Where:   c.Title,
				Message: fmt.Sprintf("chapter title %q used twice (prior id %q) — agents reference by title fallback when id missing", c.Title, prior),
			})
		} else {
			titleSeen[titleKey] = c.ID
		}
		if tokens := c.ApproxTokens(); cfg.MaxChapterTokens > 0 && tokens > cfg.MaxChapterTokens {
			out = append(out, ValidationIssue{
				Code:    "oversize",
				Where:   chapterRef(c),
				Message: fmt.Sprintf("chapter ~%d tokens > cap %d — split into sub-chapters (H3 inside this H2 becomes its own chapter)", tokens, cfg.MaxChapterTokens),
			})
		}
	}
	return out
}

// chapterRef formats a chapter as "id: title" for issue messages.
func chapterRef(c Chapter) string {
	if c.ID != "" {
		return c.ID + ": " + c.Title
	}
	return c.Title
}
