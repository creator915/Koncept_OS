package fs

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/creator915/Koncept_OS/internal/llm"
)

func listDirTool() llm.Tool {
	return llm.Tool{
		Concurrent: true,
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "list_dir",
				Description: "List the entries of a directory. Returns one entry per line, with a trailing slash for directories.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{"type": "string", "description": "Path to the directory."},
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
			entries, err := os.ReadDir(path)
			if err != nil {
				return "", err
			}
			var b strings.Builder
			for _, e := range entries {
				name := e.Name()
				if e.IsDir() {
					name += "/"
				}
				b.WriteString(name)
				b.WriteString("\n")
			}
			return b.String(), nil
		},
	}
}
