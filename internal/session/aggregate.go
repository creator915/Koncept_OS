package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/creator915/Koncept_OS/internal/graph"
)

// Aggregate walks a session and all its descendants, collecting their
// per-session "outputs" into deduplicated lists on the named session.
//
// Source of truth, in priority order:
//
//  1. **The session's graphDiff**: ground truth for what objects were
//     created/modified during the session. From this we derive
//     `newSignatures` (added object ids), `newAttributes` (added
//     attribute ids), and `implementations` (impl paths set or changed).
//  2. **`.kcpos/typecalc-evidence/<id>.json`**: ground truth for which
//     objects passed typecalc compile/test. We list each existing
//     evidence file as a `tests` entry — a real on-disk artifact, not a
//     synthetic string.
//  3. **The session's output.* fields**: kept as a backward-compat
//     fallback. If anything was previously written there (e.g. by an
//     older kcpos version, or by manual edit), it gets merged in.
//
// Before this change, the only source was (3), which meant aggregate
// was effectively useless: nothing populated those fields except the
// agent itself running `edit` against session JSON. The new design
// derives outputs from authoritative state automatically — the agent
// no longer needs to hack-patch session files for the gate to pass.
//
// graphDiff itself is NOT aggregated — each session keeps its own diff
// for rollback purposes.
func Aggregate(dir, id string) (*Session, error) {
	s, err := Load(dir, id)
	if err != nil {
		return nil, err
	}
	cwd, _ := os.Getwd()

	impls := []string{}
	sigs := []string{}
	attrs := []string{}
	tests := []string{}

	var visit func(sid string) error
	visit = func(sid string) error {
		cur, err := Load(dir, sid)
		if err != nil {
			return err
		}
		// (3) backward-compat fallback — preserve anything explicitly set.
		impls = appendUnique(impls, cur.Output.Implementations)
		sigs = appendUnique(sigs, cur.Output.NewSignatures)
		attrs = appendUnique(attrs, cur.Output.NewAttributes)
		tests = appendUnique(tests, cur.Output.Tests)

		// (1) + (2) derive from graphDiff and evidence files.
		di, ds, da, dt := deriveOutputsFromState(cur, cwd)
		impls = appendUnique(impls, di)
		sigs = appendUnique(sigs, ds)
		attrs = appendUnique(attrs, da)
		tests = appendUnique(tests, dt)

		for _, child := range cur.Children {
			if !Exists(dir, child) {
				continue
			}
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(id); err != nil {
		return nil, err
	}
	s.Output.Implementations = impls
	s.Output.NewSignatures = sigs
	s.Output.NewAttributes = attrs
	s.Output.Tests = tests
	s.UpdatedAt = time.Now().UTC()
	if err := Save(dir, s); err != nil {
		return nil, err
	}
	return s, nil
}

// deriveOutputsFromState reads ground truth out of the session's
// graphDiff and the project's typecalc-evidence directory.
//
//   - implementations: every distinct `impl` path mentioned in
//     graphDiff.added.objects[*].impl AND
//     graphDiff.modified.objects[*].after.impl. (We don't gate on
//     before/after differing — if a session set or changed an impl,
//     that path is part of its delivered work.)
//   - newSignatures:   ids in graphDiff.added.objects.
//   - newAttributes:   ids in graphDiff.added.attributes.
//   - tests:           for every object id touched by this session
//     (added or modified), if .kcpos/typecalc-evidence/<id>.json
//     exists, add the relative evidence path. Real on-disk artifact.
func deriveOutputsFromState(s *Session, cwd string) (impls, sigs, attrs, tests []string) {
	// newSignatures, newAttributes — straight from added maps.
	for id := range s.Output.GraphDiff.Added.Objects {
		sigs = append(sigs, id)
	}
	for id := range s.Output.GraphDiff.Added.Attributes {
		attrs = append(attrs, id)
	}

	// implementations — from added objects' impl + modified objects' after.impl.
	implSet := map[string]bool{}
	for _, raw := range s.Output.GraphDiff.Added.Objects {
		if p := extractImpl(raw); p != "" {
			implSet[p] = true
		}
	}
	for _, mod := range s.Output.GraphDiff.Modified.Objects {
		if p := extractImpl(mod.After); p != "" {
			implSet[p] = true
		}
	}
	for p := range implSet {
		impls = append(impls, p)
	}

	// tests — every object id this session created or modified whose
	// evidence file records kind="test". Compile-only evidence does NOT
	// count as a test (Fix 2): output.tests semantically means "tests
	// were run", not "any compile attempts". Languages with no test
	// runner (Rust / Java / pure HTML) will produce empty tests for
	// confirmed objects, which is the truth — and the gate's
	// outputs-tests-non-empty rule is now scoped to only fire when a
	// testable-language object exists (see internal/session/gate.go).
	idSet := map[string]bool{}
	for id := range s.Output.GraphDiff.Added.Objects {
		idSet[id] = true
	}
	for id := range s.Output.GraphDiff.Modified.Objects {
		idSet[id] = true
	}
	for id := range idSet {
		rel := filepath.Join(".kcpos", "typecalc-evidence", id+".json")
		path := rel
		if !filepath.IsAbs(path) && cwd != "" {
			path = filepath.Join(cwd, rel)
		}
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) == 0 {
			continue
		}
		var ev struct {
			Kind string `json:"kind"`
			OK   bool   `json:"ok"`
		}
		if err := json.Unmarshal(raw, &ev); err != nil {
			continue
		}
		if ev.Kind == "test" && ev.OK {
			tests = append(tests, rel)
		}
	}

	// Stable order — Aggregate's appendUnique preserves order, so callers
	// see deterministic output regardless of map-iteration randomness.
	sort.Strings(sigs)
	sort.Strings(attrs)
	sort.Strings(impls)
	sort.Strings(tests)
	return impls, sigs, attrs, tests
}

// extractImpl pulls the `impl` field out of an object's JSON payload.
// Returns empty string if the field is missing, null, or not a string.
func extractImpl(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var obj graph.Object
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	if obj.Impl == nil {
		return ""
	}
	return *obj.Impl
}

func appendUnique(dst, src []string) []string {
	seen := map[string]bool{}
	for _, x := range dst {
		seen[x] = true
	}
	for _, x := range src {
		if seen[x] {
			continue
		}
		dst = append(dst, x)
		seen[x] = true
	}
	return dst
}
