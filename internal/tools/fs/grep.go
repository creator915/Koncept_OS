package fs

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
)

const maxGrepResults = 200

func grepTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: true,
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "grep",
				Description: "Search file contents for a regex pattern recursively. Returns matches as path:line: content. Skips .git, node_modules, .kcpos. Caps at 200 matches.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"pattern": map[string]interface{}{
							"type":        "string",
							"description": "Go RE2 regex pattern.",
						},
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Directory or file to search. Defaults to current directory.",
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
			root, _ := args["path"].(string)
			if root == "" {
				root = "."
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				return "", fmt.Errorf("invalid pattern: %w", err)
			}

			var (
				out     strings.Builder
				matched int
			)
			walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
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
				if matched >= maxGrepResults {
					return filepath.SkipAll
				}
				f, err := os.Open(p)
				if err != nil {
					return nil
				}
				defer f.Close()
				scanner := bufio.NewScanner(f)
				scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
				lineNum := 0
				for scanner.Scan() {
					lineNum++
					line := scanner.Text()
					if re.MatchString(line) {
						fmt.Fprintf(&out, "%s:%d: %s\n", p, lineNum, line)
						matched++
						if matched >= maxGrepResults {
							fmt.Fprintf(&out, "[truncated at %d matches]\n", maxGrepResults)
							return filepath.SkipAll
						}
					}
				}
				return nil
			})
			if walkErr != nil {
				return "", walkErr
			}
			if matched == 0 {
				return "no matches", nil
			}
			return out.String(), nil
		},
	}
}
