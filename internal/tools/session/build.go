package sessiontools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
)

// sessionBuildTool exposes R2 build for single-file deliverables. It
// concatenates every confirmed (or implementing) object's
// `implFragment` into a single `<script>` block injected into the
// shared `impl` deliverable (typically index.html).
//
// The v9.0.3 single-file-deliverable model:
//   - graph object declares impl="index.html" (shared) + implFragment="K/frags/<id>.js" (per-object)
//   - child sessions write to implFragment only
//   - parent's R2 step calls session_build which assembles the fragments
//     into the deliverable in topological order
//   - the assembled deliverable is what the user opens in a browser
//
// Pre-v9.0.3 the agent had to write the whole index.html in one
// write_file call from the parent context — which 5/5 instances of
// the 2026-05-11 Terraria batch died trying.
func sessionBuildTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name:        "session_build",
				Description: "Assemble per-object impl fragments (graph object.implFragment) into the shared deliverable (graph object.impl) for single-file projects.\n\n**Mode (v9.3.1):**\n- `reference` (DEFAULT) — emit `<script src=\"K/frags/<id>.js\"></script>` references in topological order. The deliverable is index.html + K/frags/<id>.js files; ship as a folder. Cheap to re-run (small write), so the chain calls this before each runtime_smoke in the HTML branch — fragment changes become visible to the browser without rewriting the whole HTML.\n- `inline` — concatenate every fragment's body into a single inline `<script>` block. Old v9.0.3 behavior. Use only when the deliverable contract really requires one self-contained file. Rewrites the whole deliverable on every call, so expensive to invoke mid-chain.\n\nFragments order is topological (producer before consumer). Unmodeled top-level `function Foo(...)` declarations in any fragment cause refusal (AP11) — applies in both modes since the browser executes them regardless.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"mode": map[string]interface{}{
							"type":        "string",
							"description": "Assembly mode. `reference` (default) emits `<script src>` references; `inline` concatenates fragment bodies. See description for tradeoffs.",
							"enum":        []string{"reference", "inline"},
						},
					},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			mode := "reference"
			if v, ok := args["mode"].(string); ok && v != "" {
				mode = v
			}
			return runSessionBuild(mode)
		},
	}
}

// runSessionBuild is the testable core of session_build.
// mode is "reference" or "inline" (see sessionBuildTool description).
func runSessionBuild(mode string) (string, error) {
	if mode == "" {
		mode = "reference"
	}
	if mode != "reference" && mode != "inline" {
		return "", fmt.Errorf("session_build: unknown mode %q (allowed: reference, inline)", mode)
	}
	g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
	if err != nil {
		return "", fmt.Errorf("load graph: %w", err)
	}

	// Collect (id, impl, fragment) for every object that has both
	// impl and implFragment set. Objects without implFragment are
	// ignored (multi-file projects don't need this step).
	var entries []entry
	deliverables := map[string]struct{}{}
	for id, obj := range g.Objects {
		if obj.Impl == nil || *obj.Impl == "" {
			continue
		}
		if obj.ImplFragment == nil || *obj.ImplFragment == "" {
			continue
		}
		entries = append(entries, entry{id: id, implPath: *obj.Impl, fragmentPath: *obj.ImplFragment})
		deliverables[*obj.Impl] = struct{}{}
	}
	if len(entries) == 0 {
		return "session_build: no objects with implFragment set; nothing to assemble (this is correct for multi-file projects)", nil
	}
	if len(deliverables) > 1 {
		var paths []string
		for p := range deliverables {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		return "", fmt.Errorf("session_build: fragments target multiple deliverables %v — every fragment-using object must share the same impl path; fix the graph and re-run", paths)
	}

	// Topological sort: producer before consumer. Build adjacency:
	// dep[B] includes A when A produces an attribute B consumes.
	idsByObj := map[string]bool{}
	for _, e := range entries {
		idsByObj[e.id] = true
	}
	producers := map[string]string{} // attrID → objID that produces (or mutates) it
	for id, obj := range g.Objects {
		if !idsByObj[id] {
			continue
		}
		for _, a := range obj.Produces {
			producers[a] = id
		}
		for _, a := range obj.Mutates {
			if _, seen := producers[a]; !seen {
				producers[a] = id
			}
		}
	}
	deps := map[string]map[string]bool{}
	for _, e := range entries {
		deps[e.id] = map[string]bool{}
		obj := g.Objects[e.id]
		for _, a := range obj.Consumes {
			if p, ok := producers[a]; ok && p != e.id && idsByObj[p] {
				deps[e.id][p] = true
			}
		}
	}
	ordered, cycleErr := topoSort(entries, deps)
	if cycleErr != nil {
		return "", fmt.Errorf("session_build: %w", cycleErr)
	}

	// Read each fragment; track missing AND scan for unmodeled functions.
	// v9.0.6 §4: the final gate. Every function declaration we're about
	// to concatenate into the deliverable MUST correspond to a graph
	// object — its ID, its declared ImplSymbol, or a function explicitly
	// permitted as a helper of THIS object (matched against the same
	// object's def file's function set). Anything else means the agent
	// wrote code that bypassed the verification chain; refuse to ship.
	//
	// v9.3.1: AP11 still applies in reference mode — the browser executes
	// the fragments via `<script src>` so any unmodeled function still
	// reaches global scope.
	allowedNames := buildAllowedFunctionNames(g)
	var fragmentBodies []string
	var fragmentPaths []string
	var missing []string
	var unmodeled []string
	for _, e := range ordered {
		body, err := os.ReadFile(e.fragmentPath)
		if err != nil {
			missing = append(missing, fmt.Sprintf("%s (%s): %v", e.id, e.fragmentPath, err))
			continue
		}
		for _, fname := range scanFragmentFunctionNames(string(body)) {
			if allowedNames[fname] {
				continue
			}
			unmodeled = append(unmodeled, fmt.Sprintf("%s in %s", fname, e.fragmentPath))
		}
		fragmentBodies = append(fragmentBodies, fmt.Sprintf("// === %s (fragment: %s) ===\n%s", e.id, e.fragmentPath, string(body)))
		fragmentPaths = append(fragmentPaths, e.fragmentPath)
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("session_build: %d fragment file(s) missing or unreadable:\n  %s\nWrite the fragments before assembling (each child session writes its own fragment)",
			len(missing), strings.Join(missing, "\n  "))
	}
	if len(unmodeled) > 0 {
		return "", fmt.Errorf("session_build: refusing to assemble — %d function(s) declared in fragments are NOT modeled as graph objects (or as another object's implSymbol):\n  %s\n\nEvery function shipped to the deliverable must pass through confirm_object on a graph object. Either: (a) model these as separate graph objects (graph_create_object + def + implFragment), then run their confirm_object chains, or (b) inline them as local consts / closures inside an existing object's fragment so they're not top-level function declarations.",
			len(unmodeled), strings.Join(unmodeled, "\n  "))
	}

	deliverable := ordered[0].implPath
	var scriptBody string
	switch mode {
	case "reference":
		// v9.3.1 default. Emit `<script src>` per fragment in topo order.
		// Browser fetches each file (file:// scheme works for non-module
		// scripts with relative paths). Cheap to re-run since we only
		// rewrite a small block, not concatenate megabytes of fragment
		// bodies. This is what makes incremental build viable — the
		// chain can call session_build before every runtime_smoke
		// without breaking the bank.
		var refs []string
		for _, p := range fragmentPaths {
			refs = append(refs, fmt.Sprintf("<script src=%q></script>", p))
		}
		scriptBody = strings.Join(refs, "\n")
	case "inline":
		// Legacy v9.0.3 behavior. Concatenate all fragment bodies into a
		// single inline <script> block. Used when the deliverable contract
		// requires one self-contained file.
		concat := strings.Join(fragmentBodies, "\n\n")
		scriptBody = fmt.Sprintf("<script>\n%s\n</script>", concat)
	}
	assembled, err := injectKcposBlock(deliverable, scriptBody)
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(deliverable, assembled); err != nil {
		return "", err
	}
	return fmt.Sprintf("session_build: assembled %d fragment(s) into %s (%d bytes, mode=%s). Fragments in topo order: %s",
		len(ordered), deliverable, len(assembled), mode, joinIDs(ordered)), nil
}

func joinIDs(entries []entry) string {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.id
	}
	return strings.Join(ids, " → ")
}

// entry is the unit of work for session_build, kept package-local.
type entry struct {
	id           string
	implPath     string
	fragmentPath string
}

// topoSort orders entries by producer-before-consumer dependencies.
// Returns an error when a cycle is detected, naming the offending
// nodes so the agent can fix the graph.
func topoSort(entries []entry, deps map[string]map[string]bool) ([]entry, error) {
	indeg := map[string]int{}
	byID := map[string]entry{}
	for _, e := range entries {
		indeg[e.id] = 0
		byID[e.id] = e
	}
	for _, d := range deps {
		for to := range d {
			_ = to
		}
	}
	// indeg[v] = number of edges pointing TO v from deps map. Our
	// adjacency has deps[v] = {u : u must come before v}, so v has
	// |deps[v]| incoming edges from its predecessors.
	for v, predSet := range deps {
		indeg[v] = len(predSet)
	}
	// Stable order: enqueue in entries order when indeg=0.
	var queue []string
	for _, e := range entries {
		if indeg[e.id] == 0 {
			queue = append(queue, e.id)
		}
	}
	var out []entry
	for len(queue) > 0 {
		// Pop deterministically — sort the current frontier alphabetically
		// to keep build output reproducible across runs.
		sort.Strings(queue)
		v := queue[0]
		queue = queue[1:]
		out = append(out, byID[v])
		// for every w that depends on v, decrement indeg
		for w, predSet := range deps {
			if predSet[v] {
				delete(predSet, v)
				indeg[w]--
				if indeg[w] == 0 {
					queue = append(queue, w)
				}
			}
		}
	}
	if len(out) != len(entries) {
		var remaining []string
		for _, e := range entries {
			found := false
			for _, o := range out {
				if o.id == e.id {
					found = true
					break
				}
			}
			if !found {
				remaining = append(remaining, e.id)
			}
		}
		sort.Strings(remaining)
		return nil, fmt.Errorf("cycle detected among fragments: %s (consumer/producer cycle prevents a valid build order)", strings.Join(remaining, ", "))
	}
	return out, nil
}

// buildAllowedFunctionNames returns the set of function names that
// are permitted to appear in fragments shipping into the deliverable.
// v9.0.6.4: a function is permitted iff it corresponds to a graph
// object — either by id, by its ImplSymbol, or both. Helpers, utility
// functions, or any other top-level `function Foo(...)` declaration
// must be modeled as its own graph object before it can ship.
func buildAllowedFunctionNames(g *graph.Graph) map[string]bool {
	allowed := map[string]bool{}
	for id, obj := range g.Objects {
		allowed[id] = true
		if obj.ImplSymbol != "" {
			allowed[obj.ImplSymbol] = true
		}
	}
	return allowed
}

// fragFnDeclRe matches a `function Name(` token. We use the regex to
// find candidates, then check brace-nesting separately so nested
// function declarations (inside another function's body) don't trip
// the unmodeled-function gate — those are scoped to their enclosing
// function and never escape to the deliverable's global namespace.
var fragFnDeclRe = regexp.MustCompile(`function\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)

// scanFragmentFunctionNames returns the names of TOP-LEVEL function
// declarations in src — only those at brace-depth 0. Nested function
// declarations (inside another function's `{...}` body) are skipped
// since the JS scoping rules confine them to their enclosing function.
//
// Doesn't try to skip strings/comments: pathological cases where a
// comment contains `function Name(...)` would false-positive, but
// fragments written by agents almost never do that, and the failure
// mode is "agent rejects a legitimate build" — easy to diagnose.
func scanFragmentFunctionNames(src string) []string {
	seen := map[string]bool{}
	var out []string
	depth := 0
	matches := fragFnDeclRe.FindAllStringSubmatchIndex(src, -1)
	for _, m := range matches {
		// Update brace depth from previous match end to this match start.
		prev := 0
		if len(out) > 0 {
			// not used; we compute fresh from src[0:m[0]] each iteration below.
		}
		depth = braceDepthAt(src, m[0], prev)
		if depth != 0 {
			continue
		}
		name := src[m[2]:m[3]]
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// braceDepthAt computes the brace nesting depth at byte position `at`
// in src by counting `{` and `}` characters from `start` (or 0) to
// `at`. Simple counter — does not distinguish braces inside strings
// or comments, which is good enough for our purposes (fragments
// written by code-generating agents follow tidy formatting).
func braceDepthAt(src string, at, start int) int {
	depth := 0
	for i := start; i < at && i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	return depth
}

// kcposBlockOpen / kcposBlockClose bracket the assembled script so
// re-running session_build can locate and replace the prior block
// without disturbing the user-written HTML scaffolding around it.
const (
	kcposBlockOpen  = "<!-- kcpos:session_build:begin (generated; do not edit by hand) -->"
	kcposBlockClose = "<!-- kcpos:session_build:end -->"
)

// injectKcposBlock inserts (or replaces) the assembled script block
// inside the deliverable. v9.3.1: `scriptBody` is now the complete
// `<script>...</script>` markup (callers decide whether it's one inline
// block or N `<script src>` references) — injectKcposBlock just wraps
// it in the kcpos block markers and stitches it into the deliverable.
//
// If the deliverable doesn't exist, a minimal HTML scaffold is created.
// If a prior block exists, it's replaced; otherwise the block is
// inserted just before </body> (or appended when no </body> tag is
// present).
func injectKcposBlock(deliverable, scriptBody string) (string, error) {
	block := fmt.Sprintf("%s\n%s\n%s", kcposBlockOpen, scriptBody, kcposBlockClose)

	raw, err := os.ReadFile(deliverable)
	if os.IsNotExist(err) {
		// Minimal scaffold; agent may overwrite the surrounding HTML
		// later, the kcpos block markers will let us still find our
		// insertion point.
		canvasSize := "<canvas id=\"game\" width=\"800\" height=\"600\"></canvas>"
		return fmt.Sprintf("<!doctype html>\n<html>\n<head>\n  <meta charset=\"utf-8\">\n  <title>kcpos build</title>\n</head>\n<body>\n  %s\n  %s\n</body>\n</html>\n", canvasSize, block), nil
	}
	if err != nil {
		return "", fmt.Errorf("read deliverable %q: %w", deliverable, err)
	}
	content := string(raw)

	openIdx := strings.Index(content, kcposBlockOpen)
	closeIdx := strings.Index(content, kcposBlockClose)
	if openIdx >= 0 && closeIdx > openIdx {
		// Replace existing block (including markers).
		head := content[:openIdx]
		tail := content[closeIdx+len(kcposBlockClose):]
		return head + block + tail, nil
	}

	// No prior block — insert before </body>, else append.
	if i := strings.LastIndex(content, "</body>"); i >= 0 {
		return content[:i] + "  " + block + "\n" + content[i:], nil
	}
	return content + "\n" + block + "\n", nil
}

// writeFileAtomic writes via a sibling tmp + rename so a crash
// mid-write doesn't leave a half-built deliverable on disk.
func writeFileAtomic(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %q: %w", filepath.Dir(path), err)
	}
	tmp := path + ".kcpos-build.tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write tmp %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %q: %w", path, err)
	}
	return nil
}
