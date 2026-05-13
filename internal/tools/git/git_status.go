package git

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
)

func gitStatusTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: true,
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "git_status",
				Description: "Show git working tree status (porcelain format with branch info). Errors if not in a git repo.",
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(cctx, "git", "status", "--porcelain", "--branch")
			out, err := cmd.CombinedOutput()
			result := string(out)
			if err != nil {
				return result + fmt.Sprintf("\n[exit: %v]", err), nil
			}
			return result, nil
		},
	}
}
