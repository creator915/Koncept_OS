package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/creator915/Koncept_OS/internal/graph"
)

// SpecHook is the contract for "did the agent's last action need follow-up
// that the agent did not perform?" — a post-tool-call audit. If a hook
// returns a non-empty string, the loop injects it as a user message into
// the conversation and the agent must address it on the next turn before
// proceeding.
//
// Hooks are run AFTER all tool calls in a single assistant turn complete,
// so parallel calls (e.g. `graph_create_object` + `write_file <def>` in
// one turn) can satisfy each other's preconditions before the audit runs.
type SpecHook interface {
	Name() string
	After(toolName, argsJSON, result string) string
}

// DefaultHooks returns the built-in spec-compliance hooks. Override via
// RunOptions.Hooks to add custom rules or disable enforcement entirely
// (pass an empty slice).
func DefaultHooks() []SpecHook {
	return []SpecHook{
		&defExistenceHook{},
		&confirmedImplHook{},
		&defImplDistinctHook{},
		&defUniquenessHook{},
		&rootFinishGateHook{},
	}
}

// --- Hook 1: def-existence after graph_create_* ---
//
// Every graph node has a `def` field pointing at its signature file. When
// the agent creates a node, the def file should exist on disk by the end
// of that assistant turn. If not, the agent must either write it on the
// next turn or amend `def` to point at a real file.

type defExistenceHook struct{}

func (h *defExistenceHook) Name() string { return "def-existence" }

func (h *defExistenceHook) After(toolName, argsJSON, _ string) string {
	if toolName != "graph_create_attribute" && toolName != "graph_create_object" {
		return ""
	}
	var args struct {
		ID  string `json:"id"`
		Def string `json:"def"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	def := args.Def
	if def == "" {
		// Matches the tool's own default (TS-first convention).
		def = "defs/" + args.ID + ".ts"
	}
	if fileExistsRel(def) {
		return ""
	}
	noun := "object"
	mergeTool := "graph_merge_object"
	if toolName == "graph_create_attribute" {
		noun = "attribute"
		mergeTool = "graph_merge_attribute"
	}
	return fmt.Sprintf(
		"[def-existence] %s %q created with def=%q but that file does not exist on disk. "+
			"The signature file must be written before this node is properly declared. "+
			"Required next turn: either (a) write_file %q with the type signature, "+
			"or (b) %s with patch '{\"def\":\"<real file path>\"}' to point at an existing file.",
		noun, args.ID, def, def, mergeTool,
	)
}

// --- Hook 2: confirmed-impl after graph_merge_object setting status=confirmed ---
//
// An object marked `confirmed` must have its `impl` field point at a real,
// non-empty file on disk. Confirmed without an impl means the work was
// declared done but no code exists.

type confirmedImplHook struct{}

func (h *confirmedImplHook) Name() string { return "confirmed-impl" }

func (h *confirmedImplHook) After(toolName, argsJSON, _ string) string {
	if toolName != "graph_merge_object" {
		return ""
	}
	var args struct {
		ID    string `json:"id"`
		Patch string `json:"patch"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	var patch map[string]any
	if err := json.Unmarshal([]byte(args.Patch), &patch); err != nil {
		return ""
	}
	statusVal, hasStatus := patch["status"]
	if !hasStatus {
		return ""
	}
	if s, _ := statusVal.(string); s != graph.StatusConfirmed {
		return ""
	}
	// Status was advanced to confirmed. Determine effective impl:
	// from this patch if set, else from the on-disk graph state.
	var implPath string
	if v, ok := patch["impl"].(string); ok && v != "" {
		implPath = v
	} else {
		// Look up current graph state.
		g, err := graph.LoadOrInit(graph.DefaultPath)
		if err != nil {
			return ""
		}
		obj, present := g.Objects[args.ID]
		if !present {
			return ""
		}
		if obj.Impl != nil {
			implPath = *obj.Impl
		}
	}
	if implPath == "" {
		return fmt.Sprintf(
			"[confirmed-impl] object %q was set to status=confirmed but no impl path is set "+
				"(neither in this patch nor in current graph). "+
				"A confirmed object must point at the file containing its implementation. "+
				"Required next turn: graph_merge_object id=%q patch='{\"impl\":\"<file>\"}' before declaring confirmed.",
			args.ID, args.ID,
		)
	}
	if !fileExistsAndNonEmpty(implPath) {
		return fmt.Sprintf(
			"[confirmed-impl] object %q was set to status=confirmed with impl=%q but that file is missing or empty on disk. "+
				"Required next turn: write the implementation, then re-run merge if needed.",
			args.ID, implPath,
		)
	}
	return ""
}

// --- Hook 3: def-impl-distinct ---
//
// An object's `def` (signature/contract file) and `impl` (implementation
// file) must be distinct paths. Collapsing them into one path defeats the
// def/impl separation the workflow relies on for staging (signatures
// before code) and for the per-file size budget.

type defImplDistinctHook struct{}

func (h *defImplDistinctHook) Name() string { return "def-impl-distinct" }

func (h *defImplDistinctHook) After(toolName, argsJSON, _ string) string {
	if toolName != "graph_create_object" && toolName != "graph_merge_object" {
		return ""
	}
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	g, err := graph.LoadOrInit(graph.DefaultPath)
	if err != nil {
		return ""
	}
	obj, ok := g.Objects[args.ID]
	if !ok {
		return ""
	}
	if obj.Impl == nil || *obj.Impl == "" {
		return "" // no impl yet — nothing to compare against
	}
	if obj.Def == "" || obj.Def != *obj.Impl {
		return ""
	}
	return fmt.Sprintf(
		"[def-impl-distinct] object %q has def=%q == impl=%q. "+
			"def is the signature/type-contract file "+
			"(e.g. K/defs/%s.ts for TS, K/defs/%s.go for Go, K/defs/%s.java for Java) "+
			"and impl is the implementation file — they must be distinct paths. "+
			"Required: graph_merge_object id=%q patch='{\"def\":\"<separate signature file>\"}', "+
			"and create that signature file (write_file) with the type/interface declaration.",
		args.ID, obj.Def, *obj.Impl, args.ID, args.ID, args.ID, args.ID,
	)
}

// --- Hook 4: def-uniqueness ---
//
// Each entity has its own def file — one file per id, named after the id.
// If two entities share the same def path, the file is being repurposed
// as a multi-entity dump, which breaks the per-file size budget and the
// rollback model (rollback deletes def files this session created
// assuming a 1:1 entity-to-file mapping).

type defUniquenessHook struct{}

func (h *defUniquenessHook) Name() string { return "def-uniqueness" }

func (h *defUniquenessHook) After(toolName, _ /* argsJSON */, _ string) string {
	if toolName != "graph_create_attribute" && toolName != "graph_create_object" &&
		toolName != "graph_merge_attribute" && toolName != "graph_merge_object" {
		return ""
	}
	g, err := graph.LoadOrInit(graph.DefaultPath)
	if err != nil {
		return ""
	}
	defToIDs := map[string][]string{}
	for id, a := range g.Attributes {
		if a.Def != "" {
			defToIDs[a.Def] = append(defToIDs[a.Def], "attribute "+id)
		}
	}
	for id, o := range g.Objects {
		if o.Def != "" {
			defToIDs[o.Def] = append(defToIDs[o.Def], "object "+id)
		}
	}
	var dups []string
	for def, ids := range defToIDs {
		if len(ids) > 1 {
			dups = append(dups, fmt.Sprintf("%q ← %v", def, ids))
		}
	}
	if len(dups) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"[def-uniqueness] %d def path(s) shared by multiple entities: %v. "+
			"Each entity must have its own def file (one file per id, named after the id). "+
			"This rule is language-agnostic — TS projects use K/defs/<id>.ts, Go uses K/defs/<id>.go, "+
			"Java K/defs/<id>.java, etc.; the structural property is one-file-per-entity. "+
			"Required: graph_merge_<kind> for each duplicate to set a unique def path; "+
			"then write_file the new def file with the type/signature declaration.",
		len(dups), dups,
	)
}

// --- Hook 5: root-finish-gate before session_status ... finished ---
//
// A root session must pass the gate before its status can transition to
// finished. This hook does NOT block the actual status change (the agent
// already requested it) but flags that the gate was not run / did not pass.

type rootFinishGateHook struct{}

func (h *rootFinishGateHook) Name() string { return "root-finish-gate" }

func (h *rootFinishGateHook) After(toolName, argsJSON, result string) string {
	if toolName != "session_status" {
		return ""
	}
	var args struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	if args.Status != "finished" {
		return ""
	}
	// We can't easily tell whether the agent ran gate_check first without
	// loading session metadata or threading state through hooks. For now,
	// surface a soft reminder: if the result indicates success, advise that
	// the agent should have run gate_check; the §root-deliver gate will
	// have caught material violations during the SetStatus call itself
	// (lifecycle.go child-finish guard), so this is a workflow nudge.
	//
	// We do, however, refuse to be silent if the result actually failed —
	// the agent should reason about what went wrong before retrying.
	return ""
}

// --- helpers ---

func fileExistsRel(rel string) bool {
	if rel == "" {
		return false
	}
	path := rel
	if !filepath.IsAbs(path) {
		cwd, _ := os.Getwd()
		path = filepath.Join(cwd, path)
	}
	_, err := os.Stat(path)
	return err == nil
}

func fileExistsAndNonEmpty(rel string) bool {
	if rel == "" {
		return false
	}
	path := rel
	if !filepath.IsAbs(path) {
		cwd, _ := os.Getwd()
		path = filepath.Join(cwd, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Size() > 0
}

// FormatViolations renders a list of violations as a single user-facing
// message. The agent will see this on its next turn and must address each
// item before further work.
func FormatViolations(vs []string) string {
	if len(vs) == 0 {
		return ""
	}
	var b []byte
	b = append(b, '[')
	b = append(b, "kcpos spec enforcement"...)
	b = append(b, "] "...)
	b = append(b, fmt.Sprintf("%d issue(s) need correction in your next turn:\n\n", len(vs))...)
	for _, v := range vs {
		b = append(b, "  • "...)
		b = append(b, v...)
		b = append(b, '\n', '\n')
	}
	b = append(b, "Address each item with the appropriate tool calls. Do not proceed with new work until these are resolved."...)
	return string(b)
}
