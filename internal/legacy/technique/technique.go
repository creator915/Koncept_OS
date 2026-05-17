// Package technique implements the Property-based engineering-experience
// catalog from 屎山代码维护Agent设计文档 v1.0 Part 2.5 + 6.14. Feathers'
// 24+ dependency-breaking techniques are NOT a decision tree (that would
// be a predefined path — 原则 B forbids it). Each technique carries a
// composable set of trait properties; selection is `filter(required) →
// candidate_set` (Part 2.5 filter_techniques), and the LLM picks an
// inhabitant from the already-filtered set.
//
// 11.E honesty: WHO authoritatively labels every technique's full
// property set is [NOT YET DESIGNED] (doc 倾向: LLM draft + human
// review). What IS designed and built here: the trait vocabulary, the
// Technique structure, the full 6.14 technique LIST verbatim, the
// filter mechanism, and SEED properties for the techniques whose traits
// Part 6.9–6.14 prose pins unambiguously. Sparse property sets are
// honest under-specification (the filter just yields fewer guarantees),
// NOT silently-wrong completeness.
package technique

// TechniqueProperty is one composable trait (设计文档 Part 2.5 —
// non-exclusive). Grouped exactly as the doc groups them.
type TechniqueProperty string

const (
	// 入侵度
	PropLocalInvasive      TechniqueProperty = "LocalInvasive"
	PropClassInvasive      TechniqueProperty = "ClassInvasive"
	PropCallSiteInvasive   TechniqueProperty = "CallSiteInvasive"
	PropStructuralInvasive TechniqueProperty = "StructuralInvasive"
	// 安全性
	PropSignaturePreserving TechniqueProperty = "SignaturePreserving"
	PropBehaviorPreserving  TechniqueProperty = "BehaviorPreserving"
	PropReversible          TechniqueProperty = "Reversible"
	// 适用语言
	PropLangC          TechniqueProperty = "Lang_C"
	PropLangCpp        TechniqueProperty = "Lang_Cpp"
	PropLangJava       TechniqueProperty = "Lang_Java"
	PropLangAnyOO      TechniqueProperty = "Lang_Any_OO"
	PropLangAnyStatic  TechniqueProperty = "Lang_Any_Static"
	PropLangAnyDynamic TechniqueProperty = "Lang_Any_Dynamic"
	// 解决的依赖类型
	PropBreaksConstructorDep TechniqueProperty = "BreaksConstructorDep"
	PropBreaksGlobalDep      TechniqueProperty = "BreaksGlobalDep"
	PropBreaksStaticCallDep  TechniqueProperty = "BreaksStaticCallDep"
	PropBreaksInheritanceDep TechniqueProperty = "BreaksInheritanceDep"
	PropBreaksLibraryDep     TechniqueProperty = "BreaksLibraryDep"
	// 产生的 seam type
	PropProducesObjectSeam  TechniqueProperty = "ProducesObjectSeam"
	PropProducesLinkSeam    TechniqueProperty = "ProducesLinkSeam"
	PropProducesPreprocSeam TechniqueProperty = "ProducesPreprocSeam"
	// 风险类别
	PropRiskOfBuildBreakage      TechniqueProperty = "RiskOfBuildBreakage"
	PropRiskOfHiddenCallerImpact TechniqueProperty = "RiskOfHiddenCallerImpact"
	PropRiskOfDynamicLookupBypass TechniqueProperty = "RiskOfDynamicLookupBypass"
)

// Technique — 设计文档 Part 2.5. AntiPattern marks the entries Part
// 6.14 explicitly flags (Text Redefinition).
type Technique struct {
	ID         string
	Name       string
	Family     string
	Properties []TechniqueProperty
	AntiPattern bool
}

func (t Technique) has(p TechniqueProperty) bool {
	for _, q := range t.Properties {
		if q == p {
			return true
		}
	}
	return false
}

// Catalog returns the full 6.14 list. Property seeds are justified only
// where Part 6.9–6.14 prose pins them; everything else is left sparse
// (11.E review-pending) rather than guessed.
func Catalog() []Technique {
	P := func(ps ...TechniqueProperty) []TechniqueProperty { return ps }
	return []Technique{
		// Family 1 — Globals (break global dependency).
		{ID: "encapsulate-global-refs", Name: "Encapsulate Global References", Family: "Globals", Properties: P(PropBreaksGlobalDep, PropClassInvasive)},
		{ID: "replace-global-with-getter", Name: "Replace Global Reference with Getter", Family: "Globals", Properties: P(PropBreaksGlobalDep, PropProducesObjectSeam)},
		{ID: "introduce-static-setter", Name: "Introduce Static Setter", Family: "Globals", Properties: P(PropBreaksGlobalDep, PropRiskOfHiddenCallerImpact)},
		// Family 2 — Concrete Construction (break constructor dependency).
		{ID: "extract-override-factory", Name: "Extract and Override Factory Method", Family: "ConcreteConstruction", Properties: P(PropBreaksConstructorDep, PropProducesObjectSeam, PropLangAnyOO)},
		{ID: "parameterize-constructor", Name: "Parameterize Constructor", Family: "ConcreteConstruction", Properties: P(PropBreaksConstructorDep, PropSignaturePreserving)},
		{ID: "supersede-instance-variable", Name: "Supersede Instance Variable", Family: "ConcreteConstruction", Properties: P(PropBreaksConstructorDep, PropClassInvasive)},
		// Family 3 — Concrete Inheritance.
		{ID: "extract-interface", Name: "Extract Interface", Family: "ConcreteInheritance", Properties: P(PropBreaksInheritanceDep, PropProducesObjectSeam, PropLangAnyOO, PropSignaturePreserving, PropReversible)},
		{ID: "extract-implementer", Name: "Extract Implementer", Family: "ConcreteInheritance", Properties: P(PropBreaksInheritanceDep, PropProducesObjectSeam, PropLangAnyOO)},
		{ID: "pull-up-feature", Name: "Pull Up Feature", Family: "ConcreteInheritance", Properties: P(PropBreaksInheritanceDep, PropStructuralInvasive)},
		{ID: "push-down-dependency", Name: "Push Down Dependency", Family: "ConcreteInheritance", Properties: P(PropBreaksInheritanceDep, PropStructuralInvasive)},
		// Family 4 — Static / Free functions.
		{ID: "expose-static-method", Name: "Expose Static Method", Family: "StaticFree", Properties: P(PropBreaksStaticCallDep, PropSignaturePreserving)},
		{ID: "introduce-instance-delegator", Name: "Introduce Instance Delegator", Family: "StaticFree", Properties: P(PropBreaksStaticCallDep, PropProducesObjectSeam)},
		{ID: "parameterize-method", Name: "Parameterize Method", Family: "StaticFree", Properties: P(PropBreaksStaticCallDep, PropSignaturePreserving)},
		// Family 5 — Method Body Internals.
		{ID: "extract-override-call", Name: "Extract and Override Call", Family: "MethodBody", Properties: P(PropProducesObjectSeam, PropLangAnyOO, PropLocalInvasive)},
		{ID: "extract-override-getter", Name: "Extract and Override Getter", Family: "MethodBody", Properties: P(PropProducesObjectSeam, PropLangAnyOO)},
		{ID: "subclass-override-method", Name: "Subclass and Override Method", Family: "MethodBody", Properties: P(PropProducesObjectSeam, PropLangAnyOO, PropReversible)},
		{ID: "break-out-method-object", Name: "Break Out Method Object", Family: "MethodBody", Properties: P(PropStructuralInvasive, PropBehaviorPreserving)},
		// Family 6 — Function-level / C-style (Lang_C).
		{ID: "link-substitution", Name: "Link Substitution", Family: "FunctionLevelC", Properties: P(PropLangC, PropProducesLinkSeam)},
		{ID: "definition-completion", Name: "Definition Completion", Family: "FunctionLevelC", Properties: P(PropLangC, PropProducesLinkSeam)},
		{ID: "replace-fn-with-fn-pointer", Name: "Replace Function with Function Pointer", Family: "FunctionLevelC", Properties: P(PropLangC, PropCallSiteInvasive)},
		{ID: "text-redefinition", Name: "Text Redefinition", Family: "FunctionLevelC", Properties: P(PropLangC, PropProducesPreprocSeam, PropRiskOfDynamicLookupBypass), AntiPattern: true},
		// Family 7 — Parameter / Type Adaptation.
		{ID: "adapt-parameter", Name: "Adapt Parameter", Family: "ParamType", Properties: P(PropSignaturePreserving)},
		{ID: "primitivize-parameter", Name: "Primitivize Parameter", Family: "ParamType", Properties: P(PropCallSiteInvasive)},
		{ID: "template-redefinition", Name: "Template Redefinition", Family: "ParamType", Properties: P(PropLangCpp, PropProducesObjectSeam)},
		// Appendix — Extract Method (ancestor of every Extract X).
		{ID: "extract-method", Name: "Extract Method", Family: "Appendix", Properties: P(PropSignaturePreserving, PropBehaviorPreserving, PropReversible, PropLocalInvasive)},
	}
}

// Filter returns every technique whose property set is a SUPERSET of
// the required set (设计文档 Part 2.5: returns all properties ⊇
// required; the LLM then picks from the filtered candidates). No
// required props ⇒ the whole catalog. Anti-pattern entries are kept
// (Text Redefinition is selectable but flagged — Feathers "谨慎使用",
// the LLM must see it to consciously reject it).
func Filter(required ...TechniqueProperty) []Technique {
	var out []Technique
	for _, t := range Catalog() {
		ok := true
		for _, r := range required {
			if !t.has(r) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, t)
		}
	}
	return out
}
