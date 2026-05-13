// Package sessiontools hosts the session_* agent tools — work-session
// lifecycle management distinct from the chat transcript. Imported as
// sessiontools to avoid collision with internal/session.
package sessiontools

import "github.com/creator915/Koncept_OS/internal/llm/toolcall"

// Tools returns the session-area agent tools.
func Tools() map[string]toolcall.Tool {
	return map[string]toolcall.Tool{
		"session_create":           sessionCreateTool(),
		"session_start":            sessionStartTool(),
		"session_show":             sessionShowTool(),
		"session_list":             sessionListTool(),
		"session_status":           sessionStatusTool(),
		// v9.3: split the old session_delete (which was actually a
		// destructive depth-first rollback) into two clearer tools.
		// Three Terraria incidents (v9.0.6 03, v92-01, v92-02) traced
		// to agents misreading "delete" as "cleanup". The new names
		// match semantics.
		"session_rollback": sessionRollbackTool(),
		"session_dismiss":  sessionDismissTool(),
		"session_focus":            sessionFocusTool(),
		"session_aggregate":        sessionAggregateTool(),
		"session_build":            sessionBuildTool(),
		"session_set_architecture": sessionSetArchitectureTool(),
		"session_gate_check":       sessionGateCheckTool(),
		"gate_object":              gateObjectTool(),
	}
}
