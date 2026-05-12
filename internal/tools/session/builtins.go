// Package sessiontools hosts the session_* agent tools — work-session
// lifecycle management distinct from the chat transcript. Imported as
// sessiontools to avoid collision with internal/session.
package sessiontools

import "github.com/creator915/Koncept_OS/internal/llm"

// Tools returns the session-area agent tools.
func Tools() map[string]llm.Tool {
	return map[string]llm.Tool{
		"session_create":           sessionCreateTool(),
		"session_start":            sessionStartTool(),
		"session_show":             sessionShowTool(),
		"session_list":             sessionListTool(),
		"session_status":           sessionStatusTool(),
		"session_delete":           sessionDeleteTool(),
		"session_focus":            sessionFocusTool(),
		"session_aggregate":        sessionAggregateTool(),
		"session_build":            sessionBuildTool(),
		"session_set_architecture": sessionSetArchitectureTool(),
		"session_gate_check":       sessionGateCheckTool(),
		"gate_object":              gateObjectTool(),
	}
}
