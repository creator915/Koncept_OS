package snapshot

import (
	"encoding/json"
	"fmt"
)

// SideEffect is the unit of state mutation that replay applies. Every
// state-mutating tool call records its side effects so the event log
// is sufficient on its own to rebuild workdir state — no need to
// re-invoke the LLM, probe the reference, or call gcc.
//
// Discriminated union (Kind field). Concrete payloads use specific
// types (FileWrite, GraphMutation, ExecutableReplace) marshalled into
// json.RawMessage so the SideEffect envelope stays stable while
// payload schemas can evolve per-kind.
//
// Naming convention: <subsystem>.<verb>. New side-effect kinds add a
// constant + a concrete type + a switch arm in (eventually) replay.go.
// Replay sees an unknown Kind → error (fail-closed) so adding a kind
// without updating replay can't silently produce a divergent rebuild.
type SideEffect struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// Side-effect kinds. Kept as string constants (not iota) so an event
// log written by one binary version is readable by a later version
// even if the order of kind declarations changes.
const (
	// SideKindFileWrite — a file was written/overwritten in workdir.
	// Replay = write the blob content back to the same path.
	SideKindFileWrite = "file.write"

	// SideKindFileDelete — a file was deleted. Replay = delete the
	// path (idempotent). Used when a refactor removes obsolete impls.
	SideKindFileDelete = "file.delete"

	// SideKindGraphMutation — a graph_* tool produced a structured
	// change (object created, attribute linked, status changed via
	// MergeObject, etc.). Replay re-applies the same mutation against
	// the persistence-layer Graph API. We record the OPERATION (not a
	// before/after diff) because the engine guarantees that the same
	// op against the same prior state produces the same posterior.
	SideKindGraphMutation = "graph.mutation"

	// SideKindBundleUpdate — a section of .kcpos/typecalc/<id>.json was
	// rewritten by compile / describe / synthesize / test / review /
	// equiv_oracle. We store the NEW section content as a blob plus
	// the targeted section name — replay writes the section into the
	// bundle preserving other sections. (Storing the FULL bundle each
	// time would inflate the blob pool with mostly-duplicate JSON.)
	SideKindBundleUpdate = "bundle.update"

	// SideKindExecutableReplace — the agent's `compile` tool
	// docker-cp'd a fresh binary into /workspace/executable. Replay =
	// docker cp blob → /workspace/executable in the live container.
	// Failing to replay this kind is fatal: probe vs run_local
	// comparisons depend on it.
	SideKindExecutableReplace = "container.executable_replace"

	// SideKindSessionMutation — H_architect / H_aggregate /
	// H_checkpoint / H_finish wrote to K/sessions/<sid>.json. Same
	// section-replacement pattern as bundle.update.
	SideKindSessionMutation = "session.mutation"
)

// FileWrite is the payload for SideKindFileWrite.
//
// Path is workdir-relative (we never store absolute paths — the
// replay target may be a different machine / different prefix).
// Content lives in the blob pool, referenced by ContentSha.
// Mode is recorded so replay restores executable bit on compile.sh
// and similar.
type FileWrite struct {
	Path       string `json:"path"`
	ContentSha string `json:"contentSha"`
	Mode       uint32 `json:"mode"` // os.FileMode bits; replay sets via chmod
}

// FileDelete payload — just the path. Idempotent on replay (deleting
// a non-existent file is not an error during replay).
type FileDelete struct {
	Path string `json:"path"`
}

// GraphMutation records ONE graph_* operation. Op is the tool name
// (e.g. "graph_create_object", "graph_merge_object",
// "graph_link_produce"). Args is the raw tool input. Replay re-calls
// the tool with the same args.
//
// We intentionally store the OP + ARGS (not a graph snapshot before/
// after) because the persistence layer's MergeObject enforces
// invariants — replay must go through the same validation path so a
// snapshot from an OLD bin replayed under a NEW bin's validation
// rules behaves as the new bin would on a fresh run (catches drift
// like the 2026-05-21 implLang whitelist removal).
type GraphMutation struct {
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args"`
}

// BundleUpdate records one section-level rewrite of a per-object
// evidence bundle.
type BundleUpdate struct {
	ObjectID    string `json:"objectId"`
	Section     string `json:"section"`    // "spec" | "compile" | "test" | "accepted" | "cycles" | "characterization" | "runtimeTrace"
	ContentSha  string `json:"contentSha"` // sha of the new section's JSON
	SourceHash  string `json:"sourceHash,omitempty"` // optional: source hash at the time of this update (for drift correlation)
}

// ExecutableReplace records a compile-step replacement of
// /workspace/executable inside the linux/amd64 container.
//
// BinarySha references the new executable's bytes in the blob pool.
// Container is the docker container name as known at capture time;
// replay must remap this to the current container (a session can
// be replayed against a fresh container with the same image).
type ExecutableReplace struct {
	Container  string `json:"container"`
	TargetPath string `json:"targetPath"` // typically "/workspace/executable"
	BinarySha  string `json:"binarySha"`
	Mode       uint32 `json:"mode"`
}

// SessionMutation records one session.Output field rewrite.
type SessionMutation struct {
	SessionID  string `json:"sessionId"`
	Field      string `json:"field"`      // "architecture" | "implementations" | ...
	ContentSha string `json:"contentSha"` // serialized new value
}

// NewSideEffect wraps a concrete payload into a SideEffect envelope.
// Returns an error if the payload doesn't marshal (would only happen
// on unsupported types — channels, funcs).
func NewSideEffect(kind string, payload interface{}) (SideEffect, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return SideEffect{}, fmt.Errorf("snapshot: marshal side-effect payload for kind %q: %w", kind, err)
	}
	return SideEffect{Kind: kind, Payload: raw}, nil
}

// Decode unmarshals the side-effect's payload into the concrete type
// matching its Kind. Caller passes a pointer to the expected type;
// type-Kind mismatch returns an error (NOT a silent zero value).
func (se SideEffect) Decode(into interface{}) error {
	return json.Unmarshal(se.Payload, into)
}

// --- standard event types that carry side effects ---

// LLMTurnEvent payload — the assistant's response on one LLM turn.
//
// SLIM by design: only the NEW information at this turn is captured.
// Full message history is NOT included (would grow quadratically:
// turn N stores 0..N messages, totaling O(N²)). The transcripts/
// directory still holds the complete chat record; LLMTurnEvent is
// the per-turn delta the event chain needs.
//
// LLM is observation (not state mutation) — no side_effects field.
// State changes from this turn flow through subsequent ToolExec
// events the assistant triggered via ToolCalls.
type LLMTurnEvent struct {
	SubAgent  string          `json:"subAgent,omitempty"`  // identifier for which loop ran ("depth-0" main, "H_confirm_one" etc)
	TurnIndex int             `json:"turnIndex"`
	Reasoning string          `json:"reasoning,omitempty"` // assistant thinking block (may be empty)
	Content   string          `json:"content,omitempty"`   // assistant reply text (may be empty when tool_calls only)
	ToolCalls json.RawMessage `json:"toolCalls,omitempty"` // raw tool_calls from the assistant message
}

// ToolExecEvent payload — one tool invocation by the LLM, including
// any side effects it produced. Replay applies SideEffects in order
// (each tool's side effects are committed atomically per tool call).
type ToolExecEvent struct {
	Tool        string          `json:"tool"`
	Args        json.RawMessage `json:"args"`
	Result      string          `json:"result,omitempty"`
	Err         string          `json:"err,omitempty"`
	SideEffects []SideEffect    `json:"sideEffects,omitempty"`
}

// OuterTransitionEvent payload — the Outer Router moved from one
// type-flow state to another. Recorded for forensic / replay-style
// navigation; the actual graph/session state changes are captured as
// GraphMutation / SessionMutation side effects under ToolExec events.
type OuterTransitionEvent struct {
	From    string          `json:"from"`
	To      string          `json:"to"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// MilestoneEvent payload — an explicit named checkpoint. Recorded
// at Outer.* state transitions automatically (Phase 2) and on user
// `kcpos snap milestone` calls.
type MilestoneEvent struct {
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"`
}

// Standard event types (constants for the Event.Type field).
const (
	EventTypeLLMTurn          = "llm.turn"
	EventTypeToolExec         = "tool.exec"
	EventTypeOuterTransition  = "outer.transition"
	EventTypeMilestone        = "milestone"
	EventTypeBranchHead       = "branch.head"        // marks a rollback branch's tip
	EventTypeLessonSynthesized = "lesson.synthesized" // Phase 6
)
