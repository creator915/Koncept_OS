package graphtools

import (
	"context"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
)

// TestGraphDeleteObject_HappyPath — declared object is removed and
// the change persists to K/graph.json on disk.
func TestGraphDeleteObject_HappyPath(t *testing.T) {
	freshGraphCwd(t)
	_ = run(t, graphCreateObjectTool(), map[string]interface{}{
		"id": "Foo", "intent": "x", "storyPoints": 2, "storyRationale": "trivial test object",
	})
	_ = run(t, graphCreateObjectTool(), map[string]interface{}{
		"id": "Bar", "intent": "y", "storyPoints": 2, "storyRationale": "second test object",
	})
	out := run(t, graphDeleteObjectTool(), map[string]interface{}{"id": "Foo"})
	if !strings.Contains(out, "deleted") {
		t.Errorf("result should announce deletion: %s", out)
	}
	// Verify disk.
	g, err := persistence.LoadGraph(persistence.GraphDefaultPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := g.Objects["Foo"]; ok {
		t.Errorf("Foo should be removed from disk graph")
	}
	if _, ok := g.Objects["Bar"]; !ok {
		t.Errorf("Bar should remain on disk")
	}
}

// TestGraphDeleteObject_RefusesConfirmed — process-justice: confirmed
// objects can't be silently deleted. The agent must demote first.
// Without this guard the gate would later fail on missing-confirmed
// errors at session finish, hard to recover from.
func TestGraphDeleteObject_RefusesConfirmed(t *testing.T) {
	freshGraphCwd(t)
	_ = run(t, graphCreateObjectTool(), map[string]interface{}{
		"id": "Confirmed", "intent": "x", "storyPoints": 2, "storyRationale": "process-justice test target",
	})
	// Bypass MergeObject's process-justice guard and set confirmed
	// directly on disk for the test fixture.
	g, _ := persistence.LoadGraph(persistence.GraphDefaultPath)
	g.Objects["Confirmed"].Status = graph.StatusConfirmed
	_ = persistence.SaveGraph(persistence.GraphDefaultPath, g)

	_, err := graphDeleteObjectTool().Run(context.Background(),
		map[string]interface{}{"id": "Confirmed"})
	if err == nil {
		t.Fatal("expected refusal of confirmed-object delete, got nil")
	}
	if !strings.Contains(err.Error(), "status=confirmed") {
		t.Errorf("error should explain the refusal reason: %v", err)
	}
	// Object must still be on disk.
	g, _ = persistence.LoadGraph(persistence.GraphDefaultPath)
	if _, ok := g.Objects["Confirmed"]; !ok {
		t.Errorf("refused delete must not mutate disk")
	}
}

// TestGraphDeleteObject_UnknownID — clear error rather than silent
// success when target doesn't exist.
func TestGraphDeleteObject_UnknownID(t *testing.T) {
	freshGraphCwd(t)
	_, err := graphDeleteObjectTool().Run(context.Background(),
		map[string]interface{}{"id": "Ghost"})
	if err == nil {
		t.Fatal("expected error for unknown id")
	}
	if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "not in graph") {
		t.Errorf("error should mention missing object: %v", err)
	}
}

// TestGraphDeleteObject_EmptyID — empty id is a programmer error.
func TestGraphDeleteObject_EmptyID(t *testing.T) {
	freshGraphCwd(t)
	_, err := graphDeleteObjectTool().Run(context.Background(),
		map[string]interface{}{"id": ""})
	if err == nil {
		t.Fatal("expected error for empty id")
	}
}
