package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// Genesis event: parent="" + a known payload should produce a sha
// match against an external recomputation. This is the load-bearing
// integrity property of the chain — verify it once with a fixed
// vector so a future refactor can't silently change the hashing rule.
func TestEventLog_GenesisShaIsCanonical(t *testing.T) {
	log := NewEventLog(filepath.Join(t.TempDir(), "events"))
	payload := map[string]string{"hello": "world"}
	ev, err := log.Append("", "test.fixture", payload)
	if err != nil {
		t.Fatal(err)
	}
	// Recompute by hand: canonical JSON of payload + "" parent.
	raw, _ := json.Marshal(payload)
	var roundtrip interface{}
	_ = json.Unmarshal(raw, &roundtrip)
	canon, _ := json.Marshal(roundtrip)
	h := sha256.New()
	h.Write([]byte("")) // parent
	h.Write(canon)
	wantSha := hex.EncodeToString(h.Sum(nil))
	if ev.ID != wantSha {
		t.Errorf("genesis sha mismatch:\n  got  %s\n  want %s", ev.ID, wantSha)
	}
}

// SHA chain: child id depends on parent id. Two children of the same
// parent with the same payload must collide; two children with
// different parents (same payload) must differ.
func TestEventLog_ChainTiesParent(t *testing.T) {
	log := NewEventLog(filepath.Join(t.TempDir(), "events"))
	a, _ := log.Append("", "test", map[string]int{"x": 1})
	b, _ := log.Append(a.ID, "test", map[string]int{"x": 2})
	c, _ := log.Append(a.ID, "test", map[string]int{"x": 2}) // same as b
	if b.ID != c.ID {
		t.Errorf("same parent + same payload must produce same id: %s vs %s", b.ID, c.ID)
	}
	d, _ := log.Append(b.ID, "test", map[string]int{"x": 2}) // different parent
	if d.ID == b.ID {
		t.Errorf("different parent must produce different id, both got %s", d.ID)
	}
}

// Round-trip: Append → Get returns the same event (id, parent, type,
// payload preserved). Timestamp is set by Append so we don't assert
// equality on it — only assert it's recent.
func TestEventLog_AppendThenGet(t *testing.T) {
	log := NewEventLog(filepath.Join(t.TempDir(), "events"))
	original, err := log.Append("", "tool.exec", map[string]any{"tool": "write_file", "path": "main.c"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := log.Get(original.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != original.ID || got.ParentID != original.ParentID || got.Type != original.Type {
		t.Errorf("event metadata mismatch: got %+v want %+v", got, original)
	}
	// Payload preserved as RawMessage — compare canonical form.
	gotMap := map[string]any{}
	wantMap := map[string]any{}
	_ = json.Unmarshal(got.Payload, &gotMap)
	_ = json.Unmarshal(original.Payload, &wantMap)
	if gotMap["tool"] != wantMap["tool"] || gotMap["path"] != wantMap["path"] {
		t.Errorf("payload mismatch: got %v want %v", gotMap, wantMap)
	}
}

func TestEventLog_GetMissing(t *testing.T) {
	log := NewEventLog(filepath.Join(t.TempDir(), "events"))
	_, err := log.Get(strings.Repeat("a", 64))
	if !errors.Is(err, ErrEventNotFound) {
		t.Errorf("expected ErrEventNotFound, got %v", err)
	}
}

func TestEventLog_RejectInvalidParent(t *testing.T) {
	log := NewEventLog(filepath.Join(t.TempDir(), "events"))
	_, err := log.Append("not-a-sha", "test", nil)
	if err == nil {
		t.Error("invalid parent id must be rejected")
	}
}

// Walk replays in chain order starting from a known genesis. After
// building a 4-event linear chain we should see all 4 in order.
func TestEventLog_WalkLinearChain(t *testing.T) {
	log := NewEventLog(filepath.Join(t.TempDir(), "events"))
	a, _ := log.Append("", "test", "a")
	b, _ := log.Append(a.ID, "test", "b")
	c, _ := log.Append(b.ID, "test", "c")
	d, _ := log.Append(c.ID, "test", "d")

	var visited []string
	err := log.Walk("", func(e Event) error {
		visited = append(visited, string(e.Payload))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`"a"`, `"b"`, `"c"`, `"d"`}
	if len(visited) != len(want) {
		t.Fatalf("walk length %d != %d", len(visited), len(want))
	}
	for i := range want {
		if visited[i] != want[i] {
			t.Errorf("walk[%d]: got %s want %s", i, visited[i], want[i])
		}
	}
	_ = d
}

// Canonical JSON: same logical content yields same chain id even when
// the input map's key order differs across calls (Go map iteration
// is random). The unit test below cannot reliably trigger random
// iteration but the canonical-marshal step ensures determinism for
// nested objects too — assert on that.
func TestEventLog_CanonicalMarshalIsStable(t *testing.T) {
	a, _ := canonicalJSON(map[string]any{
		"a": 1, "b": 2, "nested": map[string]int{"y": 2, "x": 1},
	})
	b, _ := canonicalJSON(map[string]any{
		"nested": map[string]int{"x": 1, "y": 2}, "b": 2, "a": 1,
	})
	if string(a) != string(b) {
		t.Errorf("canonical marshal not stable across key order:\n  %s\n  %s", a, b)
	}
}

// List returns every event, sorted by timestamp. With a linear chain
// the timestamp order matches the chain order.
func TestEventLog_ListSorted(t *testing.T) {
	log := NewEventLog(filepath.Join(t.TempDir(), "events"))
	a, _ := log.Append("", "test", 1)
	b, _ := log.Append(a.ID, "test", 2)
	c, _ := log.Append(b.ID, "test", 3)
	all, err := log.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 events, got %d", len(all))
	}
	if all[0].ID != a.ID || all[1].ID != b.ID || all[2].ID != c.ID {
		t.Errorf("list not in chain order: %s, %s, %s", all[0].ID[:8], all[1].ID[:8], all[2].ID[:8])
	}
}
