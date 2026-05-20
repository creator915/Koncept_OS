package sessiontools

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
	"github.com/creator915/Koncept_OS/internal/app/workflow"
	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
)

// Session tools manage work-sessions stored under K/sessions/. These are
// distinct from the chat transcript — they track units of design /
// implementation work over the hypergraph (lifecycle, parent/child tree,
// graphDiff for rollback).

func sessionCreateTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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
			s, err := workflow.Create(persistence.SessionDefaultDir, id, parent, task, input)
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

func sessionStartTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "session_start",
				Description: "Atomically create + activate + focus a new KonceptOS work-session. Equivalent to session_create → session_status active → session_focus, but inseparable so any graph mutations you make next get correctly recorded to this session's graphDiff. **Use this instead of the three-step combo whenever you're starting fresh work** — it eliminates the common bug where a session is created but graph operations happen before focus, leaving graphDiff empty.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":     map[string]interface{}{"type": "string", "description": "Session id (s_ auto-prepended if missing)."},
						"parent": map[string]interface{}{"type": "string", "description": "Parent session id, or empty for a root session."},
						"task":   map[string]interface{}{"type": "string", "description": "Brief task description."},
						"expands_object": map[string]interface{}{"type": "string", "description": "Optional (KonceptOS §1.3). The top-graph object id this session expands into a sub-hypergraph. Must already exist as an object in K/graph.json or session_start is refused. When set, an empty K/expansions/<id>/graph.json is created and subsequent graph-write goes there (active-layer binding); on session_finish the parent object's expansion+confirmed status are set."},
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
			expandsObjectRaw, _ := args["expands_object"].(string)
			expandsObject := strings.TrimSpace(expandsObjectRaw)
			// Validate the expanded object exists in the TOP graph BEFORE
			// creating the session — a session must never be created for a
			// non-existent object (KonceptOS §1.3 precondition).
			if expandsObject != "" {
				tg, gerr := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
				if gerr != nil {
					return "", fmt.Errorf("session_start: load top graph: %w", gerr)
				}
				if _, ok := tg.Objects[expandsObject]; !ok {
					return "", fmt.Errorf("session_start: expands_object %q is not an object in the top hypergraph (K/graph.json) — create it via graph_create_object first", expandsObject)
				}
			}
			input := session.Input{
				Signatures: stringList(args["signatures"]),
				Context:    stringList(args["context"]),
			}
			s, err := workflow.Start(persistence.SessionDefaultDir, id, parent, task, input)
			if err != nil {
				return "", err
			}
			if expandsObject != "" {
				// Record the expansion link on the session and create the
				// empty sub-hypergraph file (KonceptOS §1.3: session-start
				// "创建 K/expansions/{id}/graph.json（空的子超图）").
				// Subsequent graph-write auto-targets it via the focused
				// session's ActiveGraphPath (P1.1.2/P1.2.3).
				s.ExpandsObject = expandsObject
				if serr := persistence.SaveSession(persistence.SessionDefaultDir, s); serr != nil {
					return "", fmt.Errorf("session_start: record expands_object: %w", serr)
				}
				if serr := persistence.SaveExpansionGraph(s.ID, graph.NewGraph()); serr != nil {
					return "", fmt.Errorf("session_start: create empty sub-hypergraph: %w", serr)
				}
			}
			parentInfo := ""
			if s.Parent != "" {
				parentInfo = " · parent=" + s.Parent
			}
			expInfo := ""
			if expandsObject != "" {
				expInfo = " · expands=" + expandsObject + " → " + persistence.ExpansionGraphPath(s.ID)
			}
			return fmt.Sprintf("started %s · status=active · focused%s%s", s.ID, parentInfo, expInfo), nil
		},
	}
}

func sessionShowTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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
			s, err := persistence.LoadSession(persistence.SessionDefaultDir, id)
			if err != nil {
				return "", err
			}
			out := formatSession(s)
			if s.ExpandsObject != "" {
				out += expansionSummary(s.ID, s.ExpandsObject)
			}
			return out, nil
		},
	}
}

func sessionListTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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
			ids, err := persistence.ListSessions(persistence.SessionDefaultDir)
			if err != nil {
				return "", err
			}
			filter, _ := args["status"].(string)
			if len(ids) == 0 {
				return "no sessions", nil
			}
			var rows []string
			for _, id := range ids {
				s, err := persistence.LoadSession(persistence.SessionDefaultDir, id)
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

func sessionStatusTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "session_status",
				Description: "Transition a session's status. Valid moves: waiting→active (start work), active→finished (succeeded). Other moves error — to undo work use session_rollback (destructive), or to retire a finished session entry use session_dismiss (additive cleanup).\n\nFinish guard: when transitioning a ROOT session to finished, the same checks as session_gate_check run inline; gate FAIL is a hard rejection. This stops the failure mode where a stuck agent flips status to finished to escape an unsatisfied gate. Non-root sessions skip the guard (their gate enforcement happens at the root level via children-finished).",
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
				existing, lerr := persistence.LoadSession(persistence.SessionDefaultDir, id)
				if lerr == nil && existing.Parent == "" {
					report, gerr := workflow.CheckGate(persistence.SessionDefaultDir, persistence.GraphDefaultPath, persistence.CheckpointDefaultPath, id)
					if gerr == nil && report != nil && report.Status != "PASS" {
						return "", fmt.Errorf(
							"refusing to mark root session %s as finished: gate FAIL with %d issue(s) — fix them first or call session_gate_check for the full list. Top issues:\n  %s",
							id, len(report.Issues), strings.Join(truncateIssues(report.Issues, 5), "\n  "),
						)
					}
				}
				// Expansion-finish gate (KonceptOS §1.3): an expansion
				// session can only finish when its sub-hypergraph is fully
				// confirmed + validates + children finished + produce/consume
				// correspondence holds. Failure is a LIST of reasons.
				reasons, rerr := workflow.ExpansionFinishReasons(persistence.SessionDefaultDir, persistence.GraphDefaultPath, id)
				if rerr != nil {
					return "", rerr
				}
				if len(reasons) > 0 {
					return "", fmt.Errorf(
						"refusing to finish expansion session %s: gate FAIL with %d reason(s):\n  - %s",
						id, len(reasons), strings.Join(reasons, "\n  - "))
				}
			}
			s, err := workflow.SetStatus(persistence.SessionDefaultDir, id, session.Status(to))
			if err != nil {
				return "", err
			}
			// Success side: confer Expansion + confirmed on the parent
			// object (no-op for non-expansion sessions).
			if session.Status(to) == session.StatusFinished {
				if perr := workflow.PropagateExpansion(persistence.SessionDefaultDir, persistence.GraphDefaultPath, id); perr != nil {
					return "", perr
				}
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

// sessionRollbackTool — v9.3 renamed from session_delete. The name now
// matches the actual semantics. Three Terraria incidents (v9.0.6 03,
// v92-01, v92-02) all came from agents reading "delete" as "cleanup"
// and being surprised that graphDiff + def/impl files got wiped. v92-02
// went furthest: deleted ROOT session, lost 20 source files including
// index.html itself.
//
// Companion: sessionDismissTool() — the safe additive cleanup most
// agents actually want.
func sessionRollbackTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "session_rollback",
				Description: "**DESTRUCTIVE — reverses everything this session did.** Depth-first rolls back all child sessions (reverse-applying their graphDiff), then reverse-applies this session's graphDiff, then deletes def/impl files this session created, then removes the session JSON. Use ONLY when the work is genuinely wrong and you want to undo it.\n\nFor cleaning up a *successfully* finished session, use `session_dismiss` instead — it only removes the session entry without touching graphDiff or files.\n\nIf the rolled-back session is the focused one, focus is cleared. Rolling back ROOT empties the entire project.",
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
			deleted, err := workflow.Rollback(persistence.SessionDefaultDir, persistence.GraphDefaultPath, id)
			if err != nil {
				return "", err
			}
			if len(deleted) == 0 {
				return fmt.Sprintf("no session %s found (already gone)", id), nil
			}
			return fmt.Sprintf("rolled back %d session(s): %s · graphDiff reverse-applied to %s",
				len(deleted), strings.Join(deleted, ", "), persistence.GraphDefaultPath), nil
		},
	}
}

// sessionDismissTool — v9.3 new. Removes a session entry from K/sessions/
// without rolling back anything. Use when a subagent finished its work
// successfully and you want to retire its session record. Does NOT touch
// graphDiff, def files, impl files, or any other state — only the session
// JSON. Refuses to dismiss a session that's still `active` (you should
// transition to `finished` first via session_status).
func sessionDismissTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "session_dismiss",
				Description: "Remove a finished session's entry from K/sessions/ without touching anything else. **Additive cleanup, NOT destructive.** Does NOT roll back graphDiff. Does NOT delete def/impl files. Does NOT affect child sessions. Use this when a subagent has completed its work and you want to retire the session record. The session must be in status `finished` or `waiting` (refuses `active`). If you want destructive rollback, use `session_rollback` instead.",
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
			s, err := persistence.LoadSession(persistence.SessionDefaultDir, id)
			if err != nil {
				return "", fmt.Errorf("session_dismiss: load %s: %w", id, err)
			}
			if s.Status == session.StatusActive {
				return "", fmt.Errorf("session_dismiss refuses to remove %q: status=active. Transition to finished first via session_status, OR use session_rollback for destructive cleanup", id)
			}
			// Refuse to dismiss a session that still has active children
			// hanging off it — that would orphan them.
			var active []string
			for _, c := range s.Children {
				if cs, err := persistence.LoadSession(persistence.SessionDefaultDir, c); err == nil && cs.Status == session.StatusActive {
					active = append(active, c)
				}
			}
			if len(active) > 0 {
				return "", fmt.Errorf("session_dismiss refuses to remove %q: %d child session(s) still active: %s. Finish or dismiss children first", id, len(active), strings.Join(active, ", "))
			}
			if err := persistence.DeleteSession(persistence.SessionDefaultDir, id); err != nil {
				return "", fmt.Errorf("session_dismiss: delete %s: %w", id, err)
			}
			return fmt.Sprintf("dismissed session %s (status was %s) — session JSON removed, graphDiff and files intact", id, s.Status), nil
		},
	}
}

func sessionFocusTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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
				if err := persistence.SetFocus(persistence.SessionDefaultDir, ""); err != nil {
					return "", err
				}
				return "focus cleared · graph mutations no longer recorded to any session", nil
			}
			id, err := session.NormalizeID(rawID)
			if err != nil {
				return "", err
			}
			s, err := persistence.LoadSession(persistence.SessionDefaultDir, id)
			if err != nil {
				return "", err
			}
			if s.Status != session.StatusActive {
				return "", fmt.Errorf("cannot focus on %s: status is %s, must be active first (use session_status to transition)", id, s.Status)
			}
			if err := persistence.SetFocus(persistence.SessionDefaultDir, id); err != nil {
				return "", err
			}
			return fmt.Sprintf("focus → %s · subsequent graph mutations recorded to its graphDiff", id), nil
		},
	}
}

func sessionAggregateTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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
			s, err := workflow.Aggregate(persistence.SessionDefaultDir, id)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("aggregated into %s · implementations=%d, signatures=%d, attributes=%d, tests=%d",
				s.ID, len(s.Output.Implementations), len(s.Output.NewSignatures),
				len(s.Output.NewAttributes), len(s.Output.Tests)), nil
		},
	}
}

func sessionSetArchitectureTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
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
			s, err := workflow.SetArchitecture(persistence.SessionDefaultDir, id, description)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("architecture set on %s (%d chars)", s.ID, len(s.Output.Architecture)), nil
		},
	}
}

func sessionGateCheckTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "session_gate_check",
				Description: "Verify a SESSION is ready to be finished. Cross-object scope: children finished or deleted; aggregated outputs; attribute backfill; checkpoint PASS; root architecture set. Per-object rules (impl-on-disk, evidence-pass, accepted-evidence-required) are delegated to gate_object — check individual objects with that tool for finer-grained feedback. Mechanical verification only. (v9.2: waiver mechanism removed — the gate is now binary; v11: status=confirmed only via confirm_object, never hand-set.)",
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
			r, err := workflow.CheckGate(persistence.SessionDefaultDir, persistence.GraphDefaultPath, persistence.CheckpointDefaultPath, id)
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
func gateObjectTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "gate_object",
				Description: "Run the gate's per-object checks against ONE graph object: confirmed status, impl on disk, produces-or-mutates non-empty, typecalc evidence present and passing, reasonableness review accepted. Use this for early feedback while iterating on a single object — the same rules that the root-finish gate runs at the end. The graph_merge_object hook also auto-calls this on status=confirmed transitions, so most usage is reactive (read the hook output) rather than ad-hoc. (v9.2: obstacle+waiver substitute path removed — gate is binary.)",
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
			g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
			if err != nil {
				return "", err
			}
			cwd, _ := os.Getwd()
			issues, info := workflow.CheckObjectGate(g, objID, cwd)
			var b strings.Builder
			status := "PASS"
			if len(issues) > 0 {
				status = "FAIL"
			}
			fmt.Fprintf(&b, "gate_object: %s · %s (has_evidence=%v)\n", status, objID, info.HasEvidence)
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

// expansionSummary (P1.3.4) renders the sub-hypergraph digest for an
// expansion session: object count, confirmed count, validate status.
// Read-only.
func expansionSummary(sid, expandsObject string) string {
	sub, _ := persistence.LoadExpansionGraphOrInit(sid)
	confirmed := 0
	for _, o := range sub.Objects {
		if o.Status == graph.StatusConfirmed {
			confirmed++
		}
	}
	cwd, _ := os.Getwd()
	vstatus := "PASS"
	if sub.Validate(cwd).HasErrors() {
		vstatus = "FAIL"
	}
	return fmt.Sprintf("  expansion: %s → %s\n  sub-objects: %d (confirmed %d)\n  sub-validate: %s\n",
		expandsObject, persistence.ExpansionGraphPath(sid), len(sub.Objects), confirmed, vstatus)
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
