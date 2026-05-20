package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// expectedSystemPromptHash pins the SHA256 of the embedded system.md content.
// If you change system.md intentionally, update this constant. The test will
// fail loudly on accidental drift, forcing prompt changes through review.
// Re-anchored 2026-05-20 (#3, v12): implFragment hidden from agent
// view entirely. graph_merge_object now AUTO-DERIVES
// implFragment=K/frags/<id>.js when impl is HTML and no fragment is
// set, so the agent only ever sets `impl` + `implContent`. system.md
// and protocol.go Describe() rewritten to drop every prompt-side
// mention of implFragment as a user-facing field. K/frags/* remains
// hard-refused for write_file (v10 guard); the path now appears only
// in negative/historical context. This removes the failure mode
// where the agent learned about K/frags from the prompt then tried
// to write_file there.
const expectedSystemPromptHash = "685c09e10291a991c2360969aa2725c487a1f91fe1ea91c6c1ed2062e0bb95e8"

func TestSystemPromptHash(t *testing.T) {
	sum := sha256.Sum256([]byte(SystemPrompt))
	got := hex.EncodeToString(sum[:])
	if got != expectedSystemPromptHash {
		t.Fatalf(`system prompt drift detected.
got:      %s
expected: %s

If the change to internal/agent/system.md is intentional, update
expectedSystemPromptHash in prompt_test.go to the new value.`, got, expectedSystemPromptHash)
	}
}
