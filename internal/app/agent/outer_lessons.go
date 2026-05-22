package agent

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/creator915/Koncept_OS/internal/snapshot"
)

// buildLessonPreamble reads any prior-attempt lessons saved under
// the workdir's .kcpos/snapshots/lessons/ directory and renders a
// preamble suitable for prepending to a sub-agent's system prompt.
//
// Empty string when no Snapshotter is attached, no lessons exist,
// or all lessons fail to read — callers concat unconditionally
// and get a zero-cost no-op in the common (first-attempt) case.
//
// PROMPT SIZE NOTE: Each lesson body is ~2-5KB of markdown. With
// max-attempts=3 and three accumulated failures, the preamble can
// add ~15KB to every LLM call inside H_architect (sys prompt is
// re-sent every turn). On DeepSeek pricing that's marginal, but
// future work could:
//   - dedupe lessons by heuristic key (no point showing two
//     "vacuous-oracle" lessons in a row)
//   - truncate each lesson's body to its DosAndDonts section (the
//     diagnostic prose is mostly redundant after the first attempt)
//   - only include the N most recent lessons rather than all
//
// The deps parameter is currently unused — kept on the signature
// for future session-scoped lesson filtering (when one workdir
// hosts multiple parallel sessions).
//
// Output shape (so the agent can grep for it):
//
//	=== Prior attempts failed N time(s). Read these lessons before
//	    deciding strategy: ===
//
//	[attempt/1] <body of lesson 1>
//
//	[attempt/2] <body of lesson 2>
//
//	=== End of prior-attempt lessons. Apply the "Do this on retry"
//	    sections to avoid repeating these failures. ===
//
// Recency-order: lessons sorted by timestamp ascending so the most
// recent advice lands last (recency effect → highest weight).
func buildLessonPreamble(ctx context.Context, deps *OuterDeps) string {
	s := snapshot.FromContext(ctx)
	if s == nil {
		// 2026-05-22 (audit C1): the prior implementation fell back to a
		// cwd-based Snapshotter when ctx didn't carry one. Silently
		// reading from cwd is the wrong call — if the agent is invoked
		// with `cd somewhere && kcpos ...`, cwd's .kcpos/snapshots/
		// lessons/ is a DIFFERENT store than the real workdir's. The
		// preamble would silently misroute (no lessons → no retry
		// pressure → repeat the same failure).
		//
		// Surface the gap on stderr so it's diagnosable, and return
		// empty. The cost is unit tests that exercise H_architect must
		// attach a Snapshotter via snapshot.WithSnapshotter — which they
		// already do.
		fmt.Fprintln(os.Stderr, "[snapshot/lessons] no Snapshotter in ctx — lesson preamble suppressed (attach via snapshot.WithSnapshotter to enable)")
		return ""
	}
	if !s.IsEnabled() {
		return ""
	}
	lessons, err := s.ReadLessons()
	if err != nil || len(lessons) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "=== Prior attempts failed %d time(s). Read these lessons before deciding strategy: ===\n\n", len(lessons))
	for _, l := range lessons {
		fmt.Fprintf(&b, "[%s]\n%s\n\n", l.BranchRef, l.Body)
	}
	b.WriteString(`=== End of prior-attempt lessons. Apply the "Do this on retry" sections to avoid repeating these failures. ===`)
	return b.String()
}
