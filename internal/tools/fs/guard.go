package fs

import (
	"fmt"
	"path/filepath"
	"strings"
)

// guardAgentManagedPaths hard-refuses writes/edits to paths that kcpos
// treats as agent-managed sources of truth. K/graph.json (+ K/graph.*)
// is the graph's SoT — modifying it directly desyncs the graph from
// evidence/diff capture and bypasses the v11 process-justice invariants
// (e.g. confirm_object's mechanical oracle). K/frags/* is session_build's
// output — implContent must flow through graph_merge_object so the chain
// can read it.
//
// Used by write_file and edit. Keep the two tools' refusal behaviour in
// sync here so a future tool author can't widen the bypass surface by
// adding another mutator without re-applying the guard.
func guardAgentManagedPaths(toolName, path string) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	if dir == "K" || strings.HasPrefix(dir, "K/") {
		if base == "graph.json" || strings.HasPrefix(base, "graph.") {
			return fmt.Errorf("%s: K/graph.json 是图的 source of truth，不能直接写。修改图结构请用 graph_merge_object 或 graph_link_* 工具。", toolName)
		}
	}
	if strings.HasPrefix(path, "K/frags/") {
		return fmt.Errorf("%s: K/frags/* 已废弃（v10）。代码内容请通过 graph_merge_object patch='{impl_content:\"...\"}' 写入，图会持有源码内容。", toolName)
	}
	return nil
}
