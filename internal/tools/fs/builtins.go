// Package fs hosts filesystem tools (read_file, write_file, edit,
// list_dir, glob, grep) plus the COMMAND-LOCKED execution tools
// (compile, run_local, probe). The universal `bash` tool was removed
// UNCONDITIONALLY: there is no model-facing arbitrary-shell tool in
// any mode — no capability contract, no flag, no escape hatch can
// bring it back. Shell capability is reachable ONLY through the
// command-locked sub-tools, which take typed argv/stdin and never a
// command string. The sole residual shell is `compile` running the
// agent-authored compile.sh (see sandboxed.go) — the documented,
// deployment-walled residual, not a general bash.
package fs

import "github.com/creator915/Koncept_OS/internal/llm/toolcall"

// Tools returns the set of fs-area agent tools, ready to be merged into
// the global tools registry by tools.Builtins().
func Tools() map[string]toolcall.Tool {
	return map[string]toolcall.Tool{
		"read_file":         readFileTool(),
		"write_file":        writeFileTool(),
		"edit":              editTool(),
		"list_dir":          listDirTool(),
		"compile":           compileTool(),
		"run_local":         runLocalTool(),
		"probe":             probeTool(),
		"grep":              grepTool(),
		"glob":              globTool(),
		"markdown_outline":  markdownOutlineTool(),
		"markdown_section":  markdownSectionTool(),
		"markdown_validate": markdownValidateTool(),
	}
}
