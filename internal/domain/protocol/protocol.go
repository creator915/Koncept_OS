// Package protocol is the v9.0 single source of truth for kcpos's
// runtime protocol — the rules an agent (and human operators) must
// follow when driving a project through the type-driven verification
// pipeline.
//
// Pre-v9.0 the rules lived in TWO places that drifted from each other:
//   - CLAUDE.md (project root markdown, written for humans + Claude Code)
//   - internal/agent/system.md (baked into the kcpos binary, fed to the LLM)
//
// kcpos never read CLAUDE.md at runtime, so any rule that lived only in
// CLAUDE.md was invisible to the agent. system.md duplicated most of it
// in different wording, with predictable drift. v9.0 collapses the
// situation: the *runtime* protocol lives here as Go data; system.md
// imports it via Describe() for the LLM-facing prose; CLAUDE.md is
// archived as docs/legacy-protocol-2026-05-09.md.
//
// Adding/changing a rule = editing this file. Everything else flows
// from it.
package protocol

import (
	"fmt"
	"strings"
)

// Stage represents one product-readiness stage produced by the three
// core Handlers. Only the final stage is a deliverable; preceding
// stages are work-in-progress states. Naming aligned to KonceptOS
// 2026-05-19: Handler 1.1 / 1.2 / 1.3 is the canonical vocabulary;
// the pre-v9.0 L0..L4 labels were the same mechanism under an older
// name and have been retired (see project_handler_x3_canonical memo).
type Stage struct {
	ID            string // "H1.1" / "H1.2" / "H1.3.unit" / "H1.3.integ" / "H1.3.final"
	Handler       string // owning Handler ("1.1" | "1.2" | "1.3")
	Content       string // what populates this stage
	IsDeliverable bool   // only H1.3.final
}

// Layer is a deprecated alias kept until external callers (if any)
// migrate. Identical to Stage.
//
// Deprecated: use Stage. Will be removed in a future revision.
type Layer = Stage

// Stages is the canonical stage table. Five rows in chronological
// order; H1.1 → H1.2 → H1.3 (split into unit / integ / final).
var Stages = []Stage{
	{ID: "H1.1", Handler: "1.1", Content: "K/defs/*.ts signatures + K/graph.json node declarations (Hypergraph Handler product)"},
	{ID: "H1.2", Handler: "1.2", Content: "src/*.impl.ts implementations + graph status=confirmed (Code-Compile-Test Handler product)"},
	{ID: "H1.3.unit", Handler: "1.3", Content: "src/*.test.ts unit/contract tests passing (Session Handler — per-object verification phase)"},
	{ID: "H1.3.integ", Handler: "1.3", Content: "integration.test.ts + dist/bundle.js (if bundled-form project) (Session Handler — integration phase)"},
	{ID: "H1.3.final", Handler: "1.3", Content: "checkpoint.json all must items have codeProof AND finalVerdict=PASS, root session output aggregates all child outputs, executable deliverable file exists at the conventional path (Session Handler — finish/deliverable)", IsDeliverable: true},
}

// Layers is a deprecated alias for Stages.
//
// Deprecated: use Stages.
var Layers = Stages

// PathBTriggers are the OR-conditions that force a session into path B
// (delegated work via spawn_subagent + child sessions) instead of path
// A (work all in current parent context).
//
// v8.7 prose had three OR-triggers (object count, estimated impl LOC,
// spec chapter span). v9.0 collapsed those into a single
// SessionPathThreshold=3 const and added a "testable" qualifier that
// silently disabled path B for HTML / Canvas / lang-without-runner
// projects (no object is "testable" in those, so the rule never fires).
// The 2026-05-11 Terraria batch (5/5 instances died trying to write the
// whole index.html in the parent context) proved the simplification was
// wrong: each instance had 7-19 declared objects ≥3K-8K estimated LOC
// and spanned 24 spec chapters, but path B never fired so the parent
// agent self-immolated.
//
// v9.0.3 restores the v8.7 semantics: any of these triggers fires
// path B. The "testable" qualifier is gone — HTML / Canvas-only
// objects count just like Go / TS objects.
type PathBTriggerConfig struct {
	Objects      int // ≥ this many declared objects in current session
	ImplLOC      int // ≥ this many estimated impl lines (sum across decomposed objects)
	SpecChapters int // ≥ this many spec.md top-level chapters spanned by the task
}

// PathBTriggers is the canonical trigger table. Any single field
// being met forces path B.
var PathBTriggers = PathBTriggerConfig{
	Objects:      3,
	ImplLOC:      400,
	SpecChapters: 2,
}

// SessionPathThreshold is kept as a deprecated alias for backwards
// compatibility with callers that haven't migrated to PathBTriggers.
// Mirrors PathBTriggers.Objects.
//
// Deprecated: use PathBTriggers.Objects directly.
const SessionPathThreshold = 3

// ObjectGranularityRange — number of testable objects a single project
// should decompose into. system.md previously said "3–6 on a small
// project, not 8–12". Encoded as advisory hints; not enforced.
const (
	ObjectsPerProjectMin     = 3
	ObjectsPerProjectMax     = 6
	ObjectsPerProjectWarnMax = 12
)

// FileSizeLimitLines is the hard cap on a single .ts file before it
// must be split. From CLAUDE.md §4.3 — preventing the v4 disaster where
// one 60-tile data.ts hit the 32K output-token cap and the agent
// looped retrying the same write.
const FileSizeLimitLines = 1500

// StoryPointScale is the v9.5 Fibonacci-based complexity gauge for
// objects. storyPoints >= 8 blocks status transition to implementing
// until the object is split via graph_split_object. The scale is
// anchored to real-code examples from batches v94/v93.
//
// Examples:
//   - 1pt: pure arithmetic (`add(a,b)`, `int_to_str(n)`)
//   - 2pt: single loop/iterate (`has_close_elements`, `all_prefixes`)
//   - 3pt: multi-branch / boundary handling (`validate_email`, `PollInput`)
//   - 5pt: multi-step workflow (`ComputeCamera`, `SaveLoad`)
//   - 8pt: split point — must decompose (`GenerateWorld`, `RenderFrame`)
//   - 13pt: unrepresentable — rewrite spec ("entire game loop", "Player subsystem")
type StoryPointScale int

const (
	StoryPoint1  StoryPointScale = 1
	StoryPoint2  StoryPointScale = 2
	StoryPoint3  StoryPointScale = 3
	StoryPoint5  StoryPointScale = 5
	StoryPoint8  StoryPointScale = 8  // mandatory split threshold
	StoryPoint13 StoryPointScale = 13 // unrepresentable
)

// ValidStoryPoints returns true if n is a valid Fibonacci story point.
func ValidStoryPoints(n int) bool {
	switch n {
	case 1, 2, 3, 5, 8, 13:
		return true
	default:
		return false
	}
}

// StoryPointSplitThreshold is the minimum story point value that
// requires decomposition before implementation. Objects with this
// or higher points MUST be split via graph_split_object.
const StoryPointSplitThreshold = 8

// v9.2 — WaiverFloodThreshold* constants removed along with the
// obstacle/waiver mechanism. The gate is now binary; there's no flood
// to threshold against.

// CycleCap is the maximum number of failed reviews on the same object
// before review hard-blocks. v9.2 — pre-v9.2 the agent could escalate
// to an obstacle here; now the cap means "stop, fix the impl/graph
// or refactor". Mirrors core.CycleCap — kept in sync so changes
// are coordinated.
const CycleCap = 5

// RootFinishStep is one ordered step of Handler 1.3's root-session
// finish flow. Pre-v9.0 these had R1..R4 short codes; the canonical
// names now are H1.3.aggregate / H1.3.build / H1.3.checkpoint /
// H1.3.gate — same steps, retired vocabulary.
type RootFinishStep struct {
	ID    string // "H1.3.aggregate" / "H1.3.build" / "H1.3.checkpoint" / "H1.3.gate"
	Name  string
	Doing string // imperative one-liner
}

// RootFinishFlow is the canonical Handler 1.3 root-finish step
// sequence. Renamed in 2026-05-19 (project_handler_x3_canonical):
// aggregate → build/test → checkpoint fill → final gate. v9.0 had
// dropped the gameplayProof step that lived between aggregate and
// build in v8.7.
var RootFinishFlow = []RootFinishStep{
	{ID: "H1.3.aggregate", Name: "aggregate", Doing: "session_aggregate root — collect implementations/tests/newSignatures from every child session"},
	{ID: "H1.3.build", Name: "build+test", Doing: "for HTML/single-file projects: call session_build which reads every object's implContent and emits the assembled deliverable (e.g. index.html). For multi-file projects: run npm run build / cargo build / etc. Then run the full test suite. If single-file with no extra build step needed, just verify the deliverable file exists and is non-empty"},
	{ID: "H1.3.checkpoint", Name: "checkpoint-fill", Doing: "checkpoint_fill every must item with codeProof (file:line + key export)"},
	{ID: "H1.3.gate", Name: "gate", Doing: "session_gate_check root — fixed-point: any FAIL means iterate, only PASS allows session_status root finished"},
}

// FinishCondition is one of the "unfinished checklist" predicates from
// CLAUDE.md §5.1.1. Each is a question the agent answers "yes" to
// continue working. v9.0 encodes them as a structured list so the
// agent prompt and the gate can both reference the canonical set.
type FinishCondition struct {
	ID            string // "C1".."C7"
	Scope         string // "any" | "root-only"
	Description   string
	GateRuleName  string // session_gate_check rule that enforces this (empty when implicit)
}

// FinishConditions is the canonical list. Reduced from 8 → 7 in v9.0
// (the gameplayProof condition was removed when gameplayProof itself
// was removed). Mirrors CLAUDE.md §5.1.1 (pre-v9.0).
var FinishConditions = []FinishCondition{
	{ID: "C1", Scope: "any", Description: "Every object created/modified by this session in graphDiff has status=confirmed and impl non-null", GateRuleName: "root-deliver"},
	{ID: "C2", Scope: "any", Description: "Every impl file exists at the declared path and has size > 0", GateRuleName: "root-deliver"},
	{ID: "C3", Scope: "any", Description: "Every confirmed object has typecalc evidence kind=test ok=true. For HTML deliverables, kind=runtime ok=true REPLACES kind=test (v9.3 — the vm.Script harness can't model the browser, so synthesize+test are skipped on the HTML branch). v9.2: no obstacle/waiver substitute path.", GateRuleName: "typecalc-evidence-passing"},
	{ID: "C4", Scope: "any", Description: "No child session is in waiting or active status — all finished or deleted", GateRuleName: "children-finished"},
	{ID: "C5", Scope: "root-only", Description: ".impl.ts file count >= must-item count * 0.5 (single-file deliverables count as ≥1)", GateRuleName: ""},
	{ID: "C6", Scope: "root-only", Description: "checkpoint.json: every must item has codeProof, summary.finalVerdict=PASS", GateRuleName: "checkpoint-pass"},
	{ID: "C7", Scope: "root-only", Description: "Root session output.implementations / output.tests aggregate all child outputs", GateRuleName: "outputs-tests-non-empty"},
}

// AntiPattern is a behavior the agent must NOT exhibit. v9.0 encodes
// the prose ❌ bullet list from CLAUDE.md §5.1.1 so system.md can
// generate identical text and the static checker can grep for some of
// them in transcripts (future work).
type AntiPattern struct {
	ID          string
	Description string
}

var AntiPatterns = []AntiPattern{
	{ID: "AP1", Description: "Only created defs/*.ts signatures, src/ is empty, then marked session finished or stopped"},
	{ID: "AP2", Description: "Marked root session finished without opening any child session"},
	{ID: "AP3", Description: "Test files exist but contain it.skip / expect(true).toBe(true) placeholders"},
	{ID: "AP4", Description: "Treated 'checkpoint frozen' or 'signatures designed' as milestone delivery — they are work steps, not products"},
	{ID: "AP5", Description: "Wrote code + tests green + bundle built then claimed done — that is only Handler 1.3 mid-stage (H1.3.integ); Handler 1.3 finish (H1.3.final) requires codeProof full fill + finalVerdict=PASS + deliverable file present"},
	{ID: "AP6", Description: "All children finished → reported done — root still owes the §5.5 flow"},
	{ID: "AP7", Description: "Silently scoped down deliverables when token/attention dropped — narrow the SPEC explicitly with the user instead. v9.2: 'declare obstacle' is no longer a valid exit; the gate refuses confirm regardless."},
	{ID: "AP8", Description: "long-markdown-bulk-read — Used read_file with force=true (or any bulk read) on a SPEC/DESIGN/etc. markdown file ≥5K tokens, dumping the entire document into one context. Always use markdown_outline first, then markdown_section for the specific chapters needed. This applies to any long markdown — not just SPEC.md."},
	{ID: "AP9", Description: "js-defs-as-impl — Wrote a JS def file (K/defs/<id>.js) with non-throw function bodies (e.g. `function Foo(x){ return 0; }`) and used that as 'implementation' evidence. JS defs MUST be throw-stubs; the real impl is in `implContent` on the graph object (v10), materialised to K/frags/<id>.js by session_build (see Single-file deliverable model). Static check `defs-must-throw` fires on any non-throw body."},
	{ID: "AP10", Description: "frag-trivial-stub — Set implContent (or wrote impl) with a one-line literal return body (`return 0;` / `return [];` / `return {};`) just to make typecalc_compile pass. Object code must contain real logic — at least one control-flow statement (if/for/while) or non-literal computation. Static check `frags-non-trivial` fires on the emitted fragment after session_build."},
	{ID: "AP11", Description: "unmodeled-function-in-fragment — Set implContent containing a top-level `function Foo(...)` where Foo is NOT a graph object (and not the parent object's ImplSymbol). Such functions ship to the deliverable via session_build but bypass the verification chain entirely — they never reach confirm_object. session_build refuses to assemble when ANY object's implContent contains an unmodeled top-level function. Fix: either (a) model the helper as its own graph object, or (b) inline it as `const foo = (...) => ...` / closure inside the modeled function so it's not a top-level declaration."},
	{ID: "AP12", Description: "def-multi-entity-file — Wrote a JS def file (K/defs/<id>.js) containing functions for multiple objects, or mixing attribute @typedef with object function declarations. Each def file must declare exactly the entity that matches its filename — the function name must equal the object id OR its declared ImplSymbol. Static check `defs-entity-1to1` fires on extras."},
	{ID: "AP13", Description: "def-frag-name-mismatch — The set of top-level functions declared in K/defs/<id>.js doesn't match the set inside the object's implContent (missing from impl, or extra in impl). Static check `frags-content-matches-def` enforces the 1:1 mapping (against session_build's emitted fragment) so the def's @param / @returns / @example ground truth covers every function the object ships."},
	{ID: "AP14", Description: "rollback-instead-of-dismiss — Used session_rollback (pre-v9.3: session_delete) to 'clean up' a finished subagent's session entry. session_rollback is DESTRUCTIVE: it reverse-applies graphDiff and deletes def/impl files the session produced. v9.0.6 terraria-03, v92-01, and v92-02 all hit this: v92-02 rolled back the root and lost 20 source files including index.html itself. For additive cleanup (just retire the session record) use session_dismiss — it touches nothing else. Only use session_rollback when you genuinely want to undo the work."},
	{ID: "AP15", Description: "chain-spawned-siblings — In pre-v9.3 spawn_subagent, the auto-created session's parent was the *currently focused* session, not the root. When a subagent spawned children of its own without resetting focus, those siblings became descendants 4–7 levels deep (v9.0.6 terraria-05, v92-03, v92-04). v9.3 default is parent=FindRoot(focus); if you intentionally want nesting (wave-2 coordinator), pass `parent=<id>` explicitly. Don't fall back to the old chain pattern by accident."},
	{ID: "AP16", Description: "monolithic-html-no-impl — Set obj.impl=index.html for every graph object but never set implContent, then wrote the entire single-file deliverable as one giant inline `<script>` in the parent's writing of index.html. The v10/v12 canonical single-file form is: impl=index.html on every object + implContent on each object carrying that function's body. kcpos handles internal frag-emission and assembly; you only set those two fields. (Older v9.x docs mentioned `implFragment=K/frags/<id>.js` — that field is now auto-derived; you do not set or write to it.)"},
	{ID: "AP17", Description: "html-without-incremental-build — Confirmed an HTML object whose deliverable was a stub (session_build hadn't run yet). v9.3.1 fix: confirm_object's HTML branch auto-runs session_build (reads every object's implContent, emits the assembled deliverable) right before each runtime_smoke, so the deliverable always reflects current implContent. Agents driving the chain step-by-step manually must still call session_build themselves before runtime_smoke."},
}

// Describe renders the protocol as a markdown document suitable for
// the LLM system prompt or human reading. Pre-v9.0 this content lived
// in CLAUDE.md / system.md as prose; v9.0 generates the same text from
// the structured tables above. Single source of truth — edit the
// constants/tables, not the rendered output.
func Describe() string {
	var b strings.Builder
	b.WriteString("# kcpos runtime protocol (v11 — Handler×3, v10 implContent SoT, 2026-05-20)\n\n")
	b.WriteString("This protocol is generated from internal/domain/protocol/protocol.go. ")
	b.WriteString("The agent must follow these rules; the gate (session_gate_check) ")
	b.WriteString("enforces a structural subset programmatically.\n\n")
	b.WriteString("**Canonical vocabulary**: three Handlers own the pipeline — Handler 1.1 Hypergraph (graph nodes + defs), Handler 1.2 Code-Compile-Test (impl + verification chain → confirmed), Handler 1.3 Session (gate / checkpoint / root finish / deliverable). The pre-v9.0 L0..L4 stage names and R1..R4 root-finish IDs describe the SAME mechanism and have been retired; this document uses Handler 1.1 / 1.2 / 1.3 throughout.\n\n")

	b.WriteString("## Handler stages (only the final stage is a deliverable)\n\n")
	b.WriteString("The work pipeline is owned end-to-end by three Handlers (1.1 Hypergraph / 1.2 Code-Compile-Test / 1.3 Session). Pre-v9.0 vocabulary used L0..L4 stage names — those refer to the SAME mechanism and are retired.\n\n")
	for _, s := range Stages {
		marker := "  "
		if s.IsDeliverable {
			marker = "★ "
		}
		fmt.Fprintf(&b, "%s**%s** (Handler %s) — %s\n", marker, s.ID, s.Handler, s.Content)
	}
	b.WriteString("\n")

	b.WriteString("## Finish-readiness checklist (§5.1.1)\n\n")
	for _, c := range FinishConditions {
		scope := ""
		if c.Scope == "root-only" {
			scope = " *(root only)*"
		}
		rule := ""
		if c.GateRuleName != "" {
			rule = fmt.Sprintf(" — enforced as [%s]", c.GateRuleName)
		}
		fmt.Fprintf(&b, "- **%s**%s: %s%s\n", c.ID, scope, c.Description, rule)
	}
	b.WriteString("\n")

	b.WriteString("## Anti-patterns (never do these)\n\n")
	for _, a := range AntiPatterns {
		fmt.Fprintf(&b, "- **%s**: %s\n", a.ID, a.Description)
	}
	b.WriteString("\n")

	b.WriteString("## Decomposition thresholds — path B is mandatory when ANY of these fires\n\n")
	fmt.Fprintf(&b, "- ≥%d declared objects in current session (regardless of language; HTML/Canvas-only objects count just like Go/TS objects)\n", PathBTriggers.Objects)
	fmt.Fprintf(&b, "- ≥%d estimated implementation lines (sum across the objects you've declared or plan to declare)\n", PathBTriggers.ImplLOC)
	fmt.Fprintf(&b, "- ≥%d spec.md top-level chapters spanned by the task\n", PathBTriggers.SpecChapters)
	fmt.Fprintf(&b, "- Target %d–%d objects per project; ≥%d objects usually means over-decomposed\n", ObjectsPerProjectMin, ObjectsPerProjectMax, ObjectsPerProjectWarnMax)
	fmt.Fprintf(&b, "- Single .ts file ≤ %d lines; split large data tables aggressively (32K output-token cap on each write)\n\n", FileSizeLimitLines)
	b.WriteString("**Why path B matters (2026-05-11 Terraria batch lesson)**: in path A the parent agent's context accumulates every tool result, every transcript turn, and (worst) the full impl bytes it's trying to write. For a 1500-line SPEC project this overflows the LLM stream's response deadline and the run dies before reaching any confirm_object call. In path B, each child agent has a *fresh context* containing only its assigned object — the parent never sees impl bytes, only one-line summary strings from each child. This keeps the parent context bounded regardless of project size.\n\n")

	b.WriteString("## Handler 1.3 root-finish flow\n\n")
	b.WriteString("These four ordered steps complete Handler 1.3 at the root session. The 1.3-internal sub-IDs (H1.3.aggregate / H1.3.build / H1.3.checkpoint / H1.3.gate) replace the retired R1..R4 short codes.\n\n")
	for _, r := range RootFinishFlow {
		fmt.Fprintf(&b, "- **%s (%s)**: %s\n", r.ID, r.Name, r.Doing)
	}
	b.WriteString("\n")

	b.WriteString("## Verification is binary (v9.2)\n\n")
	b.WriteString("- pre-v9.2 had `typecalc_waive` + `typecalc_obstacle` — an explicit escape pair the gate accepted as a substitute for real evidence. The 2026-05-12 Terraria batch retro showed it was theater (5/5 instances rode structural waivers into confirmed, 4/5 shipped broken). **Both tools and the whole pathway are removed.**\n")
	b.WriteString("- the gate is now BINARY: every confirmed object has real `kind=test ok=true` (or `kind=runtime ok=true` for HTML deliverables), OR it cannot be confirmed.\n")
	b.WriteString("- there is no flood threshold to detect because there are no waivers to flood.\n")
	fmt.Fprintf(&b, "- %d failed reviews on the same object → review hard-blocks. Fix the impl/graph and retry from scratch — there is no \"declare obstacle and move on\" option.\n", CycleCap)
	b.WriteString("- when a language has no in-tree runner (typecalc_test returns Insufficient), confirmation IS impossible. Resolve by: (a) restructuring the impl into a runner-supported language (Go/TS/JS/HTML/Python), OR (b) extending `internal/typecalc/lang/` to add a runner for the language.\n\n")

	b.WriteString("## Port observation invariants (v9.0.1)\n\n")
	b.WriteString("- `portObservation` is a map `{<attribute_id>: <extractor>}` — **the key must equal a graph attribute id from this object's `produces` or `mutates`** (graph attribute IDs are snake_case)\n")
	b.WriteString("- the EXTRACTOR value tracks the impl-side identifier — for `return.gameStatus` you'd write `portObservation:{\"game_status\":\"return.gameStatus\"}`, NOT `{\"gameStatus\":\"return.gameStatus\"}`\n")
	b.WriteString("- static check `port-observation-orphan-key` (v9.0.1 F) fires at write-time on key mismatch and suggests the right id\n")
	b.WriteString("- if your impl-side symbol name differs from the object id, declare it explicitly: `graph_merge_object id=<obj> patch='{\"implSymbol\":\"<jsName>\"}'` (now allowlisted in v9.0.1 A)\n\n")

	b.WriteString("## v9.0.2 inner-chain changes\n\n")
	b.WriteString("- **confirm_object can self-repair**: the chain's inner FixImpl step now calls an LLM with a STRUCTURED-edit response format (`TYPE: Edit [{path, search, replace}]` vs `TYPE: Obstacle <reason>`). On Edit it literal-string-applies the diff and re-walks compile→describe→synth→test automatically. You may see `applied N edit(s)` in `confirm_object` output — that's the chain repairing in place, not you. No need to edit + re-invoke if the chain can handle it.\n")
	b.WriteString("- **describe stays at the contract level**: numeric magic constants from the impl (e.g. `300`, `Math.random()*768`) MUST NOT appear in the spec description unless they're declared invariants. Test synthesis inherits description-level vocabulary, so contract-anchored descriptions produce contract-anchored tests.\n")
	b.WriteString("- **synthesize uses ranges for derived values**: only DECLARED INVARIANTS (named constants, enum members, status strings) take `equals`. Arithmetic-derived numbers must use `between: [lo, hi]` with ±1% tolerance. Object shape validation uses `type` not `equals: {}`.\n")
	b.WriteString("- **per-object SymbolHash isolation**: spec-stale / evidence-stale now compare per-object impl-fragment hashes instead of whole-file hash, so editing one object's function body doesn't invalidate every sibling's spec. This is a *staleness-detection* improvement only — it does NOT authorize a single agent to write the whole single-file deliverable. See \"Single-file deliverable model\" below for how multi-object HTML projects must actually be assembled.\n\n")

	b.WriteString("## What \"verified\" means — verification is not theater (v9.0.6)\n\n")
	b.WriteString("kcpos's value proposition rests on one invariant: **every byte of code that ships to the deliverable has passed through confirm_object on a graph object**. The verification chain (compile → describe → synthesize → test → review) only runs on entities that exist in the graph. Code that exists outside the graph — helper functions tucked into a def file, utility methods in a fragment that aren't declared in the def, internal functions invented during impl — is **unverified by construction**. Pre-v9.0.6 an agent could trivially ship unverified code: write `function helper(...){...}` somewhere in a fragment, session_build would happily concat it, the user would run it in the browser, kcpos would report \"all green\".\n\n")
	b.WriteString("v9.0.6 closes this with four enforcement layers, in increasing strictness:\n\n")
	b.WriteString("1. **`defs-entity-1to1`** (static check, per-object) — every function in `K/defs/<id>.js` must have a name equal to `<id>` or its `ImplSymbol`. Extra functions in the def file are flagged.\n")
	b.WriteString("2. **`frags-content-matches-def`** (static check, per-object) — the set of top-level functions in the object's implContent (as emitted by session_build) must equal the set declared in `K/defs/<id>.js`. Missing or extra functions are flagged.\n")
	b.WriteString("3. **`defs-must-throw`** + **`frags-non-trivial`** (static checks) — together stop stub bodies from posing as evidence.\n")
	b.WriteString("4. **session_build refusal** (HARD GATE) — before assembling the deliverable, session_build scans every object's implContent for top-level `function <Name>` declarations and intersects with the graph object/implSymbol set. ANY top-level function in implContent missing from the graph causes session_build to refuse: no deliverable produced, agent must either model the function as a graph object (and put it through confirm_object) or rewrite it as a scoped helper. Top-level declarations are the only shape rejected — arrow functions, closures, methods on object literals, and IIFEs are all fine since they're scoped.\n\n")
	b.WriteString("**Why this matters**: \"shipped without confirm_object\" = \"the user gets code that kcpos never claimed was correct\". The whole point of the verification chain is the guarantee that the deliverable is bounded by what was verified. v9.0.5 had this guarantee in principle but allowed a bypass (helpers smuggled in via fragment writes). v9.0.6 closed it by construction; v10 finalised it by making implContent on the graph the only writable surface (write_file to K/frags/* is hard-refused).\n\n")

	b.WriteString("## JS def files — throw-stub contract (v9.0.5)\n\n")
	b.WriteString("TypeScript uses declaration-only syntax for defs (`export function Foo(x: T): U;`) so the file syntactically cannot carry an implementation. JavaScript has no equivalent: every `function Foo(...)` MUST have a `{...}` body or the file is a parse error. To keep the contract/impl boundary clean in JS projects, kcpos requires this exact pattern for every function in `K/defs/<id>.js`:\n\n")
	b.WriteString("```js\n")
	b.WriteString("/**\n")
	b.WriteString(" * Foo: short one-line summary.\n")
	b.WriteString(" *\n")
	b.WriteString(" * @param {{x: number, y: number}} input\n")
	b.WriteString(" * @returns {{score: number}}\n")
	b.WriteString(" *\n")
	b.WriteString(" * @example\n")
	b.WriteString(" * Foo({x: 1, y: 2})  // → {score: 3}\n")
	b.WriteString(" * @example boundary\n")
	b.WriteString(" * Foo({x: 0, y: 0})  // → {score: 0}\n")
	b.WriteString(" */\n")
	b.WriteString("function Foo(input) { throw new Error(\"Foo: contract-only; set implContent on the graph object\"); }\n")
	b.WriteString("```\n\n")
	b.WriteString("Two static rules enforce this:\n\n")
	b.WriteString("- **`defs-must-throw`**: every function in `K/defs/<id>.js` must have a body whose first statement is `throw new Error(...)`. Anything else (`return 0`, real logic, an empty body) is flagged. This is what stops agents from writing a stub like `function Foo(){return 0;}` and using the def as fake impl evidence — calling that stub would always 'succeed' which lets bogus tests pass.\n")
	b.WriteString("- **`frags-non-trivial`**: every function emitted from implContent (one per object, in the assembled deliverable) must have a body with at least one control-flow statement or a non-literal return expression. Single-line `return 0;` / `return [];` / `return {};` / empty body / pass-through `return x;` are rejected. Combined with `defs-must-throw`, an agent can no longer bypass the verification chain with stub bodies — neither the def nor the implContent accepts them.\n\n")
	b.WriteString("**`@example` blocks** are not decoration: `typecalc_synthesize_tests` extracts them as input/output ground truth and feeds them to the LLM verbatim. The synthesized test suite is required to cover every `@example` in addition to its own boundary cases. Treat `@example` as the contract you're signing — write at least one happy path and one edge case per function.\n\n")

	b.WriteString("## Chapter-granular markdown access (v9.0.4)\n\n")
	b.WriteString("**Any** markdown document the agent reads — SPEC.md, DESIGN.md, third-party docs, etc. — that exceeds ~5K tokens MUST be accessed chapter-by-chapter, never bulk-read into one context:\n\n")
	b.WriteString("1. `markdown_outline <path>` returns the chapter list (id, title, line range, ~tokens) — typically 1-2K tokens regardless of source size. The parent agent uses this to plan path B: which chapters cover which graph objects.\n")
	b.WriteString("2. `markdown_section <path> <section_id>` returns one chapter's body. Child agents in path B only fetch the chapter(s) covering their assigned object — fresh context per child, bounded regardless of total SPEC size.\n")
	b.WriteString("3. `markdown_validate <path>` checks that the doc is well-structured (numeric ids, unique titles, no chapter > 5K tokens). Run before path-B decomposition to catch structurally unfit docs early — if it fails, the user must restructure before kcpos can usefully decompose.\n")
	b.WriteString("4. `read_file` auto-falls-back to the outline when the target is markdown above the threshold. Setting `force=true` bypasses this and dumps the whole file — see AP8.\n\n")
	b.WriteString("**Why this matters**: a 1500-line SPEC is ~28K tokens; tripled, ~84K tokens. Bulk-reading at session start consumes most of the LLM context window before any reasoning begins, and every child agent that does the same multiplies the cost. Chapter-granular access keeps both parent (~outline only) and each child (~one chapter) bounded at a small fraction of total spec size.\n\n")

	b.WriteString("## Single-file deliverable model (v12 — HTML/Canvas projects)\n\n")
	b.WriteString("For projects whose deliverable is `index.html` (or any single artifact aggregating code from many objects), **you set exactly two fields per graph object**:\n\n")
	b.WriteString("1. `impl: \"index.html\"` — the deliverable path, shared across all objects of the single-file project.\n")
	b.WriteString("2. `implContent: \"function Foo(...){ ...actual code... }\"` — the source code itself, stored on the graph object. Set via `graph_merge_object id=<id> patch='{\"implContent\":\"...\"}'`.\n\n")
	b.WriteString("That's it. kcpos handles everything else internally:\n\n")
	b.WriteString("- **Handler 1.3's build step (H1.3.build)** runs `session_build` which reads every object's `implContent` and emits the assembled deliverable (default reference mode: per-object script files referenced from index.html; inline mode: concatenated into one block).\n")
	b.WriteString("- **Handler 1.3's gate step (H1.3.gate)** verifies the assembled `index.html` exists, is non-empty, and passes runtime_smoke.\n\n")
	b.WriteString("**Why this matters (v10/v12)**: pre-v10 the agent wrote per-object code files to disk under K/frags/, which the chain ALSO read via `obj.implContent` if set — two stores, drift inevitable. v10 closed the drift by making `implContent` the only source. v12 hides the internal staging path from the prompt entirely: you set `impl` + `implContent`, and kcpos handles the rest. (Direct write_file to internal staging paths is hard-refused — there's nothing for the agent to do there.)\n\n")

	return b.String()
}
