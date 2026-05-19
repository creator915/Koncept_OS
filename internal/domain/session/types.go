// Package session models work-sessions: tracked units of design /
// implementation work over the hypergraph. A session has lifecycle states
// (waiting → active → finished), parent/child relations forming a tree, and
// a graphDiff field tracking the structural changes the session made
// (used for rollback).
//
// This is distinct from internal/transcript, which persists the user-agent
// chat conversation.
package session

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type Status string

const (
	StatusWaiting  Status = "waiting"
	StatusActive   Status = "active"
	StatusFinished Status = "finished"
)

// Session is one unit of tracked work over the hypergraph.
type Session struct {
	ID        string    `json:"id"`
	Parent    string    `json:"parent,omitempty"`
	Children  []string  `json:"children"`
	Status    Status    `json:"status"`
	Task      string    `json:"task"`
	// ExpandsObject (KonceptOS_implementation_plan.md §1.3) names the
	// top-graph object this session is the expansion of. Set at
	// session-start; on session-finish the parent object's
	// Expansion=<this session id> + status=confirmed; on rollback it is
	// cleared. Empty for a plain (non-expansion) session — backward
	// compatible with every pre-layered session.
	ExpandsObject string    `json:"expandsObject,omitempty"`
	Input         Input     `json:"input"`
	Output        Output    `json:"output"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Input struct {
	Signatures []string `json:"signatures"`
	Context    []string `json:"context"`
}

type Output struct {
	// Architecture (Fix 4 / KonceptOS_kcpos_analysis.md §6) is the
	// pre-implementation design artifact — a description of the
	// sub-modules and intermediate variables produced before any code
	// is written. CLAUDE.md §5.4 path A requires "even if a one-shot
	// implementation, first list sub-modules and intermediate variables".
	// The root finish gate enforces this: a root session cannot finish
	// while its Architecture is empty. Free-form string; the agent fills
	// it via session_set_architecture.
	Architecture    string    `json:"architecture"`
	Implementations []string  `json:"implementations"`
	NewSignatures   []string  `json:"newSignatures"`
	NewAttributes   []string  `json:"newAttributes"`
	Tests           []string  `json:"tests"`
	GraphDiff       GraphDiff `json:"graphDiff"`
}

// GraphDiff tracks the changes a session made to K/graph.json. Populated
// implicitly or explicitly during the session's active phase; used to
// reverse-apply on rollback.
//
// In the current slice this field is created empty and not auto-captured —
// future work will hook the graph_* tools to record diffs. The shape is
// already in place so JSON round-trips cleanly.
type GraphDiff struct {
	Added    GraphDiffAdded    `json:"added"`
	Modified GraphDiffModified `json:"modified"`
	Removed  GraphDiffRemoved  `json:"removed"`
}

type GraphDiffAdded struct {
	Attributes map[string]json.RawMessage `json:"attributes"`
	Objects    map[string]json.RawMessage `json:"objects"`
}

type GraphDiffModified struct {
	Attributes map[string]ModifiedRecord `json:"attributes"`
	Objects    map[string]ModifiedRecord `json:"objects"`
}

// ModifiedRecord pairs the pre- and post-change JSON of a single graph
// element. Both halves are kept verbatim so reverse-application is exact.
type ModifiedRecord struct {
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}

type GraphDiffRemoved struct {
	Attributes []string `json:"attributes"`
	Objects    []string `json:"objects"`
}

// emptyGraphDiff returns a fresh GraphDiff with all maps and slices
// initialized to non-nil empty values, so JSON renders as {} / [] not null.
func emptyGraphDiff() GraphDiff {
	return GraphDiff{
		Added: GraphDiffAdded{
			Attributes: map[string]json.RawMessage{},
			Objects:    map[string]json.RawMessage{},
		},
		Modified: GraphDiffModified{
			Attributes: map[string]ModifiedRecord{},
			Objects:    map[string]ModifiedRecord{},
		},
		Removed: GraphDiffRemoved{
			Attributes: []string{},
			Objects:    []string{},
		},
	}
}

// idPattern enforces session-id convention: "s_" + lowercase letter, then
// alphanumerics and underscores.
var idPattern = regexp.MustCompile(`^s_[a-z][a-z0-9_]*$`)

// ValidateID returns nil if id meets the s_<name> convention, error otherwise.
func ValidateID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("invalid session id %q (must match s_<lowercase_name>)", id)
	}
	return nil
}

// NormalizeID prepends "s_" to a name if not already present, then validates.
// Lets callers pass either "weather_proc" or "s_weather_proc".
func NormalizeID(name string) (string, error) {
	id := strings.TrimSpace(name)
	if id == "" {
		return "", fmt.Errorf("session id required")
	}
	if !strings.HasPrefix(id, "s_") {
		id = "s_" + id
	}
	if err := ValidateID(id); err != nil {
		return "", err
	}
	return id, nil
}

// New constructs a session in waiting state with empty children/output.
func New(id, parent, task string, input Input) *Session {
	now := time.Now().UTC()
	if input.Signatures == nil {
		input.Signatures = []string{}
	}
	if input.Context == nil {
		input.Context = []string{}
	}
	return &Session{
		ID:       id,
		Parent:   parent,
		Children: []string{},
		Status:   StatusWaiting,
		Task:     task,
		Input:    input,
		Output: Output{
			Implementations: []string{},
			NewSignatures:   []string{},
			NewAttributes:   []string{},
			Tests:           []string{},
			GraphDiff:       emptyGraphDiff(),
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// validTransitions defines the allowed status moves: sessions go strictly
// waiting → active → finished. Anything else (delete, rollback) is handled
// by removing the session entirely, not by status changes.
var validTransitions = map[Status]map[Status]bool{
	StatusWaiting:  {StatusActive: true},
	StatusActive:   {StatusFinished: true},
	StatusFinished: {},
}

// Transition moves status forward, with validation. Sets UpdatedAt.
func (s *Session) Transition(to Status) error {
	allowed, ok := validTransitions[s.Status]
	if !ok || !allowed[to] {
		return fmt.Errorf("invalid status transition %s → %s for session %s", s.Status, to, s.ID)
	}
	s.Status = to
	s.UpdatedAt = time.Now().UTC()
	return nil
}
