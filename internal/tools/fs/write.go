package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"github.com/creator915/Koncept_OS/internal/shared/agentctx"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
	"github.com/creator915/Koncept_OS/internal/typecalc/lang"
)

// isObjectDefPath returns true for object-def file paths — these are
// signature stubs that should be written by the subagent owning the
// object, not by the main conversation. Attribute defs
// (`<snake_case_id>.<ext>`) are NOT flagged: they're simple type
// aliases the main conversation can produce in bulk without context
// bloat.
//
// Recognises BOTH canonical prefixes — `K/defs/<PascalCaseId>.<ext>`
// (the explicit form) AND `defs/<PascalCaseId>.<ext>` (the default
// the graph_create_object tool's `def` parameter falls back to when
// not overridden). Pre-2026-05-20 only the `K/defs/` form was
// recognised, so an agent writing the *default* def path got the
// file misclassified as an impl by isLikelyImplPath downstream and
// rejected by the process-justice gate — a self-inflicted deadlock
// that the 2026-05-20 outer-Router pong batch surfaced.
func isObjectDefPath(path string) bool {
	norm := strings.ReplaceAll(path, "\\", "/")
	hasPrefix := strings.HasPrefix(norm, "K/defs/") || strings.HasPrefix(norm, "defs/")
	if !hasPrefix {
		return false
	}
	base := filepath.Base(norm)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		return false
	}
	first := rune(stem[0])
	if !unicode.IsUpper(first) {
		return false
	}
	// Reject snake_case (attribute names like "flags_config") even if
	// the first letter happened to be uppercase — PascalCase has no
	// underscores between identifiers.
	if strings.Contains(stem, "_") {
		return false
	}
	return true
}

// implHasOwningObject reports whether some graph object already claims
// `path` as its impl (obj.Impl == path). Matching semantics are
// deliberately identical to resolveAutoCompileTarget step 1 so the
// upstream process-justice gate and auto-compile attribution agree.
func implHasOwningObject(path string) bool {
	g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if err != nil || g == nil {
		return false
	}
	for _, obj := range g.Objects {
		if obj.Impl != nil && *obj.Impl == path {
			return true
		}
	}
	return false
}

// isLikelyImplPath returns true for paths that look like real code
// (impl files). Heuristic: extension matches a known source-code
// language. Excludes BOTH `K/defs/` and `defs/` so attribute and
// object def files don't get misclassified as impls — see
// isObjectDefPath for the two-prefix rationale.
func isLikelyImplPath(path string) bool {
	norm := strings.ReplaceAll(path, "\\", "/")
	if strings.HasPrefix(norm, "K/defs/") || strings.HasPrefix(norm, "defs/") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(norm))
	switch ext {
	case ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".rs", ".c", ".cpp", ".cc", ".java", ".kt", ".swift":
		return true
	}
	return false
}

func writeFileTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name: "write_file",
				Description: "Write content to a file. Creates the file if it does not exist, overwrites if it does. Creates parent directories as needed.\n\nIf the path matches an implementation file (an `impl` field of any object on the graph, OR matches the `*.impl.*` naming convention), this tool ALSO runs `typecalc_compile` on the content immediately after writing. A compile failure surfaces in the tool result; a passing compile auto-records typecalc evidence on disk so the agent does not need a separate typecalc_compile call.\n\n**HARD-REFUSED paths (v10)**:\n- `K/graph.json` and `K/graph.*` — modify the graph via graph_* tools, never directly.\n- `K/frags/*` — code under K/frags is session_build's output. Put your code in the graph object's `implContent` via `graph_merge_object id=<id> patch='{\"implContent\":\"function Foo(...){ ... }\"}'`. session_build will then emit K/frags/<id>.js from implContent.\n\n**Process-justice block**: writing a `*.<code-ext>` path (.ts/.js/.go/.py/...) outside K/defs/ is rejected unless some graph object already claims that path as its `impl`. The fix order is always: graph_create_object → graph_merge_object set impl → THEN write_file.",
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
			// Hard-refuse agent-managed SoT paths (K/graph.* + K/frags/*).
			// Shared with edit — see fs/guard.go.
			if err := guardAgentManagedPaths("write_file", path); err != nil {
				return "", err
			}
			// v9.6 — dispatch-mode hardening. Once the graph has reached
			// DispatchModeThreshold objects, the main conversation must
			// stop doing impl-side work inline (the 2026-05-14 fx batch
			// died because the main conversation wrote 15 def files + was
			// in the middle of emitting 20+ link edges when the LLM
			// stream timed out). Subagents are unrestricted; main-conv
			// is restricted ONLY at the impl-related path patterns:
			//   - K/defs/<PascalCaseId>.<ext>  (object def files)
			//   - any explicit impl file (heuristic: extension that
			//     looks like a code language, NOT inside K/defs/<id>
			//     that's an attribute type decl).
			// Attribute def files are still fine in main conv (they're
			// type aliases / simple structs and the LLM produces them
			// quickly), and writes outside K/ entirely (docs, scratch)
			// are unrestricted.
			if isObjectDefPath(path) || isLikelyImplPath(path) {
				g, _ := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
				count := 0
				if g != nil {
					count = len(g.Objects)
				}
				action := fmt.Sprintf("write_file path=%s", path)
				if err := agentctx.CheckMainImplWork(ctx, count, action); err != nil {
					return "", err
				}
			}
			// UPSTREAM PROCESS-JUSTICE GATE (流程正义) — the upstream half,
			// integrated with ①②③. An implementation file may be written
			// ONLY if a graph object already owns it (obj.Impl == path).
			// kcpos is graph-first ("图是工作流的源头"): writing impl with
			// no owning object lets the agent walk the entire un-just
			// bare path and only be ambushed at the ③ termination gate.
			// This catches it at the FIRST impl write instead. UNIVERSAL
			// (not contract-scoped) by design — graph-first IS the design,
			// and a universal structural rule has no opt-in fragility
			// (unlike a flag-gated check). graph_create_object is in the
			// catalog in every mode, so the agent can always comply: this
			// CONSTRAINS the un-just path, it does not deadlock.
			if isLikelyImplPath(path) && !implHasOwningObject(path) {
				return "", fmt.Errorf(
					"write_file BLOCKED (process-justice / 流程正义): %q is an implementation file but NO graph object owns it. "+
						"kcpos is graph-first — writing impl with no owning object walks the un-just path (no verification chain). "+
						"Required, in order, before retrying: (1) graph_create_object with an id + intent for this deliverable; "+
						"(2) graph_merge_object with a patch binding impl=%q to that object; (3) then write_file this path. "+
						"status=confirmed is then conferred ONLY by confirm_object's mechanical behavioral-equivalence oracle "+
						"(./executable == ./probe over a non-model battery) — never hand-set. This is mandatory and cannot be skipped.",
					path, path)
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
	if langTag == core.LangNone {
		return "" // no language inference possible
	}
	tv := core.New(core.KindCode, content).
		WithState(core.StateUncompiled).
		WithLang(langTag)
	env := &core.RuleEnv{WorkDir: "."}
	out, err := lang.CompileLanguageInvoker(ctx, env, tv)
	if err != nil {
		return fmt.Sprintf("auto-typecalc: invoker error: %v", err)
	}
	if out.State == core.StateCompiled {
		// HTML containing inline <script> is effectively a JS container
		// — record evidence under JavaScript so the gate requires test
		// evidence (you can't dodge testing by writing JS inside HTML).
		effectiveLang := core.DetectEffectiveLang(content, langTag)
		var written []string
		for _, id := range objectIDs {
			if recErr := core.RecordEvidence(id, "compile", string(effectiveLang), true); recErr == nil {
				written = append(written, id)
			}
		}
		// 2026-05-21 — auto-invalidate post-confirm edits.
		// PB-30 batch #8 bat (5/5) + entr (3/3) both hit terminal gate
		// fail on [method-use-rule] because the agent edited an already-
		// confirmed object's impl source after the chain ran but never
		// re-ran confirm_object. Method Use Rule is meant to catch this,
		// but at gate time the failure is terminal — there's no repair
		// path back into H_confirm_one. The fix is upstream: when
		// write_file overwrites a confirmed object's impl, atomically
		// demote it (confirmed → declared) and clear its Characterization
		// + Accepted evidence so H_confirm_one naturally re-picks it.
		invalidated := invalidateConfirmedImplOnEdit(objectIDs, content)
		if len(invalidated) > 0 {
			extra := fmt.Sprintf("\n[method-use-rule] auto-invalidated %d previously-confirmed object(s): %s — "+
				"impl source changed since the equivalence oracle locked it. Status reset to 'declared', "+
				"Characterization + Accepted cleared. confirm_object will re-run the oracle against the new artifact.",
				len(invalidated), strings.Join(invalidated, ", "))
			if len(written) == 0 {
				return strings.TrimPrefix(extra, "\n")
			}
			return fmt.Sprintf("[auto-typecalc] compile passed; evidence recorded for: %s (lang=%s)%s",
				strings.Join(written, ", "), effectiveLang, extra)
		}
		if len(written) == 0 {
			return ""
		}
		return fmt.Sprintf("[auto-typecalc] compile passed; evidence recorded for: %s (lang=%s)",
			strings.Join(written, ", "), effectiveLang)
	}
	if out.Kind == core.KindCompileError {
		ce, _ := core.DecodeCompileError(out)
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
func resolveAutoCompileTarget(path string) ([]string, core.Lang) {
	// Step 1: graph lookup — find any object whose impl == path.
	var matched []string
	if g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath); err == nil {
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
		return nil, core.LangNone
	}

	// Language inference from extension.
	ext := filepath.Ext(path)
	return matched, core.LangFromExt(ext)
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

// invalidateConfirmedImplOnEdit demotes any of the given objects that are
// currently status=confirmed AND whose Characterization.CodeHash no
// longer matches the new content's hash. For each demoted object:
//
//   - status reset to "declared" (bypasses MergeObject's
//     confirmed→implementing block — auto-invalidation is its own
//     justified transition)
//   - Characterization section cleared (next equiv_oracle will rewrite)
//   - Accepted section cleared (next review will rewrite)
//   - Cycles + Test sections cleared (so the chain re-walks freshly)
//
// Returns the IDs that were actually invalidated (empty when no edit
// crosses an already-confirmed object's impl hash, which is the common
// path — most writes are pre-confirm).
//
// This is the upstream complement to the gate's [method-use-rule]: the
// rule catches post-edit-but-no-re-characterize cases at finish time,
// but by then the failure is terminal (no repair handler). Catching the
// edit AT WRITE TIME lets H_confirm_one's natural re-pick logic drive
// the demoted object back through the chain.
func invalidateConfirmedImplOnEdit(objectIDs []string, newContent string) []string {
	g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if err != nil || g == nil {
		return nil
	}
	newHash := core.HashSource(newContent)
	var invalidated []string
	graphMutated := false
	for _, id := range objectIDs {
		obj, ok := g.Objects[id]
		if !ok || obj == nil {
			continue
		}
		if obj.Status != graph.StatusConfirmed {
			continue
		}
		// Check the existing characterization (if any) for hash drift.
		// No characterization → conservative: skip (the original confirm
		// path didn't run equiv_oracle, so we can't tell if the hash
		// changed meaningfully).
		cs, hasC := core.ReadCharacterization(id)
		if !hasC || cs == nil || cs.CodeHash == "" {
			continue
		}
		if cs.CodeHash == newHash {
			continue // no drift, no invalidation needed
		}
		// Drift confirmed → demote + clear evidence.
		obj.Status = graph.StatusDeclared
		graphMutated = true
		// Clear downstream evidence so re-confirmation runs cleanly.
		if b, hasB := core.ReadBundle(id); hasB && b != nil {
			// Legacy field — kept for read-compat on pre-Step-5
			// bundles; post-Step-5 it's always nil at this point.
			b.Characterization = nil
			b.Accepted = nil
			b.Test = nil
			b.Cycles = nil
			b.RuntimeTrace = nil
			// Step 5 single-source: strip characterization clauses
			// from spec.Contract — that's the canonical store now.
			// Without this, contract_trace.go's reconstruction-mode
			// skip would still fire on stale clauses after impl edit,
			// letting confirm pass on a re-implementation that hasn't
			// been re-locked.
			if b.Spec != nil && len(b.Spec.Contract) > 0 {
				kept := b.Spec.Contract[:0]
				for _, c := range b.Spec.Contract {
					if c.Kind != "characterization" {
						kept = append(kept, c)
					}
				}
				b.Spec.Contract = kept
			}
			_ = core.SaveBundle(b)
		}
		invalidated = append(invalidated, id)
	}
	if graphMutated {
		// Bypasses MergeObject — the auto-invalidate path is in-engine,
		// not agent-facing, and the validation it would trip on
		// (confirmed → !confirmed is rejected for agent writes) doesn't
		// apply: the invariant the validation protects is "the LLM
		// can't hand-flip confirmed", not "the engine can never demote".
		_ = persistence.SaveGraph(persistence.GraphDefaultPath, g)
	}
	return invalidated
}
