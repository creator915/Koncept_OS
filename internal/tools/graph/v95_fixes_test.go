package graphtools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creator915/Koncept_OS/internal/app/workflow"
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
)

// Tests for the two v9.5 follow-up fixes uncovered by the 2026-05-14 HE
// batch:
//   1. statusSession was always null after status-change merges because
//      MarkConfirmed (the only path that wrote it) covered the chained
//      confirm_object service, but agents mostly drove status=confirmed
//      via direct graph_merge_attribute / graph_merge_object calls.
//   2. graph_create_object accepted objects without storyPoints, which
//      v9.5 designed as required — the validation existed in
//      MergeObject (for storyPoints set later) but not at creation.

// --- injectStatusSessionFromFocus unit tests ---

func TestInjectStatusSession_NoStatus_NoChange(t *testing.T) {
	chdirTempProject(t)
	if _, err := workflow.Create(persistence.SessionDefaultDir, "s_x", "", "", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if err := persistence.SetFocus(persistence.SessionDefaultDir, "s_x"); err != nil {
		t.Fatal(err)
	}
	patch := map[string]any{"intent": "irrelevant"}
	injectStatusSessionFromFocus(patch)
	if _, ok := patch["statusSession"]; ok {
		t.Fatalf("statusSession was injected despite no status in patch: %v", patch)
	}
}

func TestInjectStatusSession_StatusPresent_InjectsFocus(t *testing.T) {
	chdirTempProject(t)
	if _, err := workflow.Create(persistence.SessionDefaultDir, "s_root", "", "", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if err := persistence.SetFocus(persistence.SessionDefaultDir, "s_root"); err != nil {
		t.Fatal(err)
	}
	patch := map[string]any{"status": "confirmed"}
	injectStatusSessionFromFocus(patch)
	got, ok := patch["statusSession"].(string)
	if !ok || got != "s_root" {
		t.Fatalf("expected statusSession=s_root, got %v (%T)", patch["statusSession"], patch["statusSession"])
	}
}

func TestInjectStatusSession_StatusPresent_NoFocus_LeavesUnset(t *testing.T) {
	chdirTempProject(t)
	// No SetFocus call — current focus is empty string.
	patch := map[string]any{"status": "confirmed"}
	injectStatusSessionFromFocus(patch)
	if _, ok := patch["statusSession"]; ok {
		t.Fatalf("statusSession should not be injected when no focus is set: %v", patch)
	}
}

func TestInjectStatusSession_ExplicitValue_NotOverwritten(t *testing.T) {
	chdirTempProject(t)
	if _, err := workflow.Create(persistence.SessionDefaultDir, "s_other", "", "", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if err := persistence.SetFocus(persistence.SessionDefaultDir, "s_other"); err != nil {
		t.Fatal(err)
	}
	patch := map[string]any{"status": "confirmed", "statusSession": "s_explicit"}
	injectStatusSessionFromFocus(patch)
	if got, _ := patch["statusSession"].(string); got != "s_explicit" {
		t.Fatalf("explicit statusSession overwritten: got %q want s_explicit", got)
	}
}

// --- graph_create_object storyPoints/storyRationale validation tests ---

func TestGraphCreateObject_MissingStoryPoints_Rejected(t *testing.T) {
	chdirTempProject(t)
	if err := os.MkdirAll("K", 0o755); err != nil {
		t.Fatal(err)
	}
	tool := graphCreateObjectTool()
	_, err := tool.Run(context.Background(), map[string]any{
		"id":             "Foo",
		"intent":         "compute Foo",
		"storyRationale": "single arithmetic",
		// storyPoints omitted
	})
	if err == nil {
		t.Fatal("expected error for missing storyPoints")
	}
	if !strings.Contains(err.Error(), "storyPoints required") {
		t.Fatalf("expected storyPoints-required error, got: %v", err)
	}
}

func TestGraphCreateObject_InvalidStoryPoints_Rejected(t *testing.T) {
	chdirTempProject(t)
	if err := os.MkdirAll("K", 0o755); err != nil {
		t.Fatal(err)
	}
	tool := graphCreateObjectTool()
	_, err := tool.Run(context.Background(), map[string]any{
		"id":             "Foo",
		"intent":         "compute Foo",
		"storyPoints":    float64(4), // not a Fibonacci value
		"storyRationale": "single arithmetic loop",
	})
	if err == nil || !strings.Contains(err.Error(), "Fibonacci") {
		t.Fatalf("expected Fibonacci-scale error for storyPoints=4, got: %v", err)
	}
}

func TestGraphCreateObject_ShortRationale_Rejected(t *testing.T) {
	chdirTempProject(t)
	if err := os.MkdirAll("K", 0o755); err != nil {
		t.Fatal(err)
	}
	tool := graphCreateObjectTool()
	_, err := tool.Run(context.Background(), map[string]any{
		"id":             "Foo",
		"intent":         "compute Foo",
		"storyPoints":    float64(2),
		"storyRationale": "short", // < 10 chars
	})
	if err == nil || !strings.Contains(err.Error(), "storyRationale") {
		t.Fatalf("expected storyRationale-length error, got: %v", err)
	}
}

func TestGraphCreateObject_HappyPath_StoresFields(t *testing.T) {
	chdirTempProject(t)
	if err := os.MkdirAll("K", 0o755); err != nil {
		t.Fatal(err)
	}
	tool := graphCreateObjectTool()
	out, err := tool.Run(context.Background(), map[string]any{
		"id":             "Foo",
		"intent":         "compute Foo",
		"def":            "K/defs/Foo.py",
		"storyPoints":    float64(3),
		"storyRationale": "multi-branch with boundary check",
	})
	if err != nil {
		t.Fatalf("happy path errored: %v", err)
	}
	if !strings.Contains(out, "storyPoints=3") {
		t.Errorf("result message should reflect storyPoints, got: %s", out)
	}
	// Verify graph.json on disk has the fields populated.
	raw, err := os.ReadFile(filepath.Join("K", "graph.json"))
	if err != nil {
		t.Fatalf("read graph.json: %v", err)
	}
	var g struct {
		Objects map[string]struct {
			StoryPoints    int    `json:"storyPoints"`
			StoryRationale string `json:"storyRationale"`
		} `json:"objects"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("parse graph.json: %v\n%s", err, raw)
	}
	obj, ok := g.Objects["Foo"]
	if !ok {
		t.Fatalf("Foo not in graph.json: %s", raw)
	}
	if obj.StoryPoints != 3 {
		t.Errorf("storyPoints not persisted: got %d want 3", obj.StoryPoints)
	}
	if obj.StoryRationale != "multi-branch with boundary check" {
		t.Errorf("storyRationale not persisted: got %q", obj.StoryRationale)
	}
}
