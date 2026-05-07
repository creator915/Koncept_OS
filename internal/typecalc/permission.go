package typecalc

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Capability is a granular permission token attached to a TypedValue via
// the Permitted constructor (§2.6, §6). The string form is canonical:
//
//	read_file:K/defs/*
//	write_file:src/UpdatePhysics.impl.ts
//	run_tool:checker
//	spawn_agent:implementer
//
// Globs use filepath.Match semantics. The role suffix on spawn_agent is
// free-form; it's typically used to label a configuration preset.
type Capability string

// Verb returns the part before the first colon ("read_file", "run_tool",
// "spawn_agent", ...).
func (c Capability) Verb() string {
	if i := strings.Index(string(c), ":"); i >= 0 {
		return string(c)[:i]
	}
	return string(c)
}

// Arg returns the part after the first colon, or "" if there is none.
func (c Capability) Arg() string {
	if i := strings.Index(string(c), ":"); i >= 0 {
		return string(c)[i+1:]
	}
	return ""
}

// Matches reports whether this capability authorizes the requested action.
// The verb must match exactly; the arg may be a glob (filepath.Match).
func (c Capability) Matches(verb, arg string) bool {
	if c.Verb() != verb {
		return false
	}
	pat := c.Arg()
	if pat == "" || pat == "*" {
		return true
	}
	// Try filepath.Match first, fall back to literal equality.
	ok, err := filepath.Match(pat, arg)
	if err == nil && ok {
		return true
	}
	return pat == arg
}

// CapSet is a set of capabilities. Order is irrelevant; we keep it as a
// slice for stable JSON serialization.
type CapSet []Capability

// Authorize returns nil if any capability in the set matches; otherwise
// returns a *TypedValue of Kind KindPermissionDenied that the caller can
// route through the rule system.
func (s CapSet) Authorize(verb, arg string) *TypedValue {
	for _, c := range s {
		if c.Matches(verb, arg) {
			return nil
		}
	}
	d := struct {
		Verb string   `json:"verb"`
		Arg  string   `json:"arg"`
		Caps []string `json:"availableCaps"`
	}{Verb: verb, Arg: arg, Caps: stringify(s)}
	raw, _ := json.Marshal(d)
	return &TypedValue{Kind: KindPermissionDenied, Payload: string(raw)}
}

// Subset reports whether s is a subset of parent (per §6.1: child caps ⊆
// parent caps).
//
// "Subset" is glob-aware, not byte-equal: a child cap is covered by a
// parent cap when every path / arg the child authorizes is also
// authorized by the parent. This is the natural meaning of
// "child ⊆ parent" when caps use glob patterns; pure string equality
// would force every preset to repeat the parent's exact tokens, which
// defeats the whole point of named profiles like CapsImplementer
// narrowing CapsRoot.
//
// Examples (verbs omitted for brevity):
//
//	parent "**"      covers child "K/defs/*"
//	parent "K/defs/*" covers child "K/defs/Foo.ts"
//	parent "src/*"   does NOT cover child "test/*"
func (s CapSet) Subset(parent CapSet) bool {
	for _, c := range s {
		if !capCovered(c, parent) {
			return false
		}
	}
	return true
}

// SubsetCheck returns nil if s ⊆ parent, else an error listing the caps
// the child requested but the parent does not cover. Used in spawn_subagent.
func (s CapSet) SubsetCheck(parent CapSet) error {
	var extra []string
	for _, c := range s {
		if !capCovered(c, parent) {
			extra = append(extra, string(c))
		}
	}
	if len(extra) > 0 {
		return fmt.Errorf("child capabilities exceed parent: %v", extra)
	}
	return nil
}

// capCovered reports whether some capability in parent covers child. A
// capability covers another when the verb matches and the parent's
// glob pattern admits every path the child's pattern admits. We can't
// solve the general "is glob A subset of glob B" problem, so we pattern-
// match a few common shapes: total wildcards, exact equality, and
// "parent ends with /* or /**, child shares the parent's prefix".
func capCovered(child Capability, parent CapSet) bool {
	cv := child.Verb()
	cp := child.Arg()
	for _, p := range parent {
		if p.Verb() != cv {
			continue
		}
		pp := p.Arg()
		if pp == "" || pp == "*" || pp == "**" {
			return true
		}
		if pp == cp {
			return true
		}
		// Parent is "prefix/*" or "prefix/**" — accept any child arg under
		// the same prefix.
		for _, suffix := range []string{"/**/*", "/**", "/*"} {
			if !strings.HasSuffix(pp, suffix) {
				continue
			}
			base := strings.TrimSuffix(pp, suffix)
			if base == "" {
				return true
			}
			if cp == base ||
				strings.HasPrefix(cp, base+"/") ||
				strings.HasPrefix(cp, base+"*") {
				return true
			}
		}
		// Parent's arg is itself a glob; treat the child's literal arg as
		// a path and see if parent matches it. This handles parent="*.ts"
		// child="K/defs/Foo.ts" cases too (filepath.Match returns true).
		if ok, _ := filepath.Match(pp, cp); ok {
			return true
		}
	}
	return false
}

func stringify(s CapSet) []string {
	out := make([]string, len(s))
	for i, c := range s {
		out[i] = string(c)
	}
	return out
}

// Preset capability profiles used by §6.3.
var (
	// CapsImplementer is the capability set typical for an implementation
	// child agent (writes one impl file, reads defs/src, can run checker
	// + compiler).
	CapsImplementer = CapSet{
		"read_file:K/defs/*",
		"read_file:K/defs/**/*",
		"read_file:src/*",
		"read_file:src/**/*",
		"read_file:K/graph.json",
		"write_file:src/*",
		"write_file:src/**/*",
		"run_tool:graph_show",
		"run_tool:graph_validate",
		"run_tool:graph_merge_object",
		"run_tool:graph_merge_attribute",
		"run_tool:typecalc_compile",
		"run_tool:typecalc_test",
	}

	// CapsTester is the capability set for a test-writing child agent —
	// can read the impl (purely to know the surface, not to copy logic)
	// and the def, but can only write *.test.* files.
	CapsTester = CapSet{
		"read_file:K/defs/*",
		"read_file:K/defs/**/*",
		"read_file:src/*.impl.ts",
		"read_file:src/*.impl.go",
		"read_file:src/*.impl.js",
		"read_file:K/graph.json",
		"write_file:src/*.test.ts",
		"write_file:src/*.test.go",
		"write_file:src/*.test.js",
		"run_tool:typecalc_test",
	}

	// CapsIntegrator is the read-everything-no-write profile for the
	// integration verifier. Can run all check/build/test tools but cannot
	// modify any source.
	CapsIntegrator = CapSet{
		"read_file:**",
		"read_file:**/*",
		"run_tool:graph_validate",
		"run_tool:graph_render",
		"run_tool:typecalc_compile",
		"run_tool:typecalc_test",
		"run_tool:typecalc_probe",
	}

	// CapsRoot is the unrestricted root-session profile.
	CapsRoot = CapSet{
		"read_file:*",
		"read_file:**",
		"read_file:**/*",
		"write_file:*",
		"write_file:**",
		"write_file:**/*",
		"run_tool:*",
		"spawn_agent:*",
	}
)

// PresetByName looks up one of the named profiles. Used by spawn_subagent
// to translate a role name ("implementer", "tester", ...) into a CapSet.
func PresetByName(name string) (CapSet, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "implementer":
		return CapsImplementer, true
	case "tester":
		return CapsTester, true
	case "integrator":
		return CapsIntegrator, true
	case "root":
		return CapsRoot, true
	}
	return nil, false
}
