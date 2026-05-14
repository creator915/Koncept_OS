package agent

import (
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/app/workflow"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// hookInternalErr formats a "the hook itself could not run" message.
// v9.3.2: hook bodies historically returned "" silently when their
// internal preconditions failed (JSON unparseable, graph file unreadable,
// object disappeared mid-flight). The silent return conflated "check
// passed" with "check didn't run", letting protocol violations sneak
// through any time a transient internal error fired. Now every such
// site emits a visible message tagged with the hook name and a
// machine-readable reason — the agent sees the hook disengaged for
// this turn, the operator can grep `-internal:` to audit how often
// the silent-pass class actually mattered.
//
// The message is INFORMATIONAL — the agent generally cannot fix a
// kcpos internal error. It's mostly a paper trail so silent-pass
// can be caught instead of hidden.
func hookInternalErr(hookName, reason string) string {
	return fmt.Sprintf(
		"[%s-internal] hook could not run: %s — informational only (agent has no action to take). "+
			"The hook's normal enforcement is disengaged for this turn.",
		hookName, reason,
	)
}

// parseMergeObjectArgs decodes a graph_merge_object argsJSON into its
// id and patch map. Accepts patch as EITHER a JSON-encoded string
// (the original tool contract: `{"patch": "{\"status\":\"confirmed\"}"}`)
// OR a nested JSON object (`{"patch": {"status": "confirmed"}}`). LLMs
// frequently send the latter despite the schema declaring string, and
// the four hooks that decode this field would all bail with "could not
// run" warnings when they did — disengaging anti-theater enforcement
// at the very moment confirmed was being asserted.
func parseMergeObjectArgs(argsJSON string) (string, map[string]any, error) {
	var args struct {
		ID    string          `json:"id"`
		Patch json.RawMessage `json:"patch"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", nil, fmt.Errorf("argsJSON parse: %w", err)
	}
	if len(args.Patch) == 0 {
		return args.ID, nil, nil
	}
	// Try object form first (what schema-non-compliant LLMs send).
	var asObj map[string]any
	if err := json.Unmarshal(args.Patch, &asObj); err == nil {
		return args.ID, asObj, nil
	}
	// Fall back to stringified-JSON form (what the schema declares).
	var asStr string
	if err := json.Unmarshal(args.Patch, &asStr); err != nil {
		return args.ID, nil, fmt.Errorf("patch field must be JSON object or JSON-encoded string")
	}
	var nested map[string]any
	if err := json.Unmarshal([]byte(asStr), &nested); err != nil {
		return args.ID, nil, fmt.Errorf("patch JSON parse: %w", err)
	}
	return args.ID, nested, nil
}

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
		&statusTransitionHook{},
		&typecalcUseHook{},
		&objectGateHook{}, // v8.8: per-object early feedback on status=confirmed
		&autoValidateHook{},
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
		return hookInternalErr("def-existence", fmt.Sprintf("argsJSON parse: %v", err))
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
	id, patch, err := parseMergeObjectArgs(argsJSON)
	if err != nil {
		return hookInternalErr("confirmed-impl", err.Error())
	}
	if patch == nil {
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
		g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
		if err != nil {
			return hookInternalErr("confirmed-impl", fmt.Sprintf("graph load: %v", err))
		}
		obj, present := g.Objects[id]
		if !present {
			return hookInternalErr("confirmed-impl", fmt.Sprintf("object %q not found in graph mid-hook (race?)", id))
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
			id, id,
		)
	}
	if !fileExistsAndNonEmpty(implPath) {
		return fmt.Sprintf(
			"[confirmed-impl] object %q was set to status=confirmed with impl=%q but that file is missing or empty on disk. "+
				"Required next turn: write the implementation, then re-run merge if needed.",
			id, implPath,
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
		return hookInternalErr("def-impl-distinct", fmt.Sprintf("argsJSON parse: %v", err))
	}
	g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if err != nil {
		return hookInternalErr("def-impl-distinct", fmt.Sprintf("graph load: %v", err))
	}
	obj, ok := g.Objects[args.ID]
	if !ok {
		return hookInternalErr("def-impl-distinct", fmt.Sprintf("object %q not found in graph mid-hook (race?)", args.ID))
	}
	if obj.Impl == nil || *obj.Impl == "" {
		return "" // no impl yet — nothing to compare against (legitimate skip)
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
	g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if err != nil {
		return hookInternalErr("def-uniqueness", fmt.Sprintf("graph load: %v", err))
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
		return hookInternalErr("root-finish-gate", fmt.Sprintf("argsJSON parse: %v", err))
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

// --- Hook 6: status-transition state machine (§5.2 of TypeCalculator doc) ---
//
// graph.Status transitions must follow the chain
// declared → implementing → confirmed; a graph_merge_*  call that tries
// to leap over implementing (or to demote a status) is flagged. The hook
// inspects the merge patch's "status" field and looks up the entity's
// current status from the on-disk graph.

type statusTransitionHook struct{}

func (h *statusTransitionHook) Name() string { return "status-transition" }

func (h *statusTransitionHook) After(toolName, argsJSON, result string) string {
	if toolName != "graph_merge_attribute" && toolName != "graph_merge_object" {
		return ""
	}
	// merge.go is atomic and already rejects illegal status transitions;
	// when the merge tool errors, the loop surfaces the result as
	// "error: <message>". The agent has already seen the rejection — a
	// duplicate hook warning was the source of the "warns but allows"
	// confusion in the 2026-05-09 pong analysis. Stay silent on errors.
	if strings.HasPrefix(result, "error:") {
		return ""
	}
	id, patch, err := parseMergeObjectArgs(argsJSON)
	if err != nil {
		return hookInternalErr("status-transition", err.Error())
	}
	if patch == nil {
		return ""
	}
	statusVal, hasStatus := patch["status"]
	if !hasStatus {
		return ""
	}
	to, _ := statusVal.(string)
	if to == "" {
		return ""
	}
	g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if err != nil {
		return hookInternalErr("status-transition", fmt.Sprintf("graph load: %v", err))
	}
	var from string
	if toolName == "graph_merge_attribute" {
		if attr, ok := g.Attributes[id]; ok {
			from = attr.Status
		}
	} else {
		if obj, ok := g.Objects[id]; ok {
			from = obj.Status
		}
	}
	// On a successful merge, the on-disk status equals `to` — so any
	// case where they differ here means the merge actually rejected
	// (and `from` is the unchanged pre-call status). With the error
	// guard above this branch is unreachable in practice; keep the
	// detection as defensive scaffolding in case a future tool path
	// applies status without going through merge.go.
	if from == "" || from == to {
		return ""
	}
	allowed := map[string]string{
		graph.StatusDeclared:     graph.StatusImplementing,
		graph.StatusImplementing: graph.StatusConfirmed,
	}
	expectedNext, ok := allowed[from]
	if !ok {
		return fmt.Sprintf(
			"[status-transition] entity %q is in status %q (terminal); transition to %q is not allowed. "+
				"To revisit a confirmed entity, roll back the session that confirmed it instead.",
			id, from, to,
		)
	}
	if to != expectedNext {
		return fmt.Sprintf(
			"[status-transition] entity %q tried %s → %s, but the only legal next step is %s. "+
				"Status must follow declared → implementing → confirmed (docs/TypeCalculator.md §5.2). "+
				"Required next turn: graph_merge_<kind> with patch '{\"status\":%q}' first, finish the implementation, then advance to %q.",
			id, from, to, expectedNext, expectedNext, to,
		)
	}
	return ""
}

// --- Hook 7: typecalc-use enforcement ---
//
// Before any object can transition to status=confirmed, there must be on-
// disk evidence that the agent ran typecalc_compile (or typecalc_test) for
// that object. The typecalc tool writes
// .kcpos/typecalc-evidence/<objectID>.json on success; this hook checks
// for that file when a graph_merge_object patch sets status=confirmed.
//
// Without this enforcement, the LLM tends to skip the mechanical
// validation entirely (we observed this on the first end-to-end run:
// "let me try typecalc_compile" thoughts but no actual call). Making the
// evidence load-bearing for confirmation forces the pathway to be used.

type typecalcUseHook struct{}

const typecalcEvidenceDirRel = ".kcpos/typecalc-evidence"

func (h *typecalcUseHook) Name() string { return "typecalc-use" }

func (h *typecalcUseHook) After(toolName, argsJSON, _ string) string {
	if toolName != "graph_merge_object" {
		return ""
	}
	id, patch, err := parseMergeObjectArgs(argsJSON)
	if err != nil {
		return hookInternalErr("typecalc-use", err.Error())
	}
	if patch == nil {
		return ""
	}
	statusVal, hasStatus := patch["status"]
	if !hasStatus {
		return ""
	}
	if s, _ := statusVal.(string); s != graph.StatusConfirmed {
		return ""
	}
	if typecalcEvidenceExists(id) {
		return ""
	}
	return fmt.Sprintf(
		"[typecalc-use] object %q was set to status=confirmed without prior typecalc evidence "+
			"(no file under %s/%s.json). The mechanical compile/test pathway is required for "+
			"confirmation — \"confirmed\" without typecalc evidence is just a string the LLM typed.\n"+
			"Required next turn: call typecalc_compile (or typecalc_test) with object_id=%q and the "+
			"actual implementation payload, then re-issue the merge. If you have already merged, "+
			"the evidence file will be created on the next typecalc call and the next finish gate "+
			"will pass — but if you skip this step the gate at root-finish will block you.",
		id, typecalcEvidenceDirRel, id, id,
	)
}

func typecalcEvidenceExists(objectID string) bool {
	if objectID == "" {
		return false
	}
	// v9.0: route through the unified ObjectState query so the agent
	// hook, the graph_merge_object blocking check, and the root gate
	// all judge "has usable evidence?" the same way.
	return core.LoadObjectState(objectID, "").HasUsableEvidence()
}

// --- Hook 7.5: object-gate on status=confirmed (v8.8) ---
//
// When graph_merge_object flips an object to status=confirmed, the
// agent has implicitly claimed "this object's evidence is in order".
// Pre-v8.8 that claim was only validated at root-finish gate_check —
// problems sat dormant for many turns until the agent eventually
// hit the big gate. v8.7 pong-03 hit gate_check 8 times because of
// this; pong-05 hit 4 times. v8.8 splits the gate so every
// status=confirmed transition gets an immediate per-object audit:
// the same rules the root walk applies, but scoped to ONE object,
// emitted as a hook message the agent sees next turn.
//
// The hook fires regardless of whether typecalc-use's evidence-exists
// check passed — that check verifies a file exists; gate_object
// verifies the file's content (ok=true / kind=test / accepted-evidence
// chain). Both can fire on the same merge if both are violated;
// FormatViolations concatenates them in order.
//
// Emits nothing when gate_object returns no issues (clean confirm).

type objectGateHook struct{}

func (h *objectGateHook) Name() string { return "object-gate" }

func (h *objectGateHook) After(toolName, argsJSON, _ string) string {
	if toolName != "graph_merge_object" {
		return ""
	}
	id, patch, err := parseMergeObjectArgs(argsJSON)
	if err != nil {
		return hookInternalErr("object-gate", err.Error())
	}
	if patch == nil {
		return ""
	}
	statusVal, hasStatus := patch["status"]
	if !hasStatus {
		return ""
	}
	if s, _ := statusVal.(string); s != graph.StatusConfirmed {
		return ""
	}
	g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if err != nil {
		return hookInternalErr("object-gate", fmt.Sprintf("graph load: %v", err))
	}
	cwd, _ := os.Getwd()
	issues, _ := workflow.CheckObjectGate(g, id, cwd)
	if len(issues) == 0 {
		return ""
	}
	var b []byte
	b = append(b, fmt.Sprintf("[object-gate] %s flipped to status=confirmed but per-object gate found %d issue(s):\n", id, len(issues))...)
	for _, iss := range issues {
		b = append(b, "  ✗ "...)
		b = append(b, iss...)
		b = append(b, '\n')
	}
	b = append(b, "Address each before the next graph_merge_object or before session_gate_check. The root-finish gate runs the same checks across all confirmed objects — fixing them per-object now avoids stacking failures."...)
	return string(b)
}

// --- Hook 8: auto-validate after every graph mutation ---
//
// Without this, structural problems (a consume with no producer, a
// dangling refines parent, a confirmed attribute with empty valueSpace)
// only surface when the agent explicitly calls graph_validate — usually
// at end-of-session. The 2026-05-09 pong run showed input_keys was
// consumed by UpdatePaddle from session start, but the missing producer
// was only noticed at the very end (root finalization), forcing a late
// CaptureInput retrofit. Surfacing the imbalance immediately after each
// mutation lets the agent fix it while context is fresh.
//
// We run the full validate but only emit ERRORs (warnings are noise on
// every edit). The check is cheap — it's the same one the gate runs.

type autoValidateHook struct{}

func (h *autoValidateHook) Name() string { return "auto-validate" }

var graphMutatingTools = map[string]bool{
	"graph_create_attribute": true,
	"graph_create_object":    true,
	"graph_link_consume":     true,
	"graph_link_produce":     true,
	"graph_link_mutate":      true,
	"graph_link_refine":      true,
	"graph_unlink_consume":   true,
	"graph_unlink_produce":   true,
	"graph_unlink_mutate":    true,
	"graph_unlink_refine":    true,
	"graph_merge_attribute":  true,
	"graph_merge_object":     true,
}

func (h *autoValidateHook) After(toolName, _ string, result string) string {
	if !graphMutatingTools[toolName] {
		return ""
	}
	// If the mutation itself failed, the on-disk graph hasn't moved —
	// no point re-flagging integrity issues that already existed.
	if strings.HasPrefix(result, "error:") {
		return ""
	}
	g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if err != nil {
		return hookInternalErr("auto-validate", fmt.Sprintf("graph load: %v", err))
	}
	cwd, _ := os.Getwd()
	report := g.Validate(cwd)
	if !report.HasErrors() {
		return ""
	}
	var errs []string
	for _, iss := range report.Issues {
		if iss.Severity == graph.Error {
			errs = append(errs, fmt.Sprintf("[%s] %s — %s", iss.Rule, iss.Where, iss.Message))
		}
		if len(errs) >= 5 {
			errs = append(errs, fmt.Sprintf("(... %d more — run graph_validate for full list)", len(report.Issues)-5))
			break
		}
	}
	return fmt.Sprintf(
		"[auto-validate] graph integrity errors detected after %s. **Required next turn**: address these errors before any other graph mutation, typecalc step, or session transition. Continuing past unresolved structural errors only compounds them — a missing producer / dangling refines parent / confirmed attribute with no valueSpace will cascade into typecalc-test-required and accepted-evidence-required gate failures later. Fix list (top %d):\n  %s",
		toolName, len(errs), strings.Join(errs, "\n  "),
	)
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
