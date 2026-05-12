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

// Layer represents the L0..L4 product hierarchy. Only L4 is a
// deliverable; L0..L3 are work-in-progress states. This matches
// CLAUDE.md §0 (pre-v9.0) — encoded here so the gate / hooks / docs
// generator all reference the same definitions.
type Layer struct {
	ID          int    // 0..4
	Name        string // L0..L4
	Content     string // what populates this layer
	IsDeliverable bool // L4 only
}

// Layers is the canonical layer table. Order matches CLAUDE.md §0.
var Layers = []Layer{
	{ID: 0, Name: "L0", Content: "K/defs/*.ts signatures + graph.json node declarations"},
	{ID: 1, Name: "L1", Content: "src/*.impl.ts implementations + graph status=confirmed"},
	{ID: 2, Name: "L2", Content: "src/*.test.ts unit/contract tests passing"},
	{ID: 3, Name: "L3", Content: "integration.test.ts + dist/bundle.js (if bundled-form project)"},
	{ID: 4, Name: "L4", Content: "checkpoint.json all must items have codeProof AND finalVerdict=PASS, root session output aggregates all child outputs, executable deliverable file exists at the conventional path", IsDeliverable: true},
}

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

// WaiverFloodThreshold is the share of confirmed objects that may pass
// via obstacle+waiver pair before the gate flags systematic
// verification-bypass. Pair count >= total*3/4 triggers, when total >=
// WaiverFloodMin.
const (
	WaiverFloodThresholdNum = 3 // numerator of 3/4 = 75%
	WaiverFloodThresholdDen = 4
	WaiverFloodMin          = 4
)

// CycleCap is the maximum number of failed reviews on the same object
// before the agent must escalate to an obstacle. Mirrors typecalc.CycleCap
// — kept in sync so changes are coordinated.
const CycleCap = 5

// RootFinishStep is one ordered step of the root-session finish flow.
// Pre-v9.0 these lived in CLAUDE.md §5.5 as R1..R5; v9.0 encodes them
// so the agent system prompt + gate can reference the same list.
type RootFinishStep struct {
	ID    string // "R1".."R4"
	Name  string
	Doing string // imperative one-liner
}

// RootFinishFlow is the canonical root-finish step sequence. v9.0
// dropped the gameplayProof step (was R3 in v8.7); current sequence
// is R1 aggregate → R2 build/test → R3 checkpoint fill → R4 final gate.
var RootFinishFlow = []RootFinishStep{
	{ID: "R1", Name: "aggregate", Doing: "session_aggregate root — collect implementations/tests/newSignatures from every child session"},
	{ID: "R2", Name: "build+test", Doing: "for HTML/single-file projects with implFragment: call session_build to concatenate fragments into the deliverable (e.g. index.html). For multi-file projects: run npm run build / cargo build / etc. Then run the full test suite. If single-file with NO fragments, just verify the file exists and is non-empty"},
	{ID: "R3", Name: "checkpoint-fill", Doing: "checkpoint_fill every must item with codeProof (file:line + key export)"},
	{ID: "R4", Name: "gate", Doing: "session_gate_check root — fixed-point: any FAIL means iterate, only PASS allows session_status root finished"},
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
	{ID: "C3", Scope: "any", Description: "Every confirmed object has typecalc evidence (kind=test ok=true OR obstacle+waiver)", GateRuleName: "typecalc-evidence-passing"},
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
	{ID: "AP5", Description: "Wrote code + tests green + bundle built then claimed done — that is only L3; L4 requires codeProof full fill + finalVerdict=PASS + deliverable file present"},
	{ID: "AP6", Description: "All children finished → reported done — root still owes the §5.5 flow"},
	{ID: "AP7", Description: "Silently scoped down deliverables when token/attention dropped — declare obstacles instead"},
	{ID: "AP8", Description: "long-markdown-bulk-read — Used read_file with force=true (or any bulk read) on a SPEC/DESIGN/etc. markdown file ≥5K tokens, dumping the entire document into one context. Always use markdown_outline first, then markdown_section for the specific chapters needed. This applies to any long markdown — not just SPEC.md."},
	{ID: "AP9", Description: "js-defs-as-impl — Wrote a JS def file (K/defs/<id>.js) with non-throw function bodies (e.g. `function Foo(x){ return 0; }`) and used that as 'implementation' evidence. JS defs MUST be throw-stubs; the real impl lives in K/frags/<id>.js (see Single-file deliverable model). Static check `defs-must-throw` fires on any non-throw body."},
	{ID: "AP10", Description: "frag-trivial-stub — Wrote a fragment file (K/frags/<id>.js) whose function body is a one-line literal return (`return 0;` / `return [];` / `return {};`) just to make typecalc_compile pass. Fragments must contain real logic — at least one control-flow statement (if/for/while) or non-literal computation. Static check `frags-non-trivial` fires on these stubs."},
	{ID: "AP11", Description: "unmodeled-function-in-fragment — Wrote a top-level `function Foo(...)` in K/frags/<id>.js where Foo is NOT a graph object (and not the parent object's ImplSymbol). Such functions ship to the deliverable via session_build but bypass the verification chain entirely — they never reach confirm_object. session_build refuses to assemble when ANY fragment contains an unmodeled function name. Fix: either (a) model the helper as its own graph object, or (b) inline it as `const foo = (...) => ...` / closure inside the modeled function so it's not a top-level declaration."},
	{ID: "AP12", Description: "def-multi-entity-file — Wrote a JS def file (K/defs/<id>.js) containing functions for multiple objects, or mixing attribute @typedef with object function declarations. Each def file must declare exactly the entity that matches its filename — the function name must equal the object id OR its declared ImplSymbol. Static check `defs-entity-1to1` fires on extras."},
	{ID: "AP13", Description: "def-frag-name-mismatch — The set of top-level functions declared in K/defs/<id>.js doesn't match the set in K/frags/<id>.js (missing from frag, or extra in frag). Static check `frags-content-matches-def` enforces the 1:1 mapping so the def's @param / @returns / @example ground truth covers every function the fragment ships."},
}

// Describe renders the protocol as a markdown document suitable for
// the LLM system prompt or human reading. Pre-v9.0 this content lived
// in CLAUDE.md / system.md as prose; v9.0 generates the same text from
// the structured tables above. Single source of truth — edit the
// constants/tables, not the rendered output.
func Describe() string {
	var b strings.Builder
	b.WriteString("# kcpos runtime protocol (v9.0)\n\n")
	b.WriteString("This protocol is generated from internal/protocol/protocol.go. ")
	b.WriteString("The agent must follow these rules; the gate (session_gate_check) ")
	b.WriteString("enforces a structural subset programmatically.\n\n")

	b.WriteString("## Product layers (only L4 is a deliverable)\n\n")
	for _, l := range Layers {
		marker := "  "
		if l.IsDeliverable {
			marker = "★ "
		}
		fmt.Fprintf(&b, "%s**%s** — %s\n", marker, l.Name, l.Content)
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

	b.WriteString("## Root finish flow (§5.5)\n\n")
	for _, r := range RootFinishFlow {
		fmt.Fprintf(&b, "- **%s %s**: %s\n", r.ID, r.Name, r.Doing)
	}
	b.WriteString("\n")

	b.WriteString("## Waiver discipline\n\n")
	fmt.Fprintf(&b, "- obstacle+waiver pair counts as evidence-equivalent at the gate (substitutes for kind=test ok=true)\n")
	fmt.Fprintf(&b, "- gate flags [waiver-flood] when ≥%d/%d (%d%%) of confirmed PRAGMATIC-waivered objects — at totalConfirmed ≥ %d\n", WaiverFloodThresholdNum, WaiverFloodThresholdDen, WaiverFloodThresholdNum*100/WaiverFloodThresholdDen, WaiverFloodMin)
	fmt.Fprintf(&b, "- waiver kinds (v9.0.1): `structural` (DOM/Canvas/side-effect/lang-without-runner — does NOT count toward flood) vs `pragmatic` (default; counts toward flood)\n")
	fmt.Fprintf(&b, "- reason-diversity probe rejects 3+ pragmatic waivers sharing the same first-60-char normalized reason\n")
	fmt.Fprintf(&b, "- %d failed reviews on the same object force agent to escalate to obstacle\n\n", CycleCap)

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
	b.WriteString("2. **`frags-content-matches-def`** (static check, per-object) — the set of top-level functions in `K/frags/<id>.js` must equal the set declared in `K/defs/<id>.js`. Missing or extra functions are flagged.\n")
	b.WriteString("3. **`defs-must-throw`** + **`frags-non-trivial`** (static checks) — together stop stub bodies from posing as evidence.\n")
	b.WriteString("4. **session_build refusal** (HARD GATE) — before assembling the deliverable, session_build scans every fragment for top-level `function <Name>` declarations and intersects with the graph object/implSymbol set. ANY function declared in a fragment but missing from the graph causes session_build to refuse: no deliverable produced, agent must either model the function as a graph object (and put it through confirm_object) or remove it. Top-level declarations are the only shape rejected — arrow functions, closures, methods on object literals, and IIFEs are all fine since they're scoped.\n\n")
	b.WriteString("**Why this matters**: \"shipped without confirm_object\" = \"the user gets code that kcpos never claimed was correct\". The whole point of the verification chain is the guarantee that the deliverable is bounded by what was verified. v9.0.5 had this guarantee in principle (defs-must-throw + frags-non-trivial) but allowed a bypass (write helpers in def, ship them via fragment). v9.0.6 makes the guarantee hold by construction.\n\n")

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
	b.WriteString("function Foo(input) { throw new Error(\"Foo: contract-only; implement in K/frags/Foo.js\"); }\n")
	b.WriteString("```\n\n")
	b.WriteString("Two static rules enforce this:\n\n")
	b.WriteString("- **`defs-must-throw`**: every function in `K/defs/<id>.js` must have a body whose first statement is `throw new Error(...)`. Anything else (`return 0`, real logic, an empty body) is flagged. This is what stops agents from writing a stub like `function Foo(){return 0;}` and using the def as fake impl evidence — calling that stub would always 'succeed' which lets bogus tests pass.\n")
	b.WriteString("- **`frags-non-trivial`**: every function in `K/frags/<id>.js` must have a body with at least one control-flow statement or a non-literal return expression. Single-line `return 0;` / `return [];` / `return {};` / empty body / pass-through `return x;` are rejected. Combined with `defs-must-throw`, an agent can no longer bypass the verification chain with stub bodies — neither file accepts them.\n\n")
	b.WriteString("**`@example` blocks** are not decoration: `typecalc_synthesize_tests` extracts them as input/output ground truth and feeds them to the LLM verbatim. The synthesized test suite is required to cover every `@example` in addition to its own boundary cases. Treat `@example` as the contract you're signing — write at least one happy path and one edge case per function.\n\n")

	b.WriteString("## Chapter-granular markdown access (v9.0.4)\n\n")
	b.WriteString("**Any** markdown document the agent reads — SPEC.md, DESIGN.md, third-party docs, etc. — that exceeds ~5K tokens MUST be accessed chapter-by-chapter, never bulk-read into one context:\n\n")
	b.WriteString("1. `markdown_outline <path>` returns the chapter list (id, title, line range, ~tokens) — typically 1-2K tokens regardless of source size. The parent agent uses this to plan path B: which chapters cover which graph objects.\n")
	b.WriteString("2. `markdown_section <path> <section_id>` returns one chapter's body. Child agents in path B only fetch the chapter(s) covering their assigned object — fresh context per child, bounded regardless of total SPEC size.\n")
	b.WriteString("3. `markdown_validate <path>` checks that the doc is well-structured (numeric ids, unique titles, no chapter > 5K tokens). Run before path-B decomposition to catch structurally unfit docs early — if it fails, the user must restructure before kcpos can usefully decompose.\n")
	b.WriteString("4. `read_file` auto-falls-back to the outline when the target is markdown above the threshold. Setting `force=true` bypasses this and dumps the whole file — see AP8.\n\n")
	b.WriteString("**Why this matters**: a 1500-line SPEC is ~28K tokens; tripled, ~84K tokens. Bulk-reading at session start consumes most of the LLM context window before any reasoning begins, and every child agent that does the same multiplies the cost. Chapter-granular access keeps both parent (~outline only) and each child (~one chapter) bounded at a small fraction of total spec size.\n\n")

	b.WriteString("## Single-file deliverable model (v9.0.3 — HTML/Canvas projects)\n\n")
	b.WriteString("Projects whose deliverable is `index.html` (or any single artifact that aggregates code from many objects) MUST use the **fragment-write + parent-concat** pattern, not the \"one agent writes the whole file\" pattern that the v9.0.2 Terraria batch tried and 5/5 instances died on:\n\n")
	b.WriteString("1. Each graph object declares **two** path fields:\n")
	b.WriteString("   - `impl: \"index.html\"` — the eventual deliverable path (shared across all objects of the same single-file project)\n")
	b.WriteString("   - `implFragment: \"K/frags/<ObjectId>.js\"` — this object's own write-target (one file per object, isolated)\n")
	b.WriteString("2. Each child session (path B) writes ONLY to its `implFragment` path. The fragment contains the function body for this one object plus any module-local helpers it needs.\n")
	b.WriteString("3. Child returns a one-line summary; parent never reads the fragment bytes.\n")
	b.WriteString("4. **R2 build step** in the root finish flow runs `kcpos build` (or equivalent) which concatenates every `implFragment` in topological order into a single `<script>` block injected into the index.html template, producing the final deliverable.\n")
	b.WriteString("5. The R4 gate verifies the assembled `index.html` exists and is non-empty.\n\n")
	b.WriteString("**Why this matters**: pre-v9.0.3 the dual-source-prevention hook (graph_merge_object refusing impl=*.js when index.html exists) forced every object to share `impl=index.html`. That made the parent agent the only entity with write access to the deliverable, defeating path B and overflowing the LLM stream deadline. v9.0.3 keeps the no-shadow-.js guarantee (the deliverable is still single-source-of-truth) but reintroduces per-object writing surfaces via the `K/frags/` staging area.\n\n")

	return b.String()
}
