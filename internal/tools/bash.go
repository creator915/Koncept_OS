package tools

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/creator915/Koncept_OS/internal/chat"
)

func bashTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "bash",
				Description: "Run a bash command in the current working directory. Returns combined stdout and stderr. Default timeout 30s.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"command": map[string]interface{}{"type": "string", "description": "The shell command to execute."},
					},
					"required": []string{"command"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			command, _ := args["command"].(string)
			if command == "" {
				return "", fmt.Errorf("command required")
			}
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(cctx, "bash", "-c", command)
			out, err := cmd.CombinedOutput()
			result := string(out)
			if cctx.Err() == context.DeadlineExceeded {
				return result + "\n[timed out after 30s]", nil
			}
			if err != nil {
				return result + fmt.Sprintf("\n[exit: %v]", err), nil
			}
			return result, nil
		},
	}
}
