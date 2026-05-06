package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/creator915/Koncept_OS/internal/chat"
)

type Tool struct {
	Spec chat.ToolSpec
	Run  func(ctx context.Context, args map[string]interface{}) (string, error)
}

func Builtins() map[string]Tool {
	return map[string]Tool{
		"read_file":              readFileTool(),
		"write_file":             writeFileTool(),
		"edit":                   editTool(),
		"list_dir":               listDirTool(),
		"bash":                   bashTool(),
		"grep":                   grepTool(),
		"glob":                   globTool(),
		"git_status":             gitStatusTool(),
		"graph_show":             graphShowTool(),
		"graph_create_attribute": graphCreateAttributeTool(),
		"graph_create_object":    graphCreateObjectTool(),
		"graph_link_refine":      graphLinkRefineTool(),
		"graph_link_consume":     graphLinkConsumeTool(),
		"graph_link_produce":     graphLinkProduceTool(),
		"graph_unlink_refine":    graphUnlinkRefineTool(),
		"graph_unlink_consume":   graphUnlinkConsumeTool(),
		"graph_unlink_produce":   graphUnlinkProduceTool(),
		"graph_merge_attribute":  graphMergeAttributeTool(),
		"graph_merge_object":     graphMergeObjectTool(),
		"graph_autowire":         graphAutowireTool(),
		"graph_validate":         graphValidateTool(),
		"graph_preflight":        graphPreflightTool(),
		"graph_render":           graphRenderTool(),
		"session_create":         sessionCreateTool(),
		"session_start":          sessionStartTool(),
		"session_show":           sessionShowTool(),
		"session_list":           sessionListTool(),
		"session_status":         sessionStatusTool(),
		"session_delete":         sessionDeleteTool(),
		"session_focus":          sessionFocusTool(),
		"session_aggregate":      sessionAggregateTool(),
		"session_gate_check":     sessionGateCheckTool(),
		"checkpoint_add_item":    checkpointAddItemTool(),
		"checkpoint_freeze":      checkpointFreezeTool(),
		"checkpoint_fill":        checkpointFillTool(),
		"checkpoint_waive":       checkpointWaiveTool(),
		"checkpoint_show":        checkpointShowTool(),
	}
}

func Specs(tools map[string]Tool) []chat.ToolSpec {
	specs := make([]chat.ToolSpec, 0, len(tools))
	for _, t := range tools {
		specs = append(specs, t.Spec)
	}
	return specs
}

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
