package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/creator915/Koncept_OS/internal/llm"
	"github.com/creator915/Koncept_OS/internal/graph"
	"github.com/creator915/Koncept_OS/internal/typecalc"
	"github.com/creator915/Koncept_OS/internal/typecalc/lang"
)

func writeFileTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name: "write_file",
				Description: "Write content to a file. Creates the file if it does not exist, overwrites if it does. Creates parent directories as needed.\n\nIf the path matches an implementation file (an `impl` field of any object on the graph, OR matches the `*.impl.*` naming convention), this tool ALSO runs `typecalc_compile` on the content immediately after writing. A compile failure surfaces in the tool result; a passing compile auto-records typecalc evidence on disk so the agent does not need a separate typecalc_compile call before merging the corresponding object to confirmed.",
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
			result := fmt.Sprintf("wrote %d bytes to %s", len(content), path)

			// Auto-typecalc: if this write looks like an implementation
			// file, run the compile rule and record evidence. The decision
			// of "is this an impl?" is made by autoCompileTargets — see
			// that function for the heuristics. We deliberately keep this
			// best-effort: a compile failure surfaces in the tool result
			// (so the agent must address it), but if the language has no
			// compile invoker the helper short-circuits to no-op.
			if extra := autoCompileAfterWrite(ctx, path, content); extra != "" {
				result += "\n\n" + extra
			}
			return result, nil
		},
	}
}

// autoCompileAfterWrite is the §5.5 implementation of "post-write
// auto-compile". Returns a string to APPEND to the tool result describing
// what happened (empty string = no auto-compile triggered).
//
// Logic:
//  1. Resolve which graph object(s), if any, claim this path as their
//     impl. If exactly one match, attribute evidence to that object's id.
//     Multiple matches (e.g. shared single-file impl) → write evidence
//     under each matched id.
//  2. If no match but path matches the conventional `*.impl.*` pattern,
//     extract id from the filename (`Foo.impl.go` → `Foo`).
//  3. If neither matches, no auto-compile (skip — could be docs, configs,
//     test files; the agent will compile explicitly if needed).
//  4. Run lang.CompileLanguageInvoker. On Compiled, write evidence
//     with kind="compile". On CompileError, do NOT write evidence and
//     surface the error log so the agent's next turn must address it.
func autoCompileAfterWrite(ctx context.Context, path, content string) string {
	objectIDs, langTag := resolveAutoCompileTarget(path)
	if len(objectIDs) == 0 {
		return ""
	}
	if langTag == typecalc.LangNone {
		return "" // no language inference possible
	}
	tv := typecalc.New(typecalc.KindCode, content).
		WithState(typecalc.StateUncompiled).
		WithLang(langTag)
	env := &typecalc.RuleEnv{WorkDir: "."}
	out, err := lang.CompileLanguageInvoker(ctx, env, tv)
	if err != nil {
		return fmt.Sprintf("auto-typecalc: invoker error: %v", err)
	}
	if out.State == typecalc.StateCompiled {
		// HTML containing inline <script> is effectively a JS container
		// — record evidence under JavaScript so the gate requires test
		// evidence (you can't dodge testing by writing JS inside HTML).
		effectiveLang := typecalc.DetectEffectiveLang(content, langTag)
		var written []string
		for _, id := range objectIDs {
			if recErr := typecalc.RecordEvidence(id, "compile", string(effectiveLang), true); recErr == nil {
				written = append(written, id)
			}
		}
		if len(written) == 0 {
			return ""
		}
		return fmt.Sprintf("[auto-typecalc] compile passed; evidence recorded for: %s (lang=%s)",
			strings.Join(written, ", "), effectiveLang)
	}
	if out.Kind == typecalc.KindCompileError {
		ce, _ := typecalc.DecodeCompileError(out)
		// Critically, do NOT record evidence. The file is on disk but
		// "implementing" semantics demand a passing compile.
		return fmt.Sprintf(
			"[auto-typecalc] COMPILE FAILED for %s\n  errorCode: %s\n  errorLog:\n%s\n\n"+
				"The file was written but no typecalc evidence was recorded. Fix the syntax / type errors and re-write — or rely on the iterative compile loop. Status=confirmed will be blocked until evidence is recorded.",
			strings.Join(objectIDs, ", "), ce.ErrorCode, indent(ce.ErrorLog, "    "))
	}
	return ""
}

// resolveAutoCompileTarget figures out which graph object(s) and language
// to attribute an auto-compile to.
func resolveAutoCompileTarget(path string) ([]string, typecalc.Lang) {
	// Step 1: graph lookup — find any object whose impl == path.
	var matched []string
	if g, err := graph.LoadOrInit(graph.DefaultPath); err == nil {
		for id, obj := range g.Objects {
			if obj.Impl != nil && *obj.Impl == path {
				matched = append(matched, id)
			}
		}
	}

	// Step 2: filename heuristic for *.impl.* pattern.
	if len(matched) == 0 {
		base := filepath.Base(path)
		// `Foo.impl.go` → name="Foo", ext="go"
		if i := strings.Index(base, ".impl."); i > 0 {
			id := base[:i]
			matched = []string{id}
		}
	}

	if len(matched) == 0 {
		return nil, typecalc.LangNone
	}

	// Language inference from extension.
	ext := filepath.Ext(path)
	return matched, typecalc.LangFromExt(ext)
}

func indent(s, prefix string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
