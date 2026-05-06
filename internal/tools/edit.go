package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/creator915/Koncept_OS/internal/chat"
)

func editTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "edit",
				Description: "Replace exact text in a file. The old_string must appear exactly once unless replace_all is true. Use this for targeted edits instead of rewriting whole files.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path":       map[string]interface{}{"type": "string", "description": "Path to the file."},
						"old_string": map[string]interface{}{"type": "string", "description": "Exact text to find. Include enough surrounding context to make it unique unless replace_all is true."},
						"new_string": map[string]interface{}{"type": "string", "description": "Text to replace with. Must differ from old_string."},
						"replace_all": map[string]interface{}{
							"type":        "boolean",
							"description": "If true, replace every occurrence. Defaults to false.",
						},
					},
					"required": []string{"path", "old_string", "new_string"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			path, _ := args["path"].(string)
			oldStr, _ := args["old_string"].(string)
			newStr, _ := args["new_string"].(string)
			replaceAll, _ := args["replace_all"].(bool)

			if path == "" {
				return "", fmt.Errorf("path required")
			}
			if oldStr == "" {
				return "", fmt.Errorf("old_string required")
			}
			if oldStr == newStr {
				return "", fmt.Errorf("old_string and new_string are identical")
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			original := string(content)
			count := strings.Count(original, oldStr)
			if count == 0 {
				return "", fmt.Errorf("old_string not found in %s", path)
			}
			if count > 1 && !replaceAll {
				return "", fmt.Errorf("old_string matched %d times in %s; add more context to make it unique or set replace_all=true", count, path)
			}
			var updated string
			if replaceAll {
				updated = strings.ReplaceAll(original, oldStr, newStr)
			} else {
				updated = strings.Replace(original, oldStr, newStr, 1)
			}
			if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
				return "", err
			}
			if count == 1 {
				return fmt.Sprintf("edited %s", path), nil
			}
			return fmt.Sprintf("edited %s (%d occurrences replaced)", path, count), nil
		},
	}
}
