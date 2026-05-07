package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// expectedSystemPromptHash pins the SHA256 of the embedded system.md content.
// If you change system.md intentionally, update this constant. The test will
// fail loudly on accidental drift, forcing prompt changes through review.
const expectedSystemPromptHash = "da73779e45f97307132f8b7ed3820897e53b3a894c0e5862c6ba01535e95483e"

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
