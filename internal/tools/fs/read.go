package fs

import (
	"context"
	"fmt"
	"os"

	"github.com/creator915/Koncept_OS/internal/llm"
)

func readFileTool() llm.Tool {
	return llm.Tool{
		Concurrent: true,
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
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
