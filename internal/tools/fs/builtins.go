// Package fs hosts filesystem-and-shell tools (read_file, write_file,
// edit, list_dir, glob, grep, bash). Splitting this into its own
// subpackage isolates write_file's auto-typecalc-on-impl-write logic
// from the graph-mutation tools (graphtools), and keeps shell concerns
// contained.
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
		"bash":              bashTool(),
		"grep":              grepTool(),
		"glob":              globTool(),
		"markdown_outline":  markdownOutlineTool(),
		"markdown_section":  markdownSectionTool(),
		"markdown_validate": markdownValidateTool(),
	}
}
