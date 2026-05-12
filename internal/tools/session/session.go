package sessiontools

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/creator915/Koncept_OS/internal/llm"
	"github.com/creator915/Koncept_OS/internal/checkpoint"
	"github.com/creator915/Koncept_OS/internal/graph"
	"github.com/creator915/Koncept_OS/internal/session"
)

// Session tools manage work-sessions stored under K/sessions/. These are
// distinct from the chat transcript — they track units of design /
// implementation work over the hypergraph (lifecycle, parent/child tree,
// graphDiff for rollback).

func sessionCreateTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
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

func sessionStartTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "session_start",
				Description: "Atomically create + activate + focus a new KonceptOS work-session. Equivalent to session_create → session_status active → session_focus, but inseparable so any graph mutations you make next get correctly recorded to this session's graphDiff. **Use this instead of the three-step combo whenever you're starting fresh work** — it eliminates the common bug where a session is created but graph operations happen before focus, leaving graphDiff empty.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":     map[string]interface{}{"type": "string", "description": "Session id (s_ auto-prepended if missing)."},
						"parent": map[string]interface{}{"type": "string", "description": "Parent session id, or empty for a root session."},
						"task":   map[string]interface{}{"type": "string", "description": "Brief task description."},
						"signatures": map[string]interface{}{
							"type":        "array",
							"items":       map[string]interface{}{"type": "string"},
							"description": "Optional. Graph object IDs this session will work on.",
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
			s, err := session.Start(session.DefaultDir, id, parent, task, input)
			if err != nil {
				return "", err
			}
			parentInfo := ""
			if s.Parent != "" {
				parentInfo = " · parent=" + s.Parent
			}
			return fmt.Sprintf("started %s · status=active · focused%s", s.ID, parentInfo), nil
		},
	}
}

func sessionShowTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
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

func sessionListTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
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

func sessionStatusTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "session_status",
				Description: "Transition a session's status. Valid moves: waiting→active (start work), active→finished (succeeded). Other moves error — to abandon a session use session_delete.\n\nFinish guard: when transitioning a ROOT session to finished, the same checks as session_gate_check run inline; gate FAIL is a hard rejection. This stops the failure mode where a stuck agent flips status to finished to escape an unsatisfied gate. Non-root sessions skip the guard (their gate enforcement happens at the root level via children-finished).",
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
			// Gate guard (2026-05-09 v6 finding): agents reaching a sticky
			// gate FAIL would call session_status active→finished directly,
			// bypassing typecalc-test-required / accepted-evidence-required
			// and shipping unverified work. The gate check is the SAME one
			// session_gate_check exposes; we just refuse to advance status
			// when it fails for a root session. Non-root sessions are
			// trusted to gate at their parent level (children-finished
			// rule already cascades to root) and skip this guard.
			if session.Status(to) == session.StatusFinished {
				existing, lerr := session.Load(session.DefaultDir, id)
				if lerr == nil && existing.Parent == "" {
					report, gerr := session.CheckGate(session.DefaultDir, graph.DefaultPath, checkpoint.DefaultPath, id)
					if gerr == nil && report != nil && report.Status != "PASS" {
						return "", fmt.Errorf(
							"refusing to mark root session %s as finished: gate FAIL with %d issue(s) — fix them first or call session_gate_check for the full list. Top issues:\n  %s",
							id, len(report.Issues), strings.Join(truncateIssues(report.Issues, 5), "\n  "),
						)
					}
				}
			}
			s, err := session.SetStatus(session.DefaultDir, id, session.Status(to))
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%s status → %s", s.ID, s.Status), nil
		},
	}
}

func truncateIssues(issues []string, n int) []string {
	if len(issues) <= n {
		return issues
	}
	out := append([]string{}, issues[:n]...)
	out = append(out, fmt.Sprintf("... (%d more — call session_gate_check for full list)", len(issues)-n))
	return out
}

func sessionDeleteTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "session_delete",
				Description: "Roll back a session: depth-first roll back all children (reverse-applying their graphDiff to K/graph.json), then reverse-apply this session's graphDiff, then delete the session JSON. Also deletes def/impl files this session created. Use when a session fails or is abandoned. If the rolled-back session was the focused one, focus is cleared.",
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

func sessionFocusTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
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

func sessionAggregateTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "session_aggregate",
				Description: "Walk a session and all its descendants, dedupe-merging their output.{implementations, newSignatures, newAttributes, tests} into the named session. Use this on the root session before final gate-check. graphDiff is intentionally NOT merged — each session keeps its own for rollback purposes.",
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

func sessionSetArchitectureTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "session_set_architecture",
				Description: "Write the architecture description for a session — a markdown-style list of sub-modules and intermediate variables produced BEFORE any implementation code is written. Required for root session finish (gate rule [architecture-non-empty]).\n\nCLAUDE.md §5.4 path A: \"even if a one-shot implementation, first list sub-modules and intermediate variables\". This step is the design artifact that justifies the hypergraph structure that follows. Format is free-form markdown — typical content: a bullet list of sub-modules with their responsibilities, and a bullet list of intermediate variables with their roles.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":          map[string]interface{}{"type": "string", "description": "Session id."},
						"description": map[string]interface{}{"type": "string", "description": "Markdown description of sub-modules + intermediate variables."},
					},
					"required": []string{"id", "description"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			rawID, _ := args["id"].(string)
			description, _ := args["description"].(string)
			if description == "" {
				return "", fmt.Errorf("description required")
			}
			id, err := session.NormalizeID(rawID)
			if err != nil {
				return "", err
			}
			s, err := session.SetArchitecture(session.DefaultDir, id, description)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("architecture set on %s (%d chars)", s.ID, len(s.Output.Architecture)), nil
		},
	}
}

func sessionGateCheckTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "session_gate_check",
				Description: "Verify a SESSION is ready to be finished. Cross-object scope: children finished or deleted; aggregated outputs; attribute backfill; checkpoint PASS; waiver-flood threshold; root architecture set. Per-object rules (impl-on-disk, evidence-pass, accepted-evidence-required) are delegated to gate_object — check individual objects with that tool for finer-grained feedback. Mechanical verification only.",
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

// gateObjectTool is the v8.8 per-object early-feedback gate. Same
// rules as the per-object branch of session_gate_check, exposed as a
// standalone tool so the agent can verify one object at a time without
// running the full root walk. Also automatically invoked by the
// graph_merge_object after-hook on status=confirmed transitions so the
// agent sees object-level issues immediately rather than discovering
// them at root-finish time.
func gateObjectTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "gate_object",
				Description: "Run the gate's per-object checks against ONE graph object: confirmed status, impl on disk, produces-or-mutates non-empty, typecalc evidence present and passing (or substituted by obstacle+waiver), reasonableness review accepted (or waived). Use this for early feedback while iterating on a single object — the same rules that the root-finish gate runs at the end. The graph_merge_object hook also auto-calls this on status=confirmed transitions, so most usage is reactive (read the hook output) rather than ad-hoc.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object_id": map[string]interface{}{"type": "string", "description": "Graph object id to check."},
					},
					"required": []string{"object_id"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			objID, _ := args["object_id"].(string)
			if objID == "" {
				return "", fmt.Errorf("object_id is required")
			}
			g, err := graph.LoadOrInit(graph.DefaultPath)
			if err != nil {
				return "", err
			}
			cwd, _ := os.Getwd()
			issues, info := session.CheckObjectGate(g, objID, cwd)
			var b strings.Builder
			status := "PASS"
			if len(issues) > 0 {
				status = "FAIL"
			}
			fmt.Fprintf(&b, "gate_object: %s · %s (has_evidence=%v, pass_via_waiver=%v)\n", status, objID, info.HasEvidence, info.PassViaWaiver)
			for _, iss := range issues {
				fmt.Fprintf(&b, "  ✗ %s\n", iss)
			}
			if len(issues) == 0 {
				fmt.Fprintln(&b, "  (no per-object issues — object passes the object-level gate)")
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
