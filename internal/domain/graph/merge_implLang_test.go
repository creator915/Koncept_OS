package graph

import (
	"strings"
	"testing"
)

// 2026-05-21 — implLang whitelist drift fix.
//
// Pre-2026-05-21 the graph layer hardcoded a 9-lang whitelist that had
// drifted from the typecalc/lang/ invoker set:
//   - allowed Java/Haskell (no invoker exists)
//   - refused C (runCCompile + runCTest fully implemented)
//
// PB-30 batch #4 saw the `entr` agent burn ~1h pivoting C→Go→TS→JS
// because the graph layer rejected `implLang="c"` while the underlying
// runtime had the gcc invoker ready. The whitelist was removed; typecalc
// is now the single source of truth for lang support, and the chain's
// compile/test step returns Insufficient (with a stage-named reason)
// when an unsupported lang is declared — same response as before, but
// the error now reaches the agent at the moment that information is
// useful (compile time, not graph-write time).

func TestMergeObject_AcceptsCImplLang(t *testing.T) {
	g := NewGraph()
	g.Objects["F"] = NewObject("defs/F.h", "")
	if err := g.MergeObject("F", map[string]any{"implLang": "C"}); err != nil {
		t.Fatalf("implLang=C must be accepted (typecalc/lang has runCCompile + runCTest): %v", err)
	}
	if g.Objects["F"].ImplLang != "C" {
		t.Errorf("implLang not persisted; got %q", g.Objects["F"].ImplLang)
	}
}

func TestMergeObject_AcceptsCanonicalLangs(t *testing.T) {
	// Every lang typecalc/lang/ has a real invoker for must be
	// graph-acceptable. Drift here = the bug we just fixed.
	for _, lang := range []string{"Go", "TypeScript", "JavaScript", "Python", "Rust", "C", "HTML"} {
		t.Run(lang, func(t *testing.T) {
			g := NewGraph()
			g.Objects["F"] = NewObject("defs/F.ts", "")
			if err := g.MergeObject("F", map[string]any{"implLang": lang}); err != nil {
				t.Fatalf("implLang=%q must be accepted: %v", lang, err)
			}
		})
	}
}

func TestMergeObject_AcceptsEmptyImplLang(t *testing.T) {
	g := NewGraph()
	g.Objects["F"] = NewObject("defs/F.ts", "")
	if err := g.MergeObject("F", map[string]any{"implLang": ""}); err != nil {
		t.Fatalf("empty implLang (unknown / not yet detected) must be accepted: %v", err)
	}
}

func TestMergeObject_AcceptsUnknownLang_RuntimeWillReject(t *testing.T) {
	// Unknown langs (e.g. Java) used to be in the whitelist as a placeholder
	// despite never being implemented. They now pass the graph layer (no
	// drift-prone hardcoded list); typecalc/lang/ returns Insufficient at
	// the compile step. The graph layer is no longer in the business of
	// curating what typecalc supports — that's typecalc's job.
	g := NewGraph()
	g.Objects["F"] = NewObject("defs/F.ts", "")
	if err := g.MergeObject("F", map[string]any{"implLang": "Java"}); err != nil {
		t.Fatalf("unknown implLang must pass graph layer (typecalc enforces runtime support): %v", err)
	}
}

func TestMergeObject_RejectsNonStringImplLang(t *testing.T) {
	g := NewGraph()
	g.Objects["F"] = NewObject("defs/F.ts", "")
	if err := g.MergeObject("F", map[string]any{"implLang": 123}); err == nil {
		t.Fatal("non-string implLang must be rejected (type check, not value check)")
	} else if !strings.Contains(err.Error(), "must be string") {
		t.Fatalf("error should name the type violation: %v", err)
	}
}
