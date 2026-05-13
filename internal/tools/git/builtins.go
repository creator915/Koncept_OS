// Package git hosts the git_status agent tool — read-only inspection
// of the working tree. Lives in its own subpackage to leave room for
// future git tools (git_log, git_diff) without bloating tools/.
package git

import "github.com/creator915/Koncept_OS/internal/llm/toolcall"

// Tools returns the git-area agent tools.
func Tools() map[string]toolcall.Tool {
	return map[string]toolcall.Tool{
		"git_status": gitStatusTool(),
	}
}
