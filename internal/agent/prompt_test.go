package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// expectedSystemPromptHash pins the SHA256 of the embedded system.md content.
// If you change system.md intentionally, update this constant. The test will
// fail loudly on accidental drift, forcing prompt changes through review.
const expectedSystemPromptHash = "13273d1ef13755912233174ee84538c746bc344ee1c8e8d38c17440fa94725ce"

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
