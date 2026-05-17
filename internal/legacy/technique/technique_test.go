package technique

import "testing"

func TestCatalog_HasAll24PlusExtractMethod(t *testing.T) {
	c := Catalog()
	// 6.14: 3+3+4+3+4+4+3 = 24 family techniques + Extract Method = 25.
	if len(c) != 25 {
		t.Fatalf("Part 6.14 lists 24 techniques + Extract Method (25), got %d", len(c))
	}
	ids := map[string]bool{}
	for _, x := range c {
		if ids[x.ID] {
			t.Fatalf("duplicate technique id %q", x.ID)
		}
		ids[x.ID] = true
	}
	if !ids["extract-method"] {
		t.Fatal("the Extract Method ancestor must be present (6.14 Appendix)")
	}
}

func TestFilter_SupersetSemantics(t *testing.T) {
	// No constraints ⇒ whole catalog (设计文档 Part 2.5).
	if len(Filter()) != 25 {
		t.Fatalf("empty filter must return the whole catalog")
	}

	// Require BehaviorPreserving — Extract Method & Break Out Method
	// Object are seeded with it.
	bp := Filter(PropBehaviorPreserving)
	if len(bp) == 0 {
		t.Fatal("expected behavior-preserving techniques")
	}
	for _, x := range bp {
		if !x.has(PropBehaviorPreserving) {
			t.Fatalf("filter leaked a non-matching technique: %s", x.Name)
		}
	}

	// Conjunction: SignaturePreserving AND Reversible ⇒ only techniques
	// that have BOTH (Extract Method, Extract Interface).
	both := Filter(PropSignaturePreserving, PropReversible)
	for _, x := range both {
		if !x.has(PropSignaturePreserving) || !x.has(PropReversible) {
			t.Fatalf("superset semantics violated: %s lacks one required prop", x.Name)
		}
	}
	gotExtractMethod := false
	for _, x := range both {
		if x.ID == "extract-method" {
			gotExtractMethod = true
		}
	}
	if !gotExtractMethod {
		t.Fatal("Extract Method must satisfy {SignaturePreserving, Reversible}")
	}
}

func TestFilter_LangCStyleFamily(t *testing.T) {
	c := Filter(PropLangC)
	if len(c) != 4 {
		t.Fatalf("Family 6 has 4 C-style techniques, got %d (%v)", len(c), c)
	}
}

// Text Redefinition stays selectable but flagged (Feathers 谨慎使用):
// the LLM must SEE it to consciously reject, not have it hidden.
func TestCatalog_TextRedefinitionIsFlaggedNotHidden(t *testing.T) {
	for _, x := range Catalog() {
		if x.ID == "text-redefinition" {
			if !x.AntiPattern {
				t.Fatal("Text Redefinition must be flagged AntiPattern (6.14)")
			}
			return
		}
	}
	t.Fatal("Text Redefinition must remain in the catalog (visible-but-flagged, not removed)")
}
