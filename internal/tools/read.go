package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/creator915/Koncept_OS/internal/chat"
)

func readFileTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "read_file",
				Description: "Read the contents of a file at the given path. Returns the file content as text.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Path to the file. Absolute or relative to the current working directory.",
						},
					},
					"required": []string{"path"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			path, _ := args["path"].(string)
			if path == "" {
				return "", fmt.Errorf("path required")
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	}
}
