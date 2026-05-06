package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/creator915/Koncept_OS/internal/chat"
)

func writeFileTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "write_file",
				Description: "Write content to a file. Creates the file if it does not exist, overwrites if it does. Creates parent directories as needed.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path":    map[string]interface{}{"type": "string", "description": "Path to the file."},
						"content": map[string]interface{}{"type": "string", "description": "Content to write."},
					},
					"required": []string{"path", "content"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			path, _ := args["path"].(string)
			content, _ := args["content"].(string)
			if path == "" {
				return "", fmt.Errorf("path required")
			}
			if dir := filepath.Dir(path); dir != "" && dir != "." {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return "", err
				}
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("wrote %d bytes to %s", len(content), path), nil
		},
	}
}
