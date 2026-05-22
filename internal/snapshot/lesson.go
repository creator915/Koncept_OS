package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Lesson is a human- AND machine-readable record of one failed
// attempt. After Rollback archives a branch, SynthesizeLesson walks
// the branch's events and extracts:
//
//   - what went wrong (the terminal Obstacle reason)
//   - what the agent had tried (tool calls + state transitions)
//   - actionable advice for the next attempt (do / don't)
//
// The lesson is written to .kcpos/snapshots/lessons/<branch>.md as
// markdown with YAML frontmatter. The agent driver (Phase 7) reads
// these on next run-start and prepends them to the system prompt,
// so subsequent attempts know what to avoid.
//
// Two-tier synthesis:
//   - Heuristic: a pattern table over the Obstacle's `phase` +
//     `reasons` keywords. Fast, zero-token, deterministic. Covers
//     the failure modes PB-30 batches #1-#8 actually hit.
//   - LLM fallback (Phase 7 wires the real implementation): for
//     failures the heuristic doesn't match. Phase 6 ships with the
//     hook but no live LLM call.
type Lesson struct {
	BranchRef     string    `json:"branchRef"`     // e.g. "attempt/1"
	FailedAt      string    `json:"failedAt"`      // terminal event id
	Failure       string    `json:"failure"`       // one-line summary
	WhatWentWrong string    `json:"whatWentWrong"` // diagnostic prose
	DosAndDonts   []string  `json:"dosAndDonts"`   // actionable bullets for retry
	GeneratedBy   string    `json:"generatedBy"`   // "heuristic:<key>" or "llm:<model>"
	Timestamp     time.Time `json:"timestamp"`

	// Body holds the raw markdown content as it appears on disk
	// (populated by ReadLessons; empty on freshly-synthesized
	// in-memory lessons since Render() reconstructs it on demand).
	// Phase 7 injects this into the agent's system prompt
	// verbatim, so the structured fields above don't need to be
	// re-rendered from scratch.
	Body string `json:"-"`
}

// Render produces the on-disk markdown shape (frontmatter + body).
// Single source of truth — WriteLesson and CLI both call this. The
// format is stable per the Phase 6 spec: changes here MUST be
// reflected in parseLessonMarkdown's reverse pass.
func (l *Lesson) Render() string {
	var b strings.Builder
	// YAML-ish frontmatter (just enough that `cat` is readable and
	// a YAML parser could load it; we use a tiny manual parser on
	// the reverse path).
	b.WriteString("---\n")
	fmt.Fprintf(&b, "branchRef: %s\n", l.BranchRef)
	fmt.Fprintf(&b, "failedAt: %s\n", l.FailedAt)
	fmt.Fprintf(&b, "generatedBy: %s\n", l.GeneratedBy)
	fmt.Fprintf(&b, "timestamp: %s\n", l.Timestamp.Format(time.RFC3339))
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# Failure record — %s\n\n", l.BranchRef)
	fmt.Fprintf(&b, "**Failure**: %s\n\n", l.Failure)
	b.WriteString("## What went wrong\n\n")
	b.WriteString(l.WhatWentWrong)
	b.WriteString("\n\n")
	if len(l.DosAndDonts) > 0 {
		b.WriteString("## Do this on retry\n\n")
		for _, d := range l.DosAndDonts {
			b.WriteString("- " + d + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("---\n")
	fmt.Fprintf(&b, "*Generated %s by %s*\n", l.Timestamp.Format(time.RFC3339), l.GeneratedBy)
	return b.String()
}

// LessonSynthesizer is the LLM-call seam. Implementations receive
// the branch events (genesis → failed tip order) plus the heuristic
// summary (when one was found) and return prose for WhatWentWrong
// + bullets for DosAndDonts.
//
// Phase 6 ships a nil-default (LLM not called). Phase 7 wires
// transport.Client through a closure.
//
// Implementations should:
//   - be bounded in token use (truncate events if needed)
//   - return ("", "", nil) when they have nothing useful to add (so
//     the heuristic result stays primary)
//   - return an error only on transport failure (caller decides
//     whether to fall back to heuristic-only)
type LessonSynthesizer func(events []Event, heuristicSummary string) (whatWentWrong string, dosAndDonts []string, err error)

// SynthesizeLesson walks the events of an archived branch and
// produces a Lesson. The branch is identified by a ref name (e.g.
// "attempt/1") which must point at a real event.
//
// llm may be nil — heuristic-only mode. When llm is provided AND
// the heuristic produces a low-confidence match (or none), llm is
// called and its output augments the lesson.
func (s *Snapshotter) SynthesizeLesson(branchRef string, llm LessonSynthesizer) (*Lesson, error) {
	tipID, err := s.Refs.Get(branchRef)
	if err != nil {
		return nil, fmt.Errorf("lesson: read branch ref %q: %w", branchRef, err)
	}

	var events []Event
	if err := s.Events.WalkFrom(tipID, func(ev Event) error {
		events = append(events, ev)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("lesson: walk branch %s: %w", branchRef, err)
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("lesson: branch %s is empty", branchRef)
	}

	// Find the terminal Obstacle's reason (if any) by scanning
	// events in REVERSE — the failure is at the tail.
	obstacleReason, terminalType := extractTerminalObstacle(events)

	// Tier 1: heuristic. Pattern table over phase + reasons keywords.
	heuristicKey, heuristicMsg, heuristicDos := lessonHeuristic(obstacleReason)

	lesson := &Lesson{
		BranchRef:   branchRef,
		FailedAt:    tipID,
		Failure:     buildFailureSummary(terminalType, obstacleReason),
		Timestamp:   time.Now().UTC(),
		DosAndDonts: heuristicDos,
	}

	if heuristicKey != "" {
		lesson.WhatWentWrong = heuristicMsg
		lesson.GeneratedBy = "heuristic:" + heuristicKey
	} else {
		lesson.WhatWentWrong = "(heuristic did not match — see raw obstacle reason above; consider running with --llm to synthesize a custom diagnosis)"
		lesson.GeneratedBy = "heuristic:no-match"
	}

	// Tier 2: LLM fallback (when provided).
	if llm != nil {
		llmWWW, llmDos, lerr := llm(events, lesson.WhatWentWrong)
		if lerr != nil {
			// Don't fail — heuristic lesson is still valid output.
			// Surface the LLM error in the lesson so the operator
			// can decide whether to retry.
			lesson.WhatWentWrong += "\n\n*(LLM synthesis attempted but failed: " + lerr.Error() + ")*"
		} else if llmWWW != "" {
			lesson.WhatWentWrong = llmWWW
			if len(llmDos) > 0 {
				lesson.DosAndDonts = llmDos
			}
			lesson.GeneratedBy = "llm"
		}
	}
	return lesson, nil
}

// extractTerminalObstacle scans events backward to find the latest
// Outer.Obstacle transition and returns its reason payload.
// terminalType returns the To-state at the tail (typically
// "Outer.Obstacle" or "Outer.Finished" for happy-path branches —
// in which case obstacleReason is empty and the caller will
// short-circuit lesson synthesis).
func extractTerminalObstacle(events []Event) (reason string, terminalType string) {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Type != EventTypeOuterTransition {
			continue
		}
		var p OuterTransitionEvent
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			continue
		}
		// First transition we find is the latest.
		if terminalType == "" {
			terminalType = p.To
		}
		if p.To != "Outer.Obstacle" {
			// Branch ended in something OTHER than Obstacle —
			// no terminal reason to extract.
			return "", terminalType
		}
		// Decode obstacle payload to extract reasons.
		var ob struct {
			Phase   string   `json:"phase"`
			Reasons []string `json:"reasons"`
		}
		if err := json.Unmarshal(p.Payload, &ob); err == nil {
			parts := []string{"phase=" + ob.Phase}
			parts = append(parts, ob.Reasons...)
			return strings.Join(parts, " | "), terminalType
		}
		return string(p.Payload), terminalType
	}
	return "", ""
}

func buildFailureSummary(terminalType, reason string) string {
	if reason == "" {
		if terminalType == "" {
			return "(no terminal transition found in branch)"
		}
		return "branch ended at " + terminalType
	}
	short := reason
	if len(short) > 200 {
		short = short[:200] + "..."
	}
	return short
}

// WriteLesson persists a Lesson to .kcpos/snapshots/lessons/<branch>.md
// as markdown with a YAML-flavored frontmatter block. The branch
// component of the ref ("attempt/1" → "attempt-1.md") gets the
// slash replaced so a single directory holds all lessons.
func (s *Snapshotter) WriteLesson(lesson *Lesson) error {
	if lesson == nil || lesson.BranchRef == "" {
		return fmt.Errorf("write lesson: branchRef required")
	}
	lessonsDir := filepath.Join(s.workdir, ".kcpos", "snapshots", "lessons")
	if err := os.MkdirAll(lessonsDir, 0o755); err != nil {
		return err
	}
	fileName := strings.ReplaceAll(lesson.BranchRef, "/", "-") + ".md"
	body := lesson.Render()
	return os.WriteFile(filepath.Join(lessonsDir, fileName), []byte(body), 0o644)
}

// ReadLessons returns every lesson saved under the workdir's
// lessons/ directory, sorted by Timestamp ascending. Used by Phase
// 7 to inject prior failure context into the next attempt's
// system prompt — oldest first so the most recent advice lands
// last (the recency-effect-friendly position).
//
// Each returned Lesson has its Body field populated with the raw
// markdown content. Phase 7 injects that verbatim, so Phase 6's
// reverse parser only needs to recover metadata (branchRef,
// timestamp, generatedBy) for sort/dedup decisions — the
// rendering survives in Body.
//
// Returns nil with no error when no lessons exist yet (fresh
// project) — caller's prompt assembly treats nil as "no prior
// failures to learn from".
func (s *Snapshotter) ReadLessons() ([]*Lesson, error) {
	dir := filepath.Join(s.workdir, ".kcpos", "snapshots", "lessons")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read lessons dir: %w", err)
	}
	var out []*Lesson
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		l := parseLessonMarkdown(string(body))
		if l != nil {
			out = append(out, l)
		}
	}
	// Sort by timestamp ascending. Lessons with zero timestamp
	// (parse failed for that field) sort first — caller can
	// inspect them but they won't shadow newer well-formed entries.
	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp.Before(out[j].Timestamp)
	})
	return out, nil
}

// parseLessonMarkdown recovers the metadata from a lesson markdown
// file plus the RAW full-document body. Phase 7's prompt injection
// uses Body directly — the structured fields are kept on the
// Lesson struct only for sort/dedup decisions, not for
// re-rendering.
//
// Best-effort: returns nil if the document doesn't look like our
// format (missing or malformed frontmatter).
//
// Format assumption: Unix line endings (`\n`). Windows-style
// (`\r\n`) lesson files would fail this prefix check; kcpos is
// Unix-only by convention.
func parseLessonMarkdown(body string) *Lesson {
	if !strings.HasPrefix(body, "---\n") {
		return nil
	}
	rest := body[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return nil
	}
	frontmatter := rest[:end]
	l := &Lesson{Body: body}
	for _, line := range strings.Split(frontmatter, "\n") {
		kv := strings.SplitN(line, ":", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.TrimSpace(kv[0])
		v := strings.TrimSpace(kv[1])
		switch k {
		case "branchRef":
			l.BranchRef = v
		case "failedAt":
			l.FailedAt = v
		case "generatedBy":
			l.GeneratedBy = v
		case "timestamp":
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				l.Timestamp = t
			}
		}
	}
	if l.BranchRef == "" {
		return nil
	}
	return l
}
