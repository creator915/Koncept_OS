package typedio

import (
	"testing"

	"github.com/creator915/Koncept_OS/internal/legacy/technique"
)

// Full loop with Part 2.5: filter techniques → that set IS the
// candidate set → only an in-set pick validates (原则 A + 原则 B: the
// LLM cannot escape the filtered inhabitants).
func TestValidateReply_ChooseTechniqueMustStayInFilteredSet(t *testing.T) {
	cands := technique.Filter(technique.PropBehaviorPreserving)
	ids := make([]string, 0, len(cands))
	for _, c := range cands {
		ids = append(ids, c.ID)
	}
	ask := DecisionAsk{Kind: AskChooseTechnique, Target: "monster()", CandidateSet: ids}

	good := []byte(`{"decision":{"technique":"extract-method"},"introducedAssumptions":[],"reasoningTrace":"lowest coupling chunk"}`)
	_, picked, err := ValidateReply(ask, good)
	if err != nil || picked != "extract-method" {
		t.Fatalf("in-set pick must validate, got picked=%q err=%v", picked, err)
	}

	// A real technique id that is NOT in the behavior-preserving filter
	// (link-substitution is Lang_C/LinkSeam, not behavior-preserving).
	bad := []byte(`{"decision":{"technique":"link-substitution"},"introducedAssumptions":[],"reasoningTrace":"x"}`)
	if _, _, err := ValidateReply(ask, bad); err == nil {
		t.Fatal("a technique outside the filtered candidate set MUST be rejected (原则 B)")
	}

	// A hallucinated id is likewise rejected.
	halluc := []byte(`{"decision":{"technique":"do-magic"},"introducedAssumptions":[],"reasoningTrace":"x"}`)
	if _, _, err := ValidateReply(ask, halluc); err == nil {
		t.Fatal("invented technique must be rejected")
	}
}

func TestValidateReply_SchemaViolationsAreErrors(t *testing.T) {
	ask := DecisionAsk{Kind: AskChooseTechnique, CandidateSet: []string{"extract-method"}}

	if _, _, err := ValidateReply(ask, []byte(`not json`)); err == nil {
		t.Fatal("non-JSON reply must error (原则 A)")
	}
	// Missing reasoningTrace (Part 2.7 requires it).
	noTrace := []byte(`{"decision":{"technique":"extract-method"},"introducedAssumptions":[]}`)
	if _, _, err := ValidateReply(ask, noTrace); err == nil {
		t.Fatal("missing reasoningTrace must error")
	}
	// Wrong decision shape for the ask kind.
	wrong := []byte(`{"decision":{"winner":"O1"},"reasoningTrace":"x"}`)
	if _, _, err := ValidateReply(ask, wrong); err == nil {
		t.Fatal("decision shape not matching the ask kind must error")
	}
}

func TestValidateReply_GenerateArtifactKindMustMatch(t *testing.T) {
	ask := DecisionAsk{Kind: AskGenerateArtifact, ArtifactKind: "SproutBody"}
	ok := []byte(`{"decision":{"kind":"SproutBody","body":{"code":"return x+1"}},"reasoningTrace":"sprout"}`)
	if _, k, err := ValidateReply(ask, ok); err != nil || k != "SproutBody" {
		t.Fatalf("matching artifact kind must validate, got k=%q err=%v", k, err)
	}
	mismatch := []byte(`{"decision":{"kind":"RefactoredCode","body":{}},"reasoningTrace":"x"}`)
	if _, _, err := ValidateReply(ask, mismatch); err == nil {
		t.Fatal("artifact kind mismatch must error")
	}
}

func TestValidateReply_ResolveOracleConflictWinnerMustBeAmongConflicting(t *testing.T) {
	ask := DecisionAsk{Kind: AskResolveOracleConflict, ConflictingOracles: []string{"O1", "O2"}}
	if _, w, err := ValidateReply(ask, []byte(`{"decision":{"winner":"O2"},"reasoningTrace":"O2 has fresher evidence"}`)); err != nil || w != "O2" {
		t.Fatalf("valid winner must pass, got w=%q err=%v", w, err)
	}
	if _, _, err := ValidateReply(ask, []byte(`{"decision":{"winner":"O9"},"reasoningTrace":"x"}`)); err == nil {
		t.Fatal("winner not among the conflicting oracles must error")
	}
}

func TestBuildPrompt_StatesTheCandidateSetForChooseTechnique(t *testing.T) {
	p := TypedPrompt{
		TaskContext:   "break ctor dep",
		CurrentBranch: "root",
		DecisionAsk:   DecisionAsk{Kind: AskChooseTechnique, Target: "Foo()", CandidateSet: []string{"parameterize-constructor", "extract-override-factory"}},
	}
	out := BuildPrompt(p)
	if !contains([]string{}, "") && (len(out) == 0) {
		t.Fatal("prompt must be non-empty")
	}
	for _, want := range []string{"parameterize-constructor", "extract-override-factory", "ChooseTechnique"} {
		if !containsSub(out, want) {
			t.Fatalf("prompt must state %q, got: %s", want, out)
		}
	}
}

func containsSub(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(sub) > 0 && indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
