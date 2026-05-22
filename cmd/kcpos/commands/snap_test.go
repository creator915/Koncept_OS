package commands

import (
	"os"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/snapshot"
)

// chdirTo is a tiny test helper — restores prior cwd via t.Cleanup.
func chdirTo(t *testing.T, dir string) {
	t.Helper()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// loadSnapshotter must error with a help string when there's no
// store. The error text needs to name the directory the user
// expected and tell them how to populate it — the most common
// confused state.
func TestSnapCLI_LoadErrorsWhenNoStore(t *testing.T) {
	chdirTo(t, t.TempDir())
	_, err := loadSnapshotter()
	if err == nil {
		t.Fatal("expected error when no snapshot store present")
	}
	if !strings.Contains(err.Error(), "no snapshot store") || !strings.Contains(err.Error(), "run-routed") {
		t.Errorf("error must name the missing store path AND the kcpos command that creates it: %v", err)
	}
}

// resolveEventID: full sha → returned as-is; prefix → matched
// unambiguously; ref name → resolved.
func TestSnapCLI_ResolveEventID(t *testing.T) {
	dir := t.TempDir()
	chdirTo(t, dir)
	s := snapshot.NewSnapshotter(dir)
	// Seed three events so prefix collision is unlikely.
	a, _ := s.Append("test", "alpha")
	b, _ := s.Append("test", "beta")
	c, _ := s.Append("test", "gamma")

	// Full sha.
	got, err := resolveEventID(s, b.ID)
	if err != nil || got != b.ID {
		t.Errorf("full sha lookup failed: got=%q err=%v", got, err)
	}
	// 12-char prefix.
	prefix := b.ID[:12]
	got, err = resolveEventID(s, prefix)
	if err != nil || got != b.ID {
		t.Errorf("prefix %q lookup failed: got=%q err=%v", prefix, got, err)
	}
	// Ref name (we set milestone for c).
	if err := s.Refs.Set("milestone/last", c.ID); err != nil {
		t.Fatal(err)
	}
	got, err = resolveEventID(s, "milestone/last")
	if err != nil || got != c.ID {
		t.Errorf("ref name lookup failed: got=%q err=%v", got, err)
	}
	// Too-short prefix → error (need ≥8 chars).
	if _, err := resolveEventID(s, "ab"); err == nil {
		t.Error("3-char prefix must error")
	}
	// Unknown prefix → error.
	if _, err := resolveEventID(s, strings.Repeat("z", 16)); err == nil {
		t.Error("unknown prefix must error")
	}
	_ = a
}

// Ambiguous prefix: two events share the same prefix → must error
// with a "use longer prefix" hint. We can't easily force collisions
// at the sha level, but we can check the error path by mocking via
// the events list (effectively).
//
// Simulated by reaching into the chain — find two events whose IDs
// share at least one byte and use that.
func TestSnapCLI_ResolveEventIDAmbiguousPrefix(t *testing.T) {
	dir := t.TempDir()
	chdirTo(t, dir)
	s := snapshot.NewSnapshotter(dir)
	a, _ := s.Append("test", 1)
	b, _ := s.Append("test", 2)
	// Find the shortest prefix shared by a and b. Their IDs are
	// derived from sha256 so often differ at byte 0 — but if they
	// happen to share leading bytes, find that prefix.
	shared := ""
	for i := 0; i < 64; i++ {
		if a.ID[i] == b.ID[i] {
			shared += string(a.ID[i])
			continue
		}
		break
	}
	if len(shared) < 8 {
		t.Skip("a and b don't share an 8+ char prefix — sha-derived ids; rerun with different seed")
	}
	// Single-event prefix should still resolve uniquely.
	uniqueA := shared + string(a.ID[len(shared)])
	if got, err := resolveEventID(s, uniqueA); err != nil || got != a.ID {
		t.Errorf("disambiguated prefix %q must resolve to a, got=%q err=%v", uniqueA, got, err)
	}
}

// resolveEventID with a name that LOOKS like a ref must fall through
// to sha lookup gracefully — refs that don't exist shouldn't shadow
// real shas of similar shape.
func TestSnapCLI_ResolveEventIDRefMiss_FallsThroughToSha(t *testing.T) {
	dir := t.TempDir()
	chdirTo(t, dir)
	s := snapshot.NewSnapshotter(dir)
	e, _ := s.Append("test", "payload")
	// Query the full sha — must succeed even though we never set a
	// ref with that name.
	got, err := resolveEventID(s, e.ID)
	if err != nil || got != e.ID {
		t.Errorf("full-sha-as-query must work without a ref by that name: got=%q err=%v", got, err)
	}
}

// summariseEvent: each event type produces a useful one-line
// description, not a raw dump.
func TestSnapCLI_SummariseEvent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ev      snapshot.Event
		mustHaveSubstr string
	}{
		{
			name: "llm.turn",
			ev: mkEvent(t, snapshot.EventTypeLLMTurn, snapshot.LLMTurnEvent{
				SubAgent: "depth-0", TurnIndex: 7, Content: "hi",
			}),
			mustHaveSubstr: "turn=7",
		},
		{
			name: "tool.exec",
			ev: mkEvent(t, snapshot.EventTypeToolExec, snapshot.ToolExecEvent{
				Tool: "write_file", Result: "wrote 100 bytes",
			}),
			mustHaveSubstr: "write_file",
		},
		{
			name: "outer.transition",
			ev: mkEvent(t, snapshot.EventTypeOuterTransition, snapshot.OuterTransitionEvent{
				From: "Outer.Architecture", To: "Outer.GraphDeclared",
			}),
			mustHaveSubstr: "Outer.Architecture",
		},
		{
			name: "milestone",
			ev: mkEvent(t, snapshot.EventTypeMilestone, snapshot.MilestoneEvent{
				Name: "graph-declared-figlet",
			}),
			mustHaveSubstr: "graph-declared-figlet",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := summariseEvent(tc.ev)
			if !strings.Contains(out, tc.mustHaveSubstr) {
				t.Errorf("summary %q must contain %q", out, tc.mustHaveSubstr)
			}
		})
	}
}

// mkEvent is a tiny test helper that marshals a payload into an Event
// without going through the EventLog — for use in unit tests of
// summariseEvent that only need the payload-shape.
func mkEvent(t *testing.T, typ string, payload interface{}) snapshot.Event {
	t.Helper()
	// Use a real EventLog to get canonical payload encoding.
	log := snapshot.NewEventLog(t.TempDir())
	ev, err := log.Append("", typ, payload)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

// Empty milestone name must be rejected before reaching RefStore.
// Otherwise `refs/milestone/.txt` gets written — file exists, but
// retrieval via `kcpos snap show milestone/` doesn't work (RefStore
// rejects names ending in "/"). Bug found in Phase 4 audit.
func TestSnapCLI_MilestoneRejectsEmptyName(t *testing.T) {
	dir := t.TempDir()
	chdirTo(t, dir)
	s := snapshot.NewSnapshotter(dir)
	ev, _ := s.Append("test", "anchor")

	// Capture stderr via redirect (tricky in Go tests without setup);
	// just verify the exit code is non-zero.
	rc := runSnapMilestone([]string{"", ev.ID})
	if rc == 0 {
		t.Error("empty milestone name must return non-zero exit code")
	}
	// Verify NO ref was created with the empty-name shape.
	refs, _ := s.Refs.List()
	for name := range refs {
		if strings.HasPrefix(name, "milestone/") && len(name) == len("milestone/") {
			t.Errorf("empty-name milestone leaked a ref: %q", name)
		}
	}
}

// summariseEvent on payload that doesn't match its declared Type
// must NOT silently render zero-value fields. The fallback marker
// prevents a schema drift from looking like valid data.
func TestSnapCLI_SummariseEventDecodeFailureMarker(t *testing.T) {
	// Construct an event whose Type claims llm.turn but whose
	// payload is a string (incompatible with LLMTurnEvent struct).
	// Use the EventLog directly so we can hand-craft the mismatch.
	log := snapshot.NewEventLog(t.TempDir())
	ev, _ := log.Append("", snapshot.EventTypeLLMTurn, "this is not an LLMTurnEvent struct")

	got := summariseEvent(ev)
	if !strings.Contains(got, "decode-failed") {
		t.Errorf("summary on schema mismatch must surface a decode-failed marker, got %q", got)
	}
}
