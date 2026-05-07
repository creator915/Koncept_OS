package agent

import (
	"encoding/json"
	"fmt"

	"github.com/creator915/Koncept_OS/internal/typecalc"
)

// authorizeToolCall is the §6 permission gate: before any tool executes,
// translate the tool name + raw args into a (verb, arg) pair and ask the
// caller's CapSet whether to allow it.
//
// If caps is nil or empty, the gate is disabled — this preserves the
// pre-typecalc behavior at the top level, where the user is implicitly
// trusted with everything. Sub-agents that opt into capability scoping
// pass a non-empty CapSet and the gate becomes active for that loop.
//
// Returns nil to allow, or a *typecalc.TypedValue of Kind
// KindPermissionDenied to deny. The caller is expected to feed the
// denied value back to the model as the tool result so the model can
// react (typically by escalating Obstacle or attempting a different
// approach within its caps).
func authorizeToolCall(caps typecalc.CapSet, toolName, argsJSON string) *typecalc.TypedValue {
	if len(caps) == 0 {
		return nil
	}
	verb, arg := mapToolToCapability(toolName, argsJSON)
	return caps.Authorize(verb, arg)
}

// mapToolToCapability translates a tool invocation into the (verb, arg)
// vocabulary used by the typecalc.Capability tokens. Most tools collapse
// to verb="run_tool" with arg=tool name; the file-touching tools
// (read_file, write_file, edit) extract the path so glob-based caps like
// `read_file:K/defs/*` actually constrain what the agent can touch.
func mapToolToCapability(toolName, argsJSON string) (string, string) {
	switch toolName {
	case "read_file":
		var a struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal([]byte(argsJSON), &a)
		return "read_file", a.Path
	case "write_file":
		var a struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal([]byte(argsJSON), &a)
		return "write_file", a.Path
	case "edit":
		// edit takes file_path or path depending on which tool variant; cover
		// both fields so the gate works regardless.
		var a struct {
			Path     string `json:"path"`
			FilePath string `json:"file_path"`
		}
		_ = json.Unmarshal([]byte(argsJSON), &a)
		path := a.Path
		if path == "" {
			path = a.FilePath
		}
		return "write_file", path
	case "spawn_subagent":
		// The role suffix is informational; arg "*" + glob caps mean any role.
		var a struct {
			Role string `json:"role"`
		}
		_ = json.Unmarshal([]byte(argsJSON), &a)
		role := a.Role
		if role == "" {
			role = "*"
		}
		return "spawn_agent", role
	default:
		// Every other tool maps to run_tool:<name>. Cap "run_tool:*" admits
		// all of them; "run_tool:graph_validate" admits only the named one.
		return "run_tool", toolName
	}
}

// renderPermissionDenied formats a denied typed value as a human-readable
// tool result. The agent will see this exact string as the tool's stdout
// equivalent and is expected to either (a) escalate via Obstacle, or (b)
// pick a different approach within its caps.
func renderPermissionDenied(denied *typecalc.TypedValue, toolName string) string {
	if denied == nil {
		return ""
	}
	var detail struct {
		Verb string   `json:"verb"`
		Arg  string   `json:"arg"`
		Caps []string `json:"availableCaps"`
	}
	_ = json.Unmarshal([]byte(denied.Payload), &detail)
	return fmt.Sprintf(
		"PermissionDenied: tool %q maps to capability %q on argument %q, which is not in this agent's permitted set.\n"+
			"Available capabilities:\n  %s\n"+
			"If this tool is genuinely needed, escalate the task as Obstacle to the parent agent (which holds a superset of caps), "+
			"or pick a different tool that fits your permitted set.",
		toolName, detail.Verb, detail.Arg, formatCapList(detail.Caps),
	)
}

func formatCapList(caps []string) string {
	if len(caps) == 0 {
		return "(none)"
	}
	out := ""
	for i, c := range caps {
		if i > 0 {
			out += "\n  "
		}
		out += c
	}
	return out
}
