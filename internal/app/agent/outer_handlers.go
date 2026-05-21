package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/creator915/Koncept_OS/internal/domain/checkpoint"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"github.com/creator915/Koncept_OS/internal/router"
	"github.com/creator915/Koncept_OS/internal/tools"
)

// Outer Handlers — see router/outerflow.go for the type-flow design.
// Naming: H_<verb>_<noun> for forward, H_repair_<noun> for feedback arcs.
//
// Each Handler returns either the declared next-state TypedValue OR
// an Obstacle. They MUST verify post-conditions on disk before
// asserting forward progress; this is the same rule every gate /
// hook follows.

// --- Forward Handlers ---

// H_architect: Outer.Task → Outer.Architecture | Outer.Obstacle
//
// Runs a focused LLM sub-loop authorised to read the SPEC outline and
// set the root session's Architecture field via session_set_architecture.
// Post-condition: sess.Output.Architecture != "".
func H_architect(deps *OuterDeps) router.Handler {
	return &router.HandlerFunc{
		In:  router.OuterTypeTask,
		Out: []string{router.OuterTypeArchitecture, router.OuterTypeObstacle},
		Run: func(ctx context.Context, _ router.TypedValue) (router.TypedValue, error) {
			sys := fmt.Sprintf(
				"You are the Handler 1.3 architect sub-agent. Your ONLY job is to set the architecture for root session %q. "+
					"Steps: (1) call markdown_outline on %q. (2) call markdown_section for the chapters that describe the system structure. "+
					"(3) **If a `./probe` exists in the working directory (brownfield / PB-style black-box rebuild)**: call `probe` with a handful of representative argv combinations (e.g. `--help`, `--version`, a typical input, an error path) to observe the reference's actual behavior. Use those observations to inform the architecture. "+
					"(4) call session_set_architecture id=%q description=<markdown listing sub-modules and intermediate variables>. "+
					"After session_set_architecture succeeds, output the single line `DONE` with no tool calls. "+
					"Do NOT touch graph_*, write_file, confirm_object — those belong to later Handlers.",
				deps.RootSessionID, deps.SpecPath, deps.RootSessionID)

			task := fmt.Sprintf("Set architecture for session %s from SPEC %s. Task: %s",
				deps.RootSessionID, deps.SpecPath, deps.TaskDescription)

			_ = deps.runScopedLLM(ctx, sys, task, []string{
				"markdown_outline", "markdown_section", "markdown_validate",
				"read_file", "session_set_architecture",
				// Brownfield probe channel (sandboxed; routes through KCPOS_AMD64_CONTAINER
				// when set). Lets the architect observe a black-box reference's behavior
				// before committing to an architecture — required for PB-30 / characterize-class
				// tasks. No-op for greenfield projects with no ./probe in cwd.
				"probe",
			})

			det, _ := router.InferOuterStateDetail(deps.RootSessionID)
			if !det.ArchitectureNonEmpty {
				return emitObstacle("architecture",
					"architecture remains empty after sub-agent loop",
					"check K/sessions/"+deps.RootSessionID+".json output.architecture"), nil
			}
			return router.MarshalPayload(router.OuterTypeArchitecture, map[string]string{
				"rootSessionID": deps.RootSessionID,
			}), nil
		},
	}
}

// H_graph_declare: Outer.Architecture → Outer.GraphDeclared | Outer.Obstacle
//
// Declares every graph object the SPEC implies, status=declared.
// Post-condition: K/graph.json non-empty AND every declared object has
// a def file on disk (def-existence hook satisfies this implicitly).
func H_graph_declare(deps *OuterDeps) router.Handler {
	return &router.HandlerFunc{
		In:  router.OuterTypeArchitecture,
		Out: []string{router.OuterTypeGraphDeclared, router.OuterTypeObstacle},
		Run: func(ctx context.Context, _ router.TypedValue) (router.TypedValue, error) {
			sys := fmt.Sprintf(
				"You are the Handler 1.1 graph-declare sub-agent. Your ONLY job is to declare every graph object the SPEC needs for session %q. "+
					"Steps: (1) call markdown_outline / markdown_section to chunk-read the SPEC at %q. "+
					"(2) For each testable contract, call graph_create_attribute (data types, snake_case) and graph_create_object (function types, PascalCase) — include intent + storyPoints (Fibonacci) + storyRationale. "+
					"(3) Use graph_link_consume / graph_link_produce / graph_link_mutate to wire dependencies. "+
					"(4) Call graph_preflight to verify the structure. "+
					"After every must-have object is declared (status=declared), output `DONE` with no tool calls. "+
					"Do NOT set impl yet; do NOT call confirm_object — those belong to H_confirm_one.\n\n"+
					"CRITICAL: avoid declaring ORCHESTRATOR objects (game loops, requestAnimationFrame wrappers, event dispatchers, "+
					"top-level main()s) as graph nodes. Their contract is 'call X then Y' — which typecalc_synthesize_tests CANNOT "+
					"derive tests for, and the per-object gate will block forever. INSTEAD: model the testable sub-functions "+
					"individually (each with concrete inputs/outputs that have produces/mutates edges to attributes), and let the "+
					"orchestrator live as plain JavaScript inside index.html or as a tiny inline glue function in one of the "+
					"verified objects' impl. This is documented in system.md as 'POOR object candidates'. "+
					"Bias HARD towards fewer objects (3-5 is excellent for a small demo) over comprehensive coverage. Every "+
					"declared object pays a ~5-10min verification chain cost.",
				deps.RootSessionID, deps.SpecPath)

			task := fmt.Sprintf("Declare graph objects for session %s from SPEC %s.",
				deps.RootSessionID, deps.SpecPath)

			_ = deps.runScopedLLM(ctx, sys, task, []string{
				"markdown_outline", "markdown_section", "markdown_validate", "read_file",
				"write_file", "edit", "list_dir", "glob",
				"graph_create_attribute", "graph_create_object",
				"graph_link_consume", "graph_link_produce", "graph_link_mutate",
				"graph_merge_object", "graph_merge_attribute",
				"graph_preflight", "graph_show", "graph_validate",
				// Brownfield probe (see H_architect rationale). During decomposition
				// the agent often needs another probe to disambiguate sub-functions
				// before committing graph object boundaries.
				"probe",
			})

			det, _ := router.InferOuterStateDetail(deps.RootSessionID)
			if len(det.DeclaredObjectIDs) == 0 {
				return emitObstacle("graph_declare",
					"no objects declared in K/graph.json after sub-agent loop"), nil
			}
			return router.MarshalPayload(router.OuterTypeGraphDeclared, map[string]interface{}{
				"rootSessionID":     deps.RootSessionID,
				"declaredObjectIDs": det.DeclaredObjectIDs,
			}), nil
		},
	}
}

// H_confirm_one: Outer.GraphDeclared | Outer.SomeConfirmed → Outer.SomeConfirmed | Outer.AllConfirmed | Outer.Obstacle
//
// Picks ONE not-yet-confirmed object, runs the impl-write + confirm_object
// inner chain on it, returns the updated tally. Re-enters on
// Outer.SomeConfirmed until det.RemainingObjectIDs is empty.
//
// Because the outer Router dispatches by input type, this single
// Handler is registered for BOTH GraphDeclared and SomeConfirmed — the
// In sum-arm is expressed via two HandlerFunc registrations sharing
// the same closure body. See registerConfirmOne below.
func handleConfirmOne(deps *OuterDeps) func(ctx context.Context, in router.TypedValue) (router.TypedValue, error) {
	return func(ctx context.Context, _ router.TypedValue) (router.TypedValue, error) {
		det, err := router.InferOuterStateDetail(deps.RootSessionID)
		if err != nil {
			return emitObstacle("confirm_one", "state inference failed: "+err.Error()), nil
		}
		if len(det.RemainingObjectIDs) == 0 {
			return router.MarshalPayload(router.OuterTypeAllConfirmed, map[string]string{
				"rootSessionID": deps.RootSessionID,
			}), nil
		}
		// Rotate to the least-attempted target instead of always
		// picking RemainingObjectIDs[0] — see pickConfirmTarget for
		// rationale (avoids one stuck object monopolising the loop).
		target := deps.pickConfirmTarget(det.RemainingObjectIDs)
		attempts := deps.recordConfirmAttempt(target)

		sys := fmt.Sprintf(
			"You are the Handler 1.2 confirm-one sub-agent. Your ONLY job is to drive ONE object %q to status=confirmed AND backfill its produced/mutated attributes. "+
				"Steps: (1) write the impl. For HTML / single-file projects (where multiple objects share impl=index.html) set the code via graph_merge_object patch='{\"implContent\":\"...\"}' (v10 source-of-truth) — do NOT call write_file K/frags/*. For separate-file projects, write_file the per-object impl path. "+
				"(2) graph_merge_object to set impl + portObservation + status=implementing. "+
				"(3) call confirm_object object_id=%q to drive the inner chain.\n\n"+
				"**Brownfield / PB-30 black-box rebuild — if `./probe` exists in cwd**: before declaring you're done, also: "+
				"  - call `probe` with representative inputs to capture reference behavior; "+
				"  - write `compile.sh` (a one-shot bash script that builds the full deliverable on linux/amd64 — no network) once enough source is in place; "+
				"  - call `compile` to invoke compile.sh in the sandboxed amd64 container; "+
				"  - call `run_local` with inputs you've already probed and compare your output against probe's. This is the structural way to ground impl in reality rather than memory.\n\n"+
				"**On TestError — Handler 1.2's brownfield branch (KonceptOS_implementation_plan.md §1.2)**: when confirm_object returns Obstacle / TestError, **DO NOT immediately re-edit the impl and retry**. The original design forbids that path (\"TestError 不打回 LLM\"). Instead: "+
				"  a. Call `characterize(object_id=%q)` first. This runs LLM-synthesized probes against the actual impl, observes what it really does, and locks the observed behavior. "+
				"  b. Read the returned `guidance` field. If LOCKED behavior matches the description (impl is internally consistent), the SYNTHESIZED TESTS are wrong — call `typecalc_synthesize_tests` to regenerate them, then retry `confirm_object`. If LOCKED behavior diverges (UNLOCKED probes / mismatch with intent), the IMPL is wrong — edit the impl to honour the intent, then re-characterize. "+
				"  c. Only after characterize-diagnosis says \"impl wrong\" should you edit the impl. Editing without characterize-diagnosis violates the documented Handler 1.2 contract.\n\n"+
				"(4) **AFTER confirm_object succeeds**, backfill the produced/mutated attributes: for each attribute this object PRODUCES or MUTATES (see graph_show), call graph_merge_attribute id=<attr> patch='{\"status\":\"confirmed\",\"valueSpace\":{...}}' — pick a valueSpace matching the attribute type (e.g. `{\"kind\":\"int\",\"min\":0,\"max\":1000000}` for numbers, `{\"kind\":\"string\",\"pattern\":\".*\"}` for strings, `{\"kind\":\"bool\"}` for booleans). Skipping this would fail the [attrs-backfilled] gate rule later. "+
				"Do NOT touch other objects; do NOT call session_aggregate / session_gate_check. When this object is confirmed AND its attributes are backfilled, output `DONE`.",
			target, target, target)

		// After 3+ failed attempts on the same object, the object is
		// almost certainly an orchestrator / loop / dispatcher whose
		// contract is "call X then Y" — typecalc_synthesize_tests
		// returns CANNOT_SYNTHESIZE because there are no observable
		// ports. Pivot the sub-agent to refactor instead of spinning.
		if attempts >= 3 {
			sys += fmt.Sprintf(
				"\n\nESCALATION (attempt #%d on %q): the standard chain has failed multiple times — most likely "+
					"this object is an ORCHESTRATOR / loop / dispatcher with no observable ports "+
					"(typecalc_synthesize_tests returns CANNOT_SYNTHESIZE). DO NOT call confirm_object on %q "+
					"again until you take ONE of these refactor remedies: "+
					"(A) Add real produces/mutates edges that this function ACTUALLY changes (use graph_link_produce / "+
					"graph_link_mutate), update portObservation to point at concrete return.<field> or args.<n>.<field>, "+
					"and rewrite the impl so those ports carry real values. OR "+
					"(B) Inline this function's body into its caller, then unlink all of this object's edges via "+
					"graph_unlink_consume / graph_unlink_produce / graph_unlink_mutate. The object will remain "+
					"`declared` in the graph but with zero edges + zero impl — the gate's per-object check is then "+
					"vacuously satisfied (no observable contract to verify). "+
					"After EITHER remedy, output `DONE` so the Router re-infers state and rotates to the next target.",
				attempts, target, target)
		}

		task := fmt.Sprintf("Confirm object %s for session %s (attempt %d).", target, deps.RootSessionID, attempts)

		_ = deps.runScopedLLM(ctx, sys, task, []string{
			"read_file", "write_file", "edit", "list_dir", "grep", "glob",
			"markdown_outline", "markdown_section",
			"graph_merge_object", "graph_merge_attribute", "graph_show",
			"graph_unlink_consume", "graph_unlink_produce", "graph_unlink_mutate",
			"graph_link_consume", "graph_link_produce", "graph_link_mutate",
			"typecalc_compile", "typecalc_describe", "typecalc_synthesize_tests",
			"typecalc_test", "typecalc_review",
			"confirm_object", "runtime_smoke", "runtime_link", "runtime_install",
			"session_build", "gate_object",
			// brownfield TestError branch per Handler 1.2 original design
			// (KonceptOS_implementation_plan.md §1.2 L158/167/178):
			"characterize",
			// Sandboxed reference-channel triad (sandboxed.go). Pre-2026-05-21
			// these were registered in Builtins() but NOT in any Phase-2.C
			// Handler scoped set — agent couldn't probe black-box references
			// or build a complete deliverable. PB-30 batch attempt #1 surfaced
			// this: 5/5 instances wrote source from README+memory only, never
			// invoked probe (0 calls). Adding here:
			//   probe       — observe black-box reference (./probe in cwd)
			//   compile     — run compile.sh on linux/amd64 container
			//   run_local   — exec the built ./executable on amd64
			"probe", "compile", "run_local",
		})

		det2, _ := router.InferOuterStateDetail(deps.RootSessionID)
		if confirmedSetEq(det2.ConfirmedObjectIDs, det.ConfirmedObjectIDs) {
			return emitObstacle("confirm_one",
				fmt.Sprintf("object %q did not reach confirmed after sub-agent loop", target),
				"check .kcpos/typecalc/"+target+".json for the latest accepted section"), nil
		}
		if len(det2.RemainingObjectIDs) == 0 {
			return router.MarshalPayload(router.OuterTypeAllConfirmed, map[string]string{
				"rootSessionID": deps.RootSessionID,
			}), nil
		}
		return router.MarshalPayload(router.OuterTypeSomeConfirmed, map[string]interface{}{
			"rootSessionID":      deps.RootSessionID,
			"remainingObjectIDs": det2.RemainingObjectIDs,
		}), nil
	}
}

// H_confirm_one_from_declared registers the H_confirm_one body for the
// GraphDeclared input arm.
func H_confirm_one_from_declared(deps *OuterDeps) router.Handler {
	return &router.HandlerFunc{
		In:  router.OuterTypeGraphDeclared,
		Out: []string{router.OuterTypeSomeConfirmed, router.OuterTypeAllConfirmed, router.OuterTypeObstacle},
		Run: handleConfirmOne(deps),
	}
}

// H_confirm_one_loop registers the H_confirm_one body for the
// SomeConfirmed input arm (loop self-edge).
func H_confirm_one_loop(deps *OuterDeps) router.Handler {
	return &router.HandlerFunc{
		In:  router.OuterTypeSomeConfirmed,
		Out: []string{router.OuterTypeSomeConfirmed, router.OuterTypeAllConfirmed, router.OuterTypeObstacle},
		Run: handleConfirmOne(deps),
	}
}

// H_aggregate: Outer.AllConfirmed → Outer.Aggregated | Outer.Obstacle
//
// Deterministic: calls session_aggregate on the root.
func H_aggregate(deps *OuterDeps) router.Handler {
	return &router.HandlerFunc{
		In:  router.OuterTypeAllConfirmed,
		Out: []string{router.OuterTypeAggregated, router.OuterTypeObstacle},
		Run: func(ctx context.Context, _ router.TypedValue) (router.TypedValue, error) {
			argsJSON := fmt.Sprintf(`{"id":%q}`, deps.RootSessionID)
			out := tools.Execute(ctx, deps.ToolSet, "session_aggregate", argsJSON)
			if strings.HasPrefix(out, "error:") {
				return emitObstacle("aggregate", "session_aggregate failed: "+out), nil
			}
			det, _ := router.InferOuterStateDetail(deps.RootSessionID)
			if !det.AggregateDone {
				return emitObstacle("aggregate",
					"session_aggregate returned ok but session.Output.Implementations is empty"), nil
			}
			return router.MarshalPayload(router.OuterTypeAggregated, map[string]string{
				"rootSessionID": deps.RootSessionID,
			}), nil
		},
	}
}

// H_build: Outer.Aggregated → Outer.Built | Outer.Checkpointed | Outer.Obstacle
//
// Deterministic. For HTML single-file projects (any object has Impl
// ending in .html/.htm) call session_build. For multi-file projects,
// no build step is needed — go straight to Checkpointed via the
// allowed Aggregated→Checkpointed shortcut.
func H_build(deps *OuterDeps) router.Handler {
	return &router.HandlerFunc{
		In:  router.OuterTypeAggregated,
		Out: []string{router.OuterTypeBuilt, router.OuterTypeCheckpointed, router.OuterTypeObstacle},
		Run: func(ctx context.Context, _ router.TypedValue) (router.TypedValue, error) {
			g, err := persistence.LoadGraph(persistence.GraphDefaultPath)
			if err != nil || g == nil {
				return emitObstacle("build", "graph load failed: "+errStr(err)), nil
			}
			needsBuild := false
			for _, o := range g.Objects {
				if o == nil || o.Impl == nil {
					continue
				}
				lc := strings.ToLower(*o.Impl)
				if strings.HasSuffix(lc, ".html") || strings.HasSuffix(lc, ".htm") {
					needsBuild = true
					break
				}
			}
			if !needsBuild {
				// Multi-file project (Python / Go / TS): no session_build
				// step needed because each object's impl IS its
				// deliverable file. Still emit Built — NOT Checkpointed —
				// so H_checkpoint runs next and creates the checkpoint.
				// Pre-2026-05-20 we shortcut to Checkpointed here, which
				// BYPASSED checkpoint creation entirely and immediately
				// failed at H_gate (no checkpoint to satisfy [checkpoint-pass]).
				// The pb-discount PB batch exposed this — no counter
				// (HTML) instance ever hit the shortcut path so it sat
				// as latent dead code for 9 batches.
				return router.MarshalPayload(router.OuterTypeBuilt, map[string]string{
					"rootSessionID": deps.RootSessionID,
					"note":          "multi-file project: no session_build needed; impl files are the deliverable",
				}), nil
			}
			argsJSON := fmt.Sprintf(`{"session_id":%q}`, deps.RootSessionID)
			out := tools.Execute(ctx, deps.ToolSet, "session_build", argsJSON)
			if strings.HasPrefix(out, "error:") {
				return emitObstacle("build", "session_build failed: "+out), nil
			}
			return router.MarshalPayload(router.OuterTypeBuilt, map[string]string{
				"rootSessionID": deps.RootSessionID,
				"note":          out,
			}), nil
		},
	}
}

// H_checkpoint: Outer.Built → Outer.Checkpointed | Outer.GateFailBuild | Outer.Obstacle
//
// Creative: agent freezes the checkpoint, then fills every must item
// with a codeProof. Post-condition: checkpoint.Frozen + every must.Filled().
func H_checkpoint(deps *OuterDeps) router.Handler {
	return &router.HandlerFunc{
		In:  router.OuterTypeBuilt,
		Out: []string{router.OuterTypeCheckpointed, router.OuterTypeGateFailBuild, router.OuterTypeObstacle},
		Run: func(ctx context.Context, _ router.TypedValue) (router.TypedValue, error) {
			// Pre-2026-05-20: this Handler checked
			// det.BuildDeliverable == "", but that field was declared
			// on OuterStateDetail and NEVER assigned by
			// InferOuterStateDetail — so the guard fired every time on
			// HTML projects, emitting GateFailBuild → H_repair_build →
			// session_build → Built → re-enter H_checkpoint → guard
			// fires again. Batch #6 logged 147 round-trips of this
			// loop on every HTML instance. Removed: H_checkpoint now
			// always proceeds to the LLM sub-loop. If the deliverable
			// is truly missing the gate's [root-deliver] rule will
			// surface it as GateFailBuild after H_gate runs — which is
			// the proper feedback path (Checkpointed → GateFailBuild
			// is a declared edge in OuterFlowRules).
			sys := fmt.Sprintf(
				"You are the Handler 1.3 checkpoint-fill sub-agent. Your ONLY job is to record verification evidence for session %q. "+
					"Steps: (1) checkpoint_add_item for every SPEC must/should requirement (severity=must|should|waiver). "+
					"(2) checkpoint_freeze. (3) checkpoint_fill id=<item> codeProof=<file:line + key export> for every must item. "+
					"After every must item is filled, output `DONE`.",
				deps.RootSessionID)

			task := fmt.Sprintf("Fill checkpoint codeProofs for session %s.", deps.RootSessionID)

			_ = deps.runScopedLLM(ctx, sys, task, []string{
				"read_file", "list_dir", "grep", "glob",
				"checkpoint_add_item", "checkpoint_freeze", "checkpoint_fill",
				"checkpoint_show",
			})

			det2, _ := router.InferOuterStateDetail(deps.RootSessionID)
			if !det2.CheckpointFrozen {
				return emitObstacle("checkpoint", "checkpoint not frozen after sub-agent loop"), nil
			}
			if !det2.CheckpointAllFilled {
				return emitObstacle("checkpoint",
					"checkpoint frozen but at least one must item lacks codeProof"), nil
			}
			return router.MarshalPayload(router.OuterTypeCheckpointed, map[string]string{
				"rootSessionID": deps.RootSessionID,
			}), nil
		},
	}
}

// H_gate: Outer.Checkpointed → Outer.GatePassed | Outer.GateFail{Object,Checkpoint,Build} | Outer.Obstacle
//
// Deterministic: runs session_gate_check root. On PASS → GatePassed.
// On FAIL classifies reasons by category and emits the appropriate
// feedback type. Repair Handlers (below) pick those up.
func H_gate(deps *OuterDeps) router.Handler {
	return &router.HandlerFunc{
		In:  router.OuterTypeCheckpointed,
		Out: []string{
			router.OuterTypeGatePassed,
			router.OuterTypeGateFailObject,
			router.OuterTypeGateFailCheckpoint,
			router.OuterTypeGateFailBuild,
			router.OuterTypeGateFailAttrs,
			router.OuterTypeObstacle,
		},
		Run: func(ctx context.Context, _ router.TypedValue) (router.TypedValue, error) {
			argsJSON := fmt.Sprintf(`{"id":%q}`, deps.RootSessionID)
			out := tools.Execute(ctx, deps.ToolSet, "session_gate_check", argsJSON)
			if strings.HasPrefix(out, "error:") {
				return emitObstacle("gate", "session_gate_check failed: "+out), nil
			}
			// session_gate_check's actual output (verified from real
			// runs 2026-05-20 batch #8 counter-05) starts with
			// `gate: PASS · session <sid>` or `gate: FAIL · session <sid>`.
			// Pre-2026-05-20 we matched `verdict: pass` here — those
			// substrings never appear, so every PASS got mis-classified
			// as FAIL → classifyGateFail returned kind="" → emitObstacle.
			// counter-05 had 3/3 obj-conf + 2/2 attr-conf + checkpoint PASS
			// and the gate said "(no issues — session ready to finish)"
			// but the Router still reported Obstacle. Match the actual
			// `gate: pass` format (case-insensitive lower).
			lower := strings.ToLower(out)
			if strings.Contains(lower, "gate: pass") || strings.Contains(lower, "gate:pass") ||
				strings.Contains(lower, "verdict: pass") || strings.Contains(lower, "verdict=pass") {
				return router.MarshalPayload(router.OuterTypeGatePassed, map[string]string{
					"rootSessionID": deps.RootSessionID,
				}), nil
			}
			// FAIL — classify.
			kind, reasons := classifyGateFail(out)
			payload := map[string]interface{}{
				"rootSessionID": deps.RootSessionID,
				"reasons":       reasons,
			}
			switch kind {
			case "attrs":
				return router.MarshalPayload(router.OuterTypeGateFailAttrs, payload), nil
			case "object":
				return router.MarshalPayload(router.OuterTypeGateFailObject, payload), nil
			case "checkpoint":
				return router.MarshalPayload(router.OuterTypeGateFailCheckpoint, payload), nil
			case "build":
				return router.MarshalPayload(router.OuterTypeGateFailBuild, payload), nil
			default:
				return emitObstacle("gate", reasons...), nil
			}
		},
	}
}

// H_finish: Outer.GatePassed → Outer.Finished | Outer.Obstacle
//
// Deterministic: session_status root finished. Verifies the disk
// post-condition: session.Status == finished AND checkpoint verdict
// == PASS.
func H_finish(deps *OuterDeps) router.Handler {
	return &router.HandlerFunc{
		In:  router.OuterTypeGatePassed,
		Out: []string{router.OuterTypeFinished, router.OuterTypeObstacle},
		Run: func(ctx context.Context, _ router.TypedValue) (router.TypedValue, error) {
			argsJSON := fmt.Sprintf(`{"id":%q,"status":"finished"}`, deps.RootSessionID)
			out := tools.Execute(ctx, deps.ToolSet, "session_status", argsJSON)
			if strings.HasPrefix(out, "error:") {
				return emitObstacle("finish", "session_status root finished failed: "+out), nil
			}
			det, _ := router.InferOuterStateDetail(deps.RootSessionID)
			if det.Type != router.OuterTypeFinished {
				return emitObstacle("finish",
					"session_status returned ok but state inference reports "+det.Type), nil
			}
			return router.MarshalPayload(router.OuterTypeFinished, map[string]interface{}{
				"rootSessionID":   deps.RootSessionID,
				"deliverableHash": "", // future: hash of deliverable file
			}), nil
		},
	}
}

// --- Repair Handlers (feedback arcs) ---

// H_repair_object: Outer.GateFailObject → Outer.SomeConfirmed | Outer.Obstacle
//
// Routes back to H_confirm_one_loop. The repair is itself a no-op
// at this level — H_confirm_one will pick the offending object up
// because state inference puts it in RemainingObjectIDs once we
// revert its status (or because it never reached confirmed despite
// the gate's prior pass).
func H_repair_object(deps *OuterDeps) router.Handler {
	return &router.HandlerFunc{
		In:  router.OuterTypeGateFailObject,
		Out: []string{router.OuterTypeSomeConfirmed, router.OuterTypeObstacle},
		Run: func(ctx context.Context, in router.TypedValue) (router.TypedValue, error) {
			det, _ := router.InferOuterStateDetail(deps.RootSessionID)
			if len(det.RemainingObjectIDs) == 0 {
				// All objects already confirmed but gate failed at the
				// object level — this is a contradiction the gate's
				// classifier can hit when the SPEC/graph drifted. Bail.
				return emitObstacle("repair_object",
					"gate flagged an object issue but no objects are in non-confirmed state"), nil
			}
			return router.MarshalPayload(router.OuterTypeSomeConfirmed, map[string]interface{}{
				"rootSessionID":      deps.RootSessionID,
				"remainingObjectIDs": det.RemainingObjectIDs,
			}), nil
		},
	}
}

// H_repair_checkpoint: Outer.GateFailCheckpoint → Outer.Checkpointed | Outer.Obstacle
//
// Re-runs the checkpoint phase to fix codeProof / verdict issues.
func H_repair_checkpoint(deps *OuterDeps) router.Handler {
	return &router.HandlerFunc{
		In:  router.OuterTypeGateFailCheckpoint,
		Out: []string{router.OuterTypeCheckpointed, router.OuterTypeObstacle},
		Run: func(ctx context.Context, in router.TypedValue) (router.TypedValue, error) {
			sys := fmt.Sprintf(
				"You are the checkpoint-repair sub-agent for session %q. The gate FAILED at the checkpoint phase. "+
					"FIRST: call checkpoint_show to see the current state. "+
					"If the checkpoint does NOT EXIST yet (no items at all): bootstrap it — for each SPEC must/should requirement call checkpoint_add_item (severity=must|should), then checkpoint_freeze, then checkpoint_fill each must item with codeProof. "+
					"If the checkpoint already exists but has unfilled items: just call checkpoint_fill for each missing codeProof. "+
					"After all must items have codeProof, output `DONE`.",
				deps.RootSessionID)

			_ = deps.runScopedLLM(ctx, sys, "Repair checkpoint for session "+deps.RootSessionID,
				[]string{"checkpoint_show", "checkpoint_add_item", "checkpoint_freeze", "checkpoint_fill", "read_file", "grep", "glob", "list_dir"})

			det, _ := router.InferOuterStateDetail(deps.RootSessionID)
			if !det.CheckpointAllFilled {
				return emitObstacle("repair_checkpoint",
					"checkpoint still has unfilled must items after repair sub-agent"), nil
			}
			return router.MarshalPayload(router.OuterTypeCheckpointed, map[string]string{
				"rootSessionID": deps.RootSessionID,
			}), nil
		},
	}
}

// H_repair_attrs: Outer.GateFailAttrs → Outer.Checkpointed | Outer.Obstacle
//
// Gate FAILed because produced/mutated attributes on confirmed objects
// are still status=declared. Runs a focused LLM sub-loop authorised
// only to call graph_merge_attribute, instructing it to backfill the
// flagged attributes' status + valueSpace. After the sub-loop, the
// Router re-enters Checkpointed which re-runs the gate.
//
// This is the v12 fix for batch #7's "3/3 confirmed + checkpoint PASS
// but session never finishes" symptom — the gate-FAIL reason was
// attribute backfill (system.md §5), not an object issue, and the old
// classifyGateFail routed it incorrectly to H_repair_object which then
// bailed seeing RemainingObjectIDs=[].
func H_repair_attrs(deps *OuterDeps) router.Handler {
	return &router.HandlerFunc{
		In:  router.OuterTypeGateFailAttrs,
		Out: []string{router.OuterTypeCheckpointed, router.OuterTypeObstacle},
		Run: func(ctx context.Context, in router.TypedValue) (router.TypedValue, error) {
			sys := fmt.Sprintf(
				"You are the attribute-backfill repair sub-agent for session %q. "+
					"The gate FAILed with rule [attrs-backfilled]: one or more attributes produced or mutated "+
					"by a confirmed object are still status=declared. Per system.md §5: a confirmed object's "+
					"produced/mutated attributes must ALSO be confirmed with their valueSpace populated. "+
					"Steps: (1) call graph_show to see which attributes need backfilling. "+
					"(2) For each declared attribute that is `produces` or `mutates` of a confirmed object, call "+
					"graph_merge_attribute id=<attr> patch='{\"status\":\"confirmed\",\"valueSpace\":{...}}'. "+
					"The valueSpace shape depends on the attribute type — for a numeric attribute use "+
					"`{\"kind\":\"int\",\"min\":0,\"max\":1000000}` or similar; for a string `{\"kind\":\"string\",\"pattern\":\".*\"}`; "+
					"for a boolean `{\"kind\":\"bool\"}`. Pick a value space that matches the attribute's intent. "+
					"After every flagged attribute is confirmed, output `DONE`.",
				deps.RootSessionID)

			_ = deps.runScopedLLM(ctx, sys, "Backfill attributes for session "+deps.RootSessionID,
				[]string{"graph_show", "graph_merge_attribute", "read_file", "grep"})

			// No specific post-condition check here — the gate runs
			// next and either passes or re-emits the same FAIL.
			return router.MarshalPayload(router.OuterTypeCheckpointed, map[string]string{
				"rootSessionID": deps.RootSessionID,
			}), nil
		},
	}
}

// H_repair_build: Outer.GateFailBuild → Outer.Built | Outer.Obstacle
//
// Re-runs session_build deterministically. If the deliverable still
// doesn't appear, bail.
func H_repair_build(deps *OuterDeps) router.Handler {
	return &router.HandlerFunc{
		In:  router.OuterTypeGateFailBuild,
		Out: []string{router.OuterTypeBuilt, router.OuterTypeObstacle},
		Run: func(ctx context.Context, _ router.TypedValue) (router.TypedValue, error) {
			argsJSON := fmt.Sprintf(`{"session_id":%q}`, deps.RootSessionID)
			out := tools.Execute(ctx, deps.ToolSet, "session_build", argsJSON)
			if strings.HasPrefix(out, "error:") {
				return emitObstacle("repair_build", "session_build retry failed: "+out), nil
			}
			det, _ := router.InferOuterStateDetail(deps.RootSessionID)
			if det.Type != router.OuterTypeBuilt && det.Type != router.OuterTypeCheckpointed && det.Type != router.OuterTypeGatePassed {
				return emitObstacle("repair_build",
					"deliverable still missing after retry: state="+det.Type), nil
			}
			return router.MarshalPayload(router.OuterTypeBuilt, map[string]string{
				"rootSessionID": deps.RootSessionID,
			}), nil
		},
	}
}

// --- helpers ---

func confirmedSetEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[string]bool{}
	for _, x := range a {
		set[x] = true
	}
	for _, y := range b {
		if !set[y] {
			return false
		}
	}
	return true
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func isMultiFile(deps *OuterDeps) bool {
	g, err := persistence.LoadGraph(persistence.GraphDefaultPath)
	if err != nil || g == nil {
		return false
	}
	for _, o := range g.Objects {
		if o == nil || o.Impl == nil {
			continue
		}
		lc := strings.ToLower(*o.Impl)
		if strings.HasSuffix(lc, ".html") || strings.HasSuffix(lc, ".htm") {
			return false
		}
	}
	return true
}

// classifyGateFail buckets a session_gate_check FAIL report into one
// of {"object","attrs","checkpoint","build",""} based on the rule
// IDs it names. "" means the failure doesn't map to a recoverable
// feedback arc — Outer.Obstacle is the right response.
//
// Rule-name → bucket:
//   - attrs: attrs-backfilled (produced/mutated attrs still declared) — attr-fix repair
//   - object: confirmed-impl, typecalc-evidence-passing, runtime-smoke-required,
//             status-transition, port-observation-required,
//             produces-or-mutates-non-empty, object-gate
//   - checkpoint: checkpoint-pass, root-deliver, outputs-tests-non-empty
//   - build: deliverable-exists, deliverable-non-empty
//
// Order matters: attrs is checked BEFORE object because batch #7 had
// both classifications matching the same string (the "object" bucket's
// keyword list included attrs-backfilled by accident, sending the
// repair to H_repair_object which then bailed on RemainingObjectIDs=[]).
func classifyGateFail(report string) (kind string, reasons []string) {
	lines := strings.Split(report, "\n")
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		reasons = append(reasons, ln)
	}
	low := strings.ToLower(report)
	switch {
	case strings.Contains(low, "attrs-backfilled"):
		return "attrs", reasons
	case strings.Contains(low, "deliverable-exists") || strings.Contains(low, "deliverable-non-empty"):
		return "build", reasons
	case strings.Contains(low, "checkpoint-pass") || strings.Contains(low, "root-deliver") ||
		strings.Contains(low, "outputs-tests-non-empty"):
		return "checkpoint", reasons
	case strings.Contains(low, "confirmed-impl") || strings.Contains(low, "typecalc-evidence-passing") ||
		strings.Contains(low, "runtime-smoke-required") ||
		strings.Contains(low, "port-observation-required") || strings.Contains(low, "produces-or-mutates-non-empty") ||
		strings.Contains(low, "status-transition") || strings.Contains(low, "object-gate"):
		return "object", reasons
	}
	return "", reasons
}

// Verify imports are all used (compiler will yell otherwise).
var _ = checkpoint.VerdictPass
