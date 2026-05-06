package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/creator915/Koncept_OS/internal/chat"
	"github.com/creator915/Koncept_OS/internal/checkpoint"
	"github.com/creator915/Koncept_OS/internal/graph"
	"github.com/creator915/Koncept_OS/internal/session"
)

// Session tools manage KonceptOS work-sessions stored under K/sessions/.
// These are distinct from the chat transcript — they track units of design /
// implementation work over the hypergraph (lifecycle, parent/child tree,
// future graphDiff). Per CLAUDE.md §5.

func sessionCreateTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "session_create",
				Description: "Create a new KonceptOS work-session (lives in K/sessions/, distinct from the chat transcript). Status starts as 'waiting'. Pass parent='' for a root session, or parent=<existing session id> to make this a child. Provide task (one-line description), and optionally input.signatures (graph object IDs this session will work on) and input.context (graph attribute IDs in scope). Returns the session id.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id": map[string]interface{}{
							"type":        "string",
							"description": "Session id, e.g. 'weather_proc' or 's_weather_proc'. Lowercase letter then alphanumerics/underscores. The 's_' prefix is auto-prepended if missing.",
						},
						"parent": map[string]interface{}{
							"type":        "string",
							"description": "Parent session id, or empty for a root session. Parent must already exist.",
						},
						"task": map[string]interface{}{
							"type":        "string",
							"description": "Brief description of what this session will accomplish.",
						},
						"signatures": map[string]interface{}{
							"type":        "array",
							"items":       map[string]interface{}{"type": "string"},
							"description": "Optional. Graph object IDs the session is responsible for.",
						},
						"context": map[string]interface{}{
							"type":        "array",
							"items":       map[string]interface{}{"type": "string"},
							"description": "Optional. Graph attribute IDs in scope.",
						},
					},
					"required": []string{"id", "task"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			rawID, _ := args["id"].(string)
			id, err := session.NormalizeID(rawID)
			if err != nil {
				return "", err
			}
			parent, _ := args["parent"].(string)
			parent = strings.TrimSpace(parent)
			task, _ := args["task"].(string)
			if task == "" {
				return "", fmt.Errorf("task required")
			}
			input := session.Input{
				Signatures: stringList(args["signatures"]),
				Context:    stringList(args["context"]),
			}
			s, err := session.Create(session.DefaultDir, id, parent, task, input)
			if err != nil {
				return "", err
			}
			parentInfo := ""
			if s.Parent != "" {
				parentInfo = " (child of " + s.Parent + ")"
			}
			return fmt.Sprintf("created session %s%s · status=waiting", s.ID, parentInfo), nil
		},
	}
}

func sessionShowTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "session_show",
				Description: "Show one KonceptOS session: id, status, parent, children, task, input/output summary. Pass the session id.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id": map[string]interface{}{"type": "string"},
					},
					"required": []string{"id"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			rawID, _ := args["id"].(string)
			id, err := session.NormalizeID(rawID)
			if err != nil {
				return "", err
			}
			s, err := session.Load(session.DefaultDir, id)
			if err != nil {
				return "", err
			}
			return formatSession(s), nil
		},
	}
}

func sessionListTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "session_list",
				Description: "List KonceptOS sessions. Optional status filter (waiting/active/finished); omit to list all. Returns a table-style summary, one session per line.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"status": map[string]interface{}{
							"type":        "string",
							"description": "Optional filter: waiting | active | finished. Empty/absent = no filter.",
						},
					},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			ids, err := session.List(session.DefaultDir)
			if err != nil {
				return "", err
			}
			filter, _ := args["status"].(string)
			if len(ids) == 0 {
				return "no sessions", nil
			}
			var rows []string
			for _, id := range ids {
				s, err := session.Load(session.DefaultDir, id)
				if err != nil {
					rows = append(rows, fmt.Sprintf("  %s · [load error: %v]", id, err))
					continue
				}
				if filter != "" && string(s.Status) != filter {
					continue
				}
				parent := s.Parent
				if parent == "" {
					parent = "<root>"
				}
				rows = append(rows, fmt.Sprintf("  %s · %s · parent=%s · children=%d · %s",
					s.ID, s.Status, parent, len(s.Children), truncateText(s.Task, 60)))
			}
			if len(rows) == 0 {
				return fmt.Sprintf("no sessions matching status=%s", filter), nil
			}
			return strings.Join(rows, "\n"), nil
		},
	}
}

func sessionStatusTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "session_status",
				Description: "Transition a session's status. Valid moves: waiting→active (start work), active→finished (succeeded). Other moves error — to abandon a session use session_delete.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":     map[string]interface{}{"type": "string"},
						"status": map[string]interface{}{"type": "string", "description": "Target: active | finished"},
					},
					"required": []string{"id", "status"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			rawID, _ := args["id"].(string)
			id, err := session.NormalizeID(rawID)
			if err != nil {
				return "", err
			}
			to, _ := args["status"].(string)
			s, err := session.SetStatus(session.DefaultDir, id, session.Status(to))
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%s status → %s", s.ID, s.Status), nil
		},
	}
}

func sessionDeleteTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "session_delete",
				Description: "Roll back a session: depth-first roll back all children (reverse-applying their graphDiff to K/graph.json), then reverse-apply this session's graphDiff, then delete the session JSON. Per CLAUDE.md §5.3. Use when a session fails or is abandoned. If the rolled-back session was the focused one, focus is cleared.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id": map[string]interface{}{"type": "string"},
					},
					"required": []string{"id"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			rawID, _ := args["id"].(string)
			id, err := session.NormalizeID(rawID)
			if err != nil {
				return "", err
			}
			deleted, err := session.Rollback(session.DefaultDir, graph.DefaultPath, id)
			if err != nil {
				return "", err
			}
			if len(deleted) == 0 {
				return fmt.Sprintf("no session %s found (already gone)", id), nil
			}
			return fmt.Sprintf("rolled back %d session(s): %s · graphDiff reverse-applied to %s",
				len(deleted), strings.Join(deleted, ", "), graph.DefaultPath), nil
		},
	}
}

func sessionFocusTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "session_focus",
				Description: "Set the currently-focused session. While a session is focused AND active, every mutating graph_* call appends its diff to that session's graphDiff (used later for rollback). Pass id=<session id> to set focus, or id='' to clear it. The session must already be in 'active' status.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id": map[string]interface{}{
							"type":        "string",
							"description": "Session id to focus on, or empty string to clear focus.",
						},
					},
					"required": []string{"id"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			rawID, _ := args["id"].(string)
			rawID = strings.TrimSpace(rawID)
			if rawID == "" {
				if err := session.SetFocus(session.DefaultDir, ""); err != nil {
					return "", err
				}
				return "focus cleared · graph mutations no longer recorded to any session", nil
			}
			id, err := session.NormalizeID(rawID)
			if err != nil {
				return "", err
			}
			s, err := session.Load(session.DefaultDir, id)
			if err != nil {
				return "", err
			}
			if s.Status != session.StatusActive {
				return "", fmt.Errorf("cannot focus on %s: status is %s, must be active first (use session_status to transition)", id, s.Status)
			}
			if err := session.SetFocus(session.DefaultDir, id); err != nil {
				return "", err
			}
			return fmt.Sprintf("focus → %s · subsequent graph mutations recorded to its graphDiff", id), nil
		},
	}
}

func sessionAggregateTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "session_aggregate",
				Description: "Walk a session and all its descendants, dedupe-merging their output.{implementations, newSignatures, newAttributes, tests} into the named session. Use this on the root session before final gate-check (CLAUDE.md §5.5 R1). graphDiff is intentionally NOT merged — each session keeps its own for rollback purposes.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id": map[string]interface{}{"type": "string", "description": "Session id to aggregate into."},
					},
					"required": []string{"id"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			rawID, _ := args["id"].(string)
			id, err := session.NormalizeID(rawID)
			if err != nil {
				return "", err
			}
			s, err := session.Aggregate(session.DefaultDir, id)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("aggregated into %s · implementations=%d, signatures=%d, attributes=%d, tests=%d",
				s.ID, len(s.Output.Implementations), len(s.Output.NewSignatures),
				len(s.Output.NewAttributes), len(s.Output.Tests)), nil
		},
	}
}

func sessionGateCheckTool() Tool {
	return Tool{
		Spec: chat.ToolSpec{
			Type: "function",
			Function: chat.ToolFunction{
				Name:        "session_gate_check",
				Description: "Verify a session is ready to be finished, per CLAUDE.md §5.1.1 + §5.5 R5. Checks: all children finished or deleted; every object in graphDiff.added is confirmed with impl file on disk (non-empty); session has aggregated outputs if it has children; checkpoint (if any) is frozen with FinalVerdict=PASS. Returns a PASS/FAIL report listing each violation. NOTE: skips gameplayProof check (§5.1.1#7) per the convergent variant.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id": map[string]interface{}{"type": "string", "description": "Session id to gate-check (typically the root)."},
					},
					"required": []string{"id"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			rawID, _ := args["id"].(string)
			id, err := session.NormalizeID(rawID)
			if err != nil {
				return "", err
			}
			r, err := session.CheckGate(session.DefaultDir, graph.DefaultPath, checkpoint.DefaultPath, id)
			if err != nil {
				return "", err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "gate: %s · session %s\n", r.Status, r.SessionID)
			if len(r.Issues) == 0 {
				fmt.Fprintln(&b, "  (no issues — session ready to finish)")
				return b.String(), nil
			}
			for _, iss := range r.Issues {
				fmt.Fprintf(&b, "  ✗ %s\n", iss)
			}
			return b.String(), nil
		},
	}
}

func formatSession(s *session.Session) string {
	var b strings.Builder
	fmt.Fprintf(&b, "session %s\n", s.ID)
	fmt.Fprintf(&b, "  status: %s\n", s.Status)
	fmt.Fprintf(&b, "  task: %s\n", s.Task)
	if s.Parent != "" {
		fmt.Fprintf(&b, "  parent: %s\n", s.Parent)
	} else {
		fmt.Fprintf(&b, "  parent: <root>\n")
	}
	if len(s.Children) > 0 {
		sorted := append([]string(nil), s.Children...)
		sort.Strings(sorted)
		fmt.Fprintf(&b, "  children: %s\n", strings.Join(sorted, ", "))
	} else {
		fmt.Fprintf(&b, "  children: (none)\n")
	}
	if len(s.Input.Signatures) > 0 {
		fmt.Fprintf(&b, "  input.signatures: %s\n", strings.Join(s.Input.Signatures, ", "))
	}
	if len(s.Input.Context) > 0 {
		fmt.Fprintf(&b, "  input.context: %s\n", strings.Join(s.Input.Context, ", "))
	}
	if len(s.Output.Implementations) > 0 {
		fmt.Fprintf(&b, "  output.implementations: %s\n", strings.Join(s.Output.Implementations, ", "))
	}
	if len(s.Output.Tests) > 0 {
		fmt.Fprintf(&b, "  output.tests: %s\n", strings.Join(s.Output.Tests, ", "))
	}
	if diff := summarizeGraphDiff(&s.Output.GraphDiff); diff != "" {
		fmt.Fprintf(&b, "  graphDiff: %s\n", diff)
	}
	fmt.Fprintf(&b, "  createdAt: %s\n", s.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "  updatedAt: %s\n", s.UpdatedAt.Format("2006-01-02 15:04:05"))
	return b.String()
}

// summarizeGraphDiff produces a one-line digest of the diff: which ids were
// added / modified / removed, grouped by attributes vs objects. Empty diff
// returns "" so callers can skip the line.
func summarizeGraphDiff(d *session.GraphDiff) string {
	addedAttrs := mapKeys(d.Added.Attributes)
	addedObjs := mapKeys(d.Added.Objects)
	modAttrs := mapKeys(d.Modified.Attributes)
	modObjs := mapKeys(d.Modified.Objects)
	totalAdded := len(addedAttrs) + len(addedObjs)
	totalMod := len(modAttrs) + len(modObjs)
	totalRem := len(d.Removed.Attributes) + len(d.Removed.Objects)
	if totalAdded+totalMod+totalRem == 0 {
		return ""
	}
	var parts []string
	if totalAdded > 0 {
		parts = append(parts, fmt.Sprintf("added=%d", totalAdded))
	}
	if totalMod > 0 {
		parts = append(parts, fmt.Sprintf("modified=%d", totalMod))
	}
	if totalRem > 0 {
		parts = append(parts, fmt.Sprintf("removed=%d", totalRem))
	}
	header := strings.Join(parts, ", ")
	var lines []string
	if len(addedAttrs)+len(addedObjs) > 0 {
		lines = append(lines, "    added:")
		if len(addedAttrs) > 0 {
			sort.Strings(addedAttrs)
			lines = append(lines, "      attributes: "+strings.Join(addedAttrs, ", "))
		}
		if len(addedObjs) > 0 {
			sort.Strings(addedObjs)
			lines = append(lines, "      objects: "+strings.Join(addedObjs, ", "))
		}
	}
	if len(modAttrs)+len(modObjs) > 0 {
		lines = append(lines, "    modified:")
		if len(modAttrs) > 0 {
			sort.Strings(modAttrs)
			lines = append(lines, "      attributes: "+strings.Join(modAttrs, ", "))
		}
		if len(modObjs) > 0 {
			sort.Strings(modObjs)
			lines = append(lines, "      objects: "+strings.Join(modObjs, ", "))
		}
	}
	if totalRem > 0 {
		lines = append(lines, "    removed:")
		if len(d.Removed.Attributes) > 0 {
			lines = append(lines, "      attributes: "+strings.Join(d.Removed.Attributes, ", "))
		}
		if len(d.Removed.Objects) > 0 {
			lines = append(lines, "      objects: "+strings.Join(d.Removed.Objects, ", "))
		}
	}
	if len(lines) == 0 {
		return header
	}
	return header + "\n" + strings.Join(lines, "\n")
}

func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func stringList(v any) []string {
	if v == nil {
		return []string{}
	}
	arr, ok := v.([]any)
	if !ok {
		return []string{}
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
