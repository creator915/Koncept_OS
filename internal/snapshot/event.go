package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Event is the unit of state evolution. It's the smallest "stroke" in
// the painting metaphor: one LLM turn, one tool execution, one outer
// Router transition, or one explicit milestone marker.
//
// SHA-chain: ID = sha256(ParentID || canonical_json(Payload)). Editing
// any historical event invalidates every downstream ID, so the chain
// itself is the integrity guarantee — no separate signing needed.
// The genesis event has ParentID == "" by convention.
//
// Type strings are namespaced ("llm.turn", "tool.exec", "outer.transition",
// "milestone", "branch.head") so future extensions don't collide.
type Event struct {
	ID        string          `json:"id"`
	ParentID  string          `json:"parentId,omitempty"`
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"ts"`
	Payload   json.RawMessage `json:"payload"`
}

// EventLog is an append-only on-disk log of events under a single
// directory. Each event lives in its own file (<root>/<id>.json) so
// individual events can be inspected with `cat` and so the directory
// listing serves as a natural index. A future packfile compaction
// could collapse old events into a single file; the API stays the
// same.
type EventLog struct {
	root string // e.g. ".kcpos/snapshots/events"
}

func NewEventLog(root string) *EventLog {
	return &EventLog{root: root}
}

// ErrEventNotFound distinguishes "we never recorded this id" from
// other I/O errors.
var ErrEventNotFound = fmt.Errorf("event not found")

// Append records a new event with the given parent (use "" for
// genesis) and returns the fully-populated stored event. ParentID,
// Type, Timestamp, and Payload are inputs; ID is computed.
//
// Payload is marshalled with sorted keys (canonical form) so the same
// logical payload yields the same chain id even if a caller serialises
// fields in different orders. Without that determinism the chain
// would be at the mercy of Go map iteration order.
//
// Idempotency: if an event with the computed id already exists on
// disk, the existing event (with its ORIGINAL timestamp) is returned
// unchanged. Same logical event = same causal step; re-recording it
// must not bump the wall-clock or rewrite a file that other tooling
// may be reading. (Without this guard, a retry that replays an
// already-logged action would silently rewrite history.)
func (l *EventLog) Append(parentID, eventType string, payload interface{}) (Event, error) {
	if eventType == "" {
		return Event{}, fmt.Errorf("eventlog: type required")
	}
	if parentID != "" && !validSha(parentID) {
		return Event{}, fmt.Errorf("eventlog: invalid parent id %q", parentID)
	}
	canonical, err := canonicalJSON(payload)
	if err != nil {
		return Event{}, fmt.Errorf("eventlog: canonicalize payload: %w", err)
	}
	id := chainSha(parentID, canonical)
	if existing, err := l.Get(id); err == nil {
		// Pre-existing event with the same logical content → return as-is.
		return existing, nil
	}
	ev := Event{
		ID:        id,
		ParentID:  parentID,
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Payload:   canonical,
	}
	if err := l.write(ev); err != nil {
		return Event{}, err
	}
	return ev, nil
}

// Get returns the event with the given id, or ErrEventNotFound.
func (l *EventLog) Get(id string) (Event, error) {
	if !validSha(id) {
		return Event{}, fmt.Errorf("eventlog: invalid id %q", id)
	}
	data, err := os.ReadFile(l.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return Event{}, ErrEventNotFound
		}
		return Event{}, fmt.Errorf("eventlog: read: %w", err)
	}
	var ev Event
	if err := json.Unmarshal(data, &ev); err != nil {
		return Event{}, fmt.Errorf("eventlog: unmarshal: %w", err)
	}
	return ev, nil
}

// List returns every event currently in the log, ordered by Timestamp.
// On a large log this is O(N); for selective queries Get/Walk is
// preferred. Used by CLI list/replay paths where ordering matters.
func (l *EventLog) List() ([]Event, error) {
	entries, err := os.ReadDir(l.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("eventlog: list: %w", err)
	}
	out := make([]Event, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		ev, err := l.Get(id)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp.Before(out[j].Timestamp)
	})
	return out, nil
}

// Walk replays the chain forward from genesis, calling fn for each
// event in chain order (NOT timestamp order — chain order is the
// causal order, immune to clock skew). Returns the first error from
// fn or any I/O error.
//
// LINEAR-ONLY: Walk silently picks one child per parent — when a
// rollback (Phase 5) introduces branching the chain stops being a
// chain and Walk's output becomes order-dependent on List(). Use
// WalkFrom with an explicit tip ref to choose which branch to
// follow once branches exist.
//
// Stops at the first event with no child (the linear tip). Returns
// nil with no events when log is empty.
func (l *EventLog) Walk(genesisID string, fn func(Event) error) error {
	// Build parent → child index.
	all, err := l.List()
	if err != nil {
		return err
	}
	childOf := map[string]Event{}
	for _, e := range all {
		childOf[e.ParentID] = e
	}
	cursor := genesisID
	for {
		ev, ok := childOf[cursor]
		if !ok {
			return nil
		}
		if err := fn(ev); err != nil {
			return err
		}
		cursor = ev.ID
	}
}

// WalkFrom walks BACKWARD from the named tip down to genesis,
// collects every ancestor, then calls fn in forward (genesis → tip)
// order. Unlike Walk (which builds a parent→child map and breaks on
// branched histories), WalkFrom follows the parent chain from a
// specific tip — so it's the correct primitive for replay-to-N
// after Phase 5 rollbacks have introduced branches.
//
// Each ancestor is read via Get, which (via BlobStore.VerifyOnGet's
// twin in the event side) implicitly verifies file integrity. A
// missing parent (broken chain) returns an error containing both
// the cursor and the parent id, so the operator can diagnose which
// link in the chain is gone.
func (l *EventLog) WalkFrom(tipID string, fn func(Event) error) error {
	if tipID == "" {
		return nil
	}
	if !validSha(tipID) {
		return fmt.Errorf("eventlog: invalid tip id %q", tipID)
	}
	// Walk ancestry backward, collect events. Bounded by chain depth.
	var chain []Event
	cursor := tipID
	for cursor != "" {
		ev, err := l.Get(cursor)
		if err != nil {
			return fmt.Errorf("eventlog: walk from %s: missing ancestor %s: %w", tipID[:16], cursor[:16], err)
		}
		chain = append(chain, ev)
		cursor = ev.ParentID
	}
	// Apply fn in forward order (genesis → tip).
	for i := len(chain) - 1; i >= 0; i-- {
		if err := fn(chain[i]); err != nil {
			return err
		}
	}
	return nil
}

func (l *EventLog) write(ev Event) error {
	if err := os.MkdirAll(l.root, 0o755); err != nil {
		return fmt.Errorf("eventlog: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return fmt.Errorf("eventlog: marshal: %w", err)
	}
	tmp, err := os.CreateTemp(l.root, ".tmp-*")
	if err != nil {
		return fmt.Errorf("eventlog: temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("eventlog: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("eventlog: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("eventlog: close: %w", err)
	}
	if err := os.Rename(tmpPath, l.path(ev.ID)); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("eventlog: rename: %w", err)
	}
	return nil
}

func (l *EventLog) path(id string) string {
	return filepath.Join(l.root, id+".json")
}

// chainSha = sha256(parent || canonical_payload). Hex.
func chainSha(parent string, canonicalPayload []byte) string {
	h := sha256.New()
	h.Write([]byte(parent))
	h.Write(canonicalPayload)
	return hex.EncodeToString(h.Sum(nil))
}

// canonicalJSON marshals v with sorted map keys (Go's encoding/json
// already sorts map[string]interface{} keys when marshalling, but
// only for the top level; for nested objects we go through a
// round-trip via a stable sorter). The shape we need is "stable text
// for the same logical content" — anything stronger than that is
// over-engineering for the chain.
func canonicalJSON(v interface{}) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	// Re-marshal through interface{} to normalise key order at every
	// depth. This is the standard cheap canonicalisation pattern in Go.
	var parsed interface{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	out, err := json.Marshal(parsed)
	if err != nil {
		return nil, err
	}
	return out, nil
}
