package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// expectedSystemPromptHash pins the SHA256 of the embedded system.md content.
// If you change system.md intentionally, update this constant. The test will
// fail loudly on accidental drift, forcing prompt changes through review.
const expectedSystemPromptHash = "f78fbddc2016bc1ed1cf7cd3242515cb4988e85f163e2918eed353b5bcefe407"

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
