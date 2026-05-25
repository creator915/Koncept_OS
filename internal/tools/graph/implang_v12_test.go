package graphtools

import (
	"context"
	"strings"
	"testing"
)

// v12 process-justice (2026-05-25): graph_merge_object must refuse
// implLang patches that disagree with any other object's already-set
// implLang in the same graph. Architecture-level language choice is
// pinned at H_architect time and shared across all objects; mid-flow
// switches break PB cleanroom toolchain alignment + throw away impl
// work. The ONLY designed re-architect path is Outer.Obstacle →
// Phase 7 rollback to architecture milestone.
//
// Tests cover: first-set allowed, same-language re-set allowed (no-op),
// different-language change for the SAME object refused, different-
// language change for ANOTHER object refused, case-insensitive
// normalization, empty implLang patch allowed.

// firstSet: empty graph → implLang patch allowed. Architecture's
// initial language choice has to be settable; the gate fires only on
// disagreement, not on the first set.
func TestImplLangV12_FirstSetAllowed(t *testing.T) {
	freshGraphCwd(t)
	_ = run(t, graphCreateObjectTool(), map[string]interface{}{
		"id": "Foo", "intent": "x", "storyPoints": 2, "storyRationale": "trivial test object",
	})
	_, err := graphMergeObjectTool().Run(context.Background(), map[string]interface{}{
		"id":    "Foo",
		"patch": `{"implLang":"Go"}`,
	})
	if err != nil {
		t.Errorf("first implLang set should succeed when no other object has one, got: %v", err)
	}
}

// Same-language re-set is a no-op the gate must accept (idempotency).
func TestImplLangV12_SameLanguageReset(t *testing.T) {
	freshGraphCwd(t)
	_ = run(t, graphCreateObjectTool(), map[string]interface{}{
		"id": "Foo", "intent": "x", "storyPoints": 2, "storyRationale": "trivial test object",
	})
	_ = run(t, graphMergeObjectTool(), map[string]interface{}{
		"id": "Foo", "patch": `{"implLang":"Go"}`,
	})
	_, err := graphMergeObjectTool().Run(context.Background(), map[string]interface{}{
		"id":    "Foo",
		"patch": `{"implLang":"Go"}`,
	})
	if err != nil {
		t.Errorf("same-language re-set should be a no-op, got refusal: %v", err)
	}
}

// Different language on the SAME object → refused. The gate iterates
// all objects including the target's own current value.
func TestImplLangV12_ChangeSameObjectRefused(t *testing.T) {
	freshGraphCwd(t)
	_ = run(t, graphCreateObjectTool(), map[string]interface{}{
		"id": "Foo", "intent": "x", "storyPoints": 2, "storyRationale": "trivial test object",
	})
	_ = run(t, graphMergeObjectTool(), map[string]interface{}{
		"id": "Foo", "patch": `{"implLang":"Go"}`,
	})
	_, err := graphMergeObjectTool().Run(context.Background(), map[string]interface{}{
		"id":    "Foo",
		"patch": `{"implLang":"JavaScript"}`,
	})
	if err == nil {
		t.Fatal("changing implLang on same object must be refused")
	}
	for _, want := range []string{"v12 process-justice", "Phase 7", "architecture milestone"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must mention %q for diagnostic value: %v", want, err)
		}
	}
}

// Different language on a DIFFERENT object in the same graph → refused.
// This is the entr PB-30 failure mode: agent set OsInput.implLang=C
// first, then tried to set ParseArgs.implLang=JavaScript later.
func TestImplLangV12_DifferentObjectRefused(t *testing.T) {
	freshGraphCwd(t)
	_ = run(t, graphCreateObjectTool(), map[string]interface{}{
		"id": "Foo", "intent": "x", "storyPoints": 2, "storyRationale": "first object set to Go",
	})
	_ = run(t, graphMergeObjectTool(), map[string]interface{}{
		"id": "Foo", "patch": `{"implLang":"Go"}`,
	})
	_ = run(t, graphCreateObjectTool(), map[string]interface{}{
		"id": "Bar", "intent": "y", "storyPoints": 2, "storyRationale": "second object will try JS",
	})
	_, err := graphMergeObjectTool().Run(context.Background(), map[string]interface{}{
		"id":    "Bar",
		"patch": `{"implLang":"JavaScript"}`,
	})
	if err == nil {
		t.Fatal("cross-object language disagreement must be refused")
	}
	if !strings.Contains(err.Error(), "Foo") {
		t.Errorf("error should name the conflicting object Foo: %v", err)
	}
}

// Case-insensitive: "go" / "Go" / "GO" are all the same language.
// The agent may serialize either form depending on prompt phrasing;
// the gate shouldn't fire on a casing difference.
func TestImplLangV12_CaseInsensitive(t *testing.T) {
	freshGraphCwd(t)
	_ = run(t, graphCreateObjectTool(), map[string]interface{}{
		"id": "Foo", "intent": "x", "storyPoints": 2, "storyRationale": "trivial test object",
	})
	_ = run(t, graphMergeObjectTool(), map[string]interface{}{
		"id": "Foo", "patch": `{"implLang":"Go"}`,
	})
	for _, variant := range []string{"go", "GO", "Go", "  go  "} {
		_, err := graphMergeObjectTool().Run(context.Background(), map[string]interface{}{
			"id":    "Foo",
			"patch": `{"implLang":"` + variant + `"}`,
		})
		if err != nil {
			t.Errorf("case/whitespace variant %q should normalize to Go, got refusal: %v", variant, err)
		}
	}
}

// Empty implLang patch (explicit "") is treated as "no opinion" and
// must NOT trigger refusal. The agent might use empty to clear; that's
// not a language switch in our intent.
func TestImplLangV12_EmptyAllowed(t *testing.T) {
	freshGraphCwd(t)
	_ = run(t, graphCreateObjectTool(), map[string]interface{}{
		"id": "Foo", "intent": "x", "storyPoints": 2, "storyRationale": "trivial test object",
	})
	_ = run(t, graphMergeObjectTool(), map[string]interface{}{
		"id": "Foo", "patch": `{"implLang":"Go"}`,
	})
	_, err := graphMergeObjectTool().Run(context.Background(), map[string]interface{}{
		"id":    "Foo",
		"patch": `{"implLang":""}`,
	})
	if err != nil {
		t.Errorf("empty implLang patch should not trigger refusal, got: %v", err)
	}
}
