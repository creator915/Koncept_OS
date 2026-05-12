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
				Description: "Read the contents of a file at the given path. Returns the file content as text.\n\n**v9.0.4 markdown auto-outline**: when the target is a .md/.markdown file longer than ~5K tokens, read_file returns the chapter OUTLINE instead of the full body. Fetch individual chapters via markdown_section. Pass force=true to read the whole file anyway (NOT recommended — every parent / child agent that opens a long markdown in this mode multiplies its context footprint).",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Path to the file. Absolute or relative to the current working directory.",
						},
						"force": map[string]interface{}{
							"type":        "boolean",
							"description": "Optional. Override the auto-outline behavior for large markdown files. Default false. Setting true is an anti-pattern for long markdown — prefer markdown_section.",
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
			force, _ := args["force"].(bool)
			b, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			if outline, swapped := MaybeOutlineMarkdown(path, b, force); swapped {
				return outline, nil
			}
			return string(b), nil
		},
	}
}
