package fs

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/creator915/Koncept_OS/internal/llm"
)

const maxGlobResults = 500

func globTool() llm.Tool {
	return llm.Tool{
		Concurrent: true,
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "glob",
				Description: "Find files matching a filename pattern (e.g. '*.go', 'main_*.ts') recursively. Skips .git, node_modules, .kcpos. Pattern uses Go filepath.Match syntax (no '**'); recursion is implicit.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"pattern": map[string]interface{}{
							"type":        "string",
							"description": "Filename glob pattern matched against the basename, e.g. '*.go'.",
						},
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Root directory to walk. Defaults to current directory.",
						},
					},
					"required": []string{"pattern"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			pattern, _ := args["pattern"].(string)
			if pattern == "" {
				return "", fmt.Errorf("pattern required")
			}
			if _, err := filepath.Match(pattern, ""); err != nil {
				return "", fmt.Errorf("invalid pattern: %w", err)
			}
			root, _ := args["path"].(string)
			if root == "" {
				root = "."
			}

			var matches []string
			err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return nil
				}
				if d.IsDir() {
					switch d.Name() {
					case ".git", "node_modules", ".kcpos", ".idea", ".vscode", "dist", "build":
						return filepath.SkipDir
					}
					return nil
				}
				if len(matches) >= maxGlobResults {
					return filepath.SkipAll
				}
				ok, _ := filepath.Match(pattern, d.Name())
				if ok {
					matches = append(matches, p)
				}
				return nil
			})
			if err != nil {
				return "", err
			}
			if len(matches) == 0 {
				return "no matches", nil
			}
			sort.Strings(matches)
			result := strings.Join(matches, "\n")
			if len(matches) >= maxGlobResults {
				result += fmt.Sprintf("\n[truncated at %d matches]", maxGlobResults)
			}
			return result + "\n", nil
		},
	}
}
