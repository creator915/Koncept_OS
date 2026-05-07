// Package checkpointtools hosts the checkpoint_* agent tools — the
// verification ledger workflow (立卷 → freeze → fill → show). Imported
// as checkpointtools to avoid collision with internal/checkpoint.
package checkpointtools

import (
	"strings"

	"github.com/creator915/Koncept_OS/internal/llm"
)

// Tools returns the checkpoint-area agent tools.
func Tools() map[string]llm.Tool {
	return map[string]llm.Tool{
		"checkpoint_add_item": checkpointAddItemTool(),
		"checkpoint_freeze":   checkpointFreezeTool(),
		"checkpoint_fill":     checkpointFillTool(),
		"checkpoint_waive":    checkpointWaiveTool(),
		"checkpoint_show":     checkpointShowTool(),
	}
}

// truncateText shortens a string to n characters with an ellipsis.
// Local copy because the original lives in tools/session/session.go;
// duplicating four lines beats introducing a shared util package.
func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "..."
}
