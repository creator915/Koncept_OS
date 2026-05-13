package runtimetools

import (
	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
	"github.com/creator915/Koncept_OS/internal/runtime/install"
)

// Tools returns the runtime-area agent tools, merged by tools.Builtins().
func Tools() map[string]toolcall.Tool {
	return map[string]toolcall.Tool{
		"runtime_smoke":   runtimeSmokeTool(),
		"runtime_install": install.InstallTool(),
		"runtime_link":    install.LinkTool(),
	}
}
