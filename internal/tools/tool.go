// Package tools is the aggregation point for the agent's tool registry.
// Individual tools live in subpackages (fs, git, graph (graphtools),
// session (sessiontools), checkpoint (checkpointtools), typecalc
// (typecalctools)); this file just merges their Tools() maps and
// provides Execute / Specs helpers used by the agent loop.
//
// `Tool` itself is defined in package llm (see internal/llm/tool.go) so
// subpackages can return it without an import cycle on this package.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/creator915/Koncept_OS/internal/llm"
	checkpointtools "github.com/creator915/Koncept_OS/internal/tools/checkpoint"
	"github.com/creator915/Koncept_OS/internal/tools/fs"
	"github.com/creator915/Koncept_OS/internal/tools/git"
	graphtools "github.com/creator915/Koncept_OS/internal/tools/graph"
	sessiontools "github.com/creator915/Koncept_OS/internal/tools/session"
	typecalctools "github.com/creator915/Koncept_OS/internal/tools/typecalc"
)

// Tool is re-exported from llm so existing callers (agent.RunTurnOpts,
// SubAgentRunner, cmd/kcpos) can keep using `tools.Tool` as the
// interchange type. New code may use `llm.Tool` directly.
type Tool = llm.Tool

// Builtins returns the full set of agent tools, aggregated from each
// subpackage. The set is a fresh map every call (so callers can mutate
// it — e.g. BuiltinsWithSubAgent injects spawn_subagent).
func Builtins() map[string]Tool {
	out := map[string]Tool{}
	for k, v := range fs.Tools() {
		out[k] = v
	}
	for k, v := range git.Tools() {
		out[k] = v
	}
	for k, v := range graphtools.Tools() {
		out[k] = v
	}
	for k, v := range sessiontools.Tools() {
		out[k] = v
	}
	for k, v := range checkpointtools.Tools() {
		out[k] = v
	}
	for k, v := range typecalctools.Tools() {
		out[k] = v
	}
	return out
}

// Specs extracts the schema descriptions the LLM provider's chat API
// expects in its `tools` field.
func Specs(tools map[string]Tool) []llm.ToolSpec {
	specs := make([]llm.ToolSpec, 0, len(tools))
	for _, t := range tools {
		specs = append(specs, t.Spec)
	}
	return specs
}

// Execute runs the named tool with JSON-encoded args, returning a
// stringified result the agent loop posts back as a tool message.
// Errors are surfaced as `error: ...` strings so the loop can still
// continue (the agent is expected to react to the error).
func Execute(ctx context.Context, tools map[string]Tool, name, argsJSON string) string {
	t, ok := tools[name]
	if !ok {
		return fmt.Sprintf("error: unknown tool %q", name)
	}
	var args map[string]interface{}
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return fmt.Sprintf("error: invalid arguments JSON: %v", err)
		}
	}
	out, err := t.Run(ctx, args)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return out
}
