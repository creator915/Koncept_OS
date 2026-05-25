package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/creator915/Koncept_OS/internal/router"
	"github.com/creator915/Koncept_OS/internal/snapshot"
)

// RunRoutedTurnWithRetry is the Phase 7 wrapper around
// RunRoutedTurnWithPersist that automates the "obstacle → rollback
// → synthesize lesson → retry" loop. Each attempt:
//
//  1. Runs the outer Router to a terminal state.
//  2. If Outer.Finished — done, return success.
//  3. If Outer.Obstacle AND retry budget remains AND a milestone
//     is available to roll back to — rollback to the latest
//     milestone, synthesize a lesson from the failed branch's
//     events, write it to disk, and start a new attempt. The next
//     attempt's H_architect reads the lesson and prepends it to
//     its system prompt.
//  4. Otherwise — return the obstacle to the caller (final
//     failure).
//
// snapshotter MUST be non-nil and attached to ctx (snapshot.
// FromContext(ctx) returns it) for milestone tracking and rollback
// to function. Pass via snapshot.WithSnapshotter at the top-level
// caller (typically commands/run_routed.go).
//
// maxAttempts caps total tries including the first; 0 → uses
// DefaultMaxAttempts.
func RunRoutedTurnWithRetry(
	ctx context.Context,
	deps *OuterDeps,
	persist TracePersistor,
	snap *snapshot.Snapshotter,
	maxAttempts int,
) (RoutedRunResult, error) {
	if snap == nil {
		// No snapshotter — degrade to a single attempt. The Phase 7
		// retry workflow requires snapshot state to roll back, so
		// silently bypassing the wrapper would just produce a
		// confusing "0 attempts retried" log. Run the single attempt
		// as-is.
		return RunRoutedTurnWithPersist(ctx, deps, persist)
	}
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}

	var lastResult RoutedRunResult
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Honour ctx cancellation BETWEEN attempts. The inner
		// RunRoutedTurnWithPersist also checks ctx mid-step, but
		// without this guard a user SIGINT after attempt N's
		// terminal would still trigger attempt N+1.
		if err := ctx.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "[retry] context cancelled before attempt %d: %v\n", attempt, err)
			return lastResult, nil
		}
		if attempt > 1 {
			fmt.Fprintf(os.Stderr, "[retry] attempt %d/%d — using lessons from %d prior failure(s)\n",
				attempt, maxAttempts, attempt-1)
		}
		result, err := RunRoutedTurnWithPersist(ctx, deps, persist)
		if err != nil {
			return result, err
		}
		lastResult = result
		if result.TerminalType == router.OuterTypeFinished {
			return result, nil
		}
		// Terminal was NOT Finished. Decide whether to retry.
		if attempt >= maxAttempts {
			summary := resultPhaseSummary(result)
			if summary == "" {
				summary = result.TerminalType
			}
			fmt.Fprintf(os.Stderr, "[retry] giving up after %d attempts (last: %s)\n",
				maxAttempts, summary)
			return result, nil
		}
		// Find a rollback target. Preference order (most-recent
		// real work first): checkpointed > built > aggregated >
		// all-confirmed > graph-declared > architecture. If none
		// of these milestones exist (very early failure), retry
		// is meaningless — bail.
		target, mname := pickRollbackMilestone(snap, deps.RootSessionID)
		if target == "" {
			fmt.Fprintf(os.Stderr, "[retry] no milestone reached on attempt %d — refusing to roll back to genesis (would just rerun the same trajectory)\n", attempt)
			return result, nil
		}
		// Rollback.
		rb, err := snap.Rollback(target, fmt.Sprintf("auto-attempt-%d", attempt))
		if err != nil {
			fmt.Fprintf(os.Stderr, "[retry] rollback to %s failed: %v — returning prior result\n", mname, err)
			return result, nil
		}
		fmt.Fprintf(os.Stderr, "[retry] rolled back to milestone %s (event %s); archived branch %s with %d events\n",
			mname, target[:16], rb.ArchivedBranchRef, rb.EventsArchived)

		// Synthesize lesson from archived branch. Phase 7 ships
		// with heuristic-only; LLM-driven synthesis can be added
		// by passing a snapshot.LessonSynthesizer here.
		lesson, err := snap.SynthesizeLesson(rb.ArchivedBranchRef, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[retry] lesson synthesis failed: %v — continuing without it\n", err)
		} else if err := snap.WriteLesson(lesson); err != nil {
			fmt.Fprintf(os.Stderr, "[retry] write lesson failed: %v — continuing without it\n", err)
		} else {
			short := lesson.Failure
			if len(short) > 80 {
				short = short[:80] + "..."
			}
			fmt.Fprintf(os.Stderr, "[retry] lesson written (%s): %s\n", lesson.GeneratedBy, short)
		}
	}
	return lastResult, nil
}

// DefaultMaxAttempts caps retry-budget at 3. Past this, the
// odds of a different outcome on the next attempt drop sharply —
// repeated rollback to the same milestone with the same lessons
// often produces correlated failures (the agent reads the lesson
// but the same blocker persists).
const DefaultMaxAttempts = 3

// rollbackMilestonePriority is the order pickRollbackMilestone uses
// when choosing a rollback target. Most-work-invested last → first
// in the list, so we preserve as much agent effort as possible.
//
// "gate-passed" is intentionally EXCLUDED from this priority. Its
// successor is Outer.Finished; rolling back to gate-passed and
// retrying would just re-run finish — same outcome as the last
// attempt. The exclusion keeps the retry loop from spinning on
// terminal-stage failures.
//
// Order MUST be derivable from the set returned by milestoneNameFor:
// the table below filters it through the priority, so a new
// milestone added to milestoneNameFor without being added here would
// be ignored by the retry loop. Audit reviewers: when extending
// milestoneNameFor, add the new name here too (or argue why it
// shouldn't be a rollback target).
var rollbackMilestonePriority = []string{
	"checkpointed",
	"built",
	"aggregated",
	"all-confirmed",
	"graph-declared",
	"architecture",
}

// pickRollbackMilestone returns the latest milestone ref event id
// suitable for rollback, plus the milestone name. Returns ("","")
// when no milestone exists for this session (very early failure;
// retrying would just rerun the same trajectory).
func pickRollbackMilestone(snap *snapshot.Snapshotter, sessionID string) (eventID, name string) {
	for _, p := range rollbackMilestonePriority {
		key := "milestone/" + p + "-" + sessionID
		if id, err := snap.Refs.Get(key); err == nil {
			return id, p
		}
	}
	return "", ""
}

// resultPhaseSummary extracts the obstacle phase + first reason
// from a result for one-line logging. Helper for the retry loop
// banner; returns "" when result wasn't an Obstacle.
func resultPhaseSummary(result RoutedRunResult) string {
	if result.TerminalType != router.OuterTypeObstacle {
		return ""
	}
	var p struct {
		Phase   string   `json:"phase"`
		Reasons []string `json:"reasons"`
	}
	if err := json.Unmarshal(result.TerminalPayload, &p); err != nil {
		return ""
	}
	if len(p.Reasons) == 0 {
		return p.Phase
	}
	r := p.Reasons[0]
	if len(r) > 120 {
		r = r[:120] + "..."
	}
	return p.Phase + ": " + strings.TrimSpace(r)
}
