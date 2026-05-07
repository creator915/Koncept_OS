package typecalc

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Actor identifies who executes a rule. Per §3.1, the actor decides who
// produces the output: an LLM call, a system function, the compiler, the
// tester, the type-tag selector, or a human (escalation).
type Actor string

const (
	ActorLLM      Actor = "llm"
	ActorSystem   Actor = "system"
	ActorCompiler Actor = "compiler"
	ActorTester   Actor = "tester"
	ActorChecker  Actor = "checker"
	ActorSelector Actor = "selector"
	ActorHuman    Actor = "human"
)

// Handler is the executable body of a Rule. It consumes a list of input
// TypedValues (in the order declared by Rule.Input) and produces exactly
// one TypedValue. A handler that "branches" (sum-type output) is responsible
// for picking which branch to emit; the router enforces only that the
// emitted value's tag is in Rule.Output.
type Handler func(ctx context.Context, env *RuleEnv, inputs ...*TypedValue) (*TypedValue, error)

// Rule is one entry in the type calculation table from §3. Most rules in
// the doc are written as "input × ... ⇒ output | output | ..."; we
// represent the inputs as ordered Tag patterns and the legal outputs as a
// SumType.
type Rule struct {
	Name    string
	Actor   Actor
	Input   []Tag    // ordered; matched positionally against router input
	Output  SumType  // legal alternatives — handler picks one
	Handler Handler

	// Description is the human-readable doc text from §3 (one or two
	// sentences). Surfaced in tracing.
	Description string
}

// RuleEnv is the ambient context handlers see — the LLM client, current
// session id, capability set, working directory, etc. It is intentionally
// loose: handlers cherry-pick the fields they need.
//
// Concrete fields are added as handlers are wired in (compile.go,
// test.go, llm_handler.go, etc.). The struct lives in this file so all
// rule files can reference it without import cycles.
type RuleEnv struct {
	WorkDir   string
	SessionID string
	Caps      CapSet

	// LLMInvoker is the function the rule registry calls when a rule's
	// Actor is ActorLLM. It receives the system prompt + user messages
	// (already containing TYPE: header expectations) and returns the raw
	// LLM response text. Decoupled as a function pointer so unit tests
	// can stub it without depending on the chat package.
	LLMInvoker LLMInvoker

	// CompileInvoker / TestInvoker are similarly stubbable. They run real
	// language toolchains in production (compile.go, test.go).
	CompileInvoker CompileInvoker
	TestInvoker    TestInvoker

	// MaxRetries caps the compile / test loops (§7.1, §7.2). 0 = use the
	// package default (defaultMaxRetries).
	MaxRetries int
}

// LLMInvoker is the function signature for invoking an LLM with a prompt
// and the legal sum type. The implementation is responsible for inserting
// instructions about the TYPE: header convention.
type LLMInvoker func(ctx context.Context, env *RuleEnv, prompt string, expected SumType) (raw string, err error)

// CompileInvoker runs the real compiler for a given language and source
// payload. Returns either Compiled-tagged output, or a CompileError. The
// implementation lives in compile.go; the field is here so the env can
// be stubbed for tests.
type CompileInvoker func(ctx context.Context, env *RuleEnv, src *TypedValue) (*TypedValue, error)

// TestInvoker runs the real test runner over the given Compiled source
// and TestSuite. Returns either Tested<Pass> or a TestError.
type TestInvoker func(ctx context.Context, env *RuleEnv, compiled, suite *TypedValue) (*TypedValue, error)

// Registry holds all rules indexed by name and by input tag head (the
// first input's Tag). Routing dispatches by matching the input head; if
// multiple rules share the same head we pick the most specific (later-
// registered rule wins on a tie, matching the ergonomic intuition that
// you can override a default rule by registering a more specialized one).
type Registry struct {
	byName map[string]*Rule
	order  []string // registration order, for deterministic introspection
}

// NewRegistry returns an empty registry. RegisterDefaults populates it
// with the rules from §3.
func NewRegistry() *Registry {
	return &Registry{byName: map[string]*Rule{}}
}

// Register adds a rule to the registry. Returns an error if a rule with
// the same name already exists.
func (r *Registry) Register(rule *Rule) error {
	if rule == nil {
		return fmt.Errorf("nil rule")
	}
	if rule.Name == "" {
		return fmt.Errorf("rule has no name")
	}
	if _, exists := r.byName[rule.Name]; exists {
		return fmt.Errorf("rule %q already registered", rule.Name)
	}
	r.byName[rule.Name] = rule
	r.order = append(r.order, rule.Name)
	return nil
}

// Lookup returns the rule with the given name.
func (r *Registry) Lookup(name string) (*Rule, bool) {
	rule, ok := r.byName[name]
	return rule, ok
}

// Match finds a rule whose input head matches the given tag. The return
// is the most-specific rule — if a rule's Tag matches all three of
// (Kind, State, Lang), it wins over a rule that only matches Kind. We do
// not implement full unification here; in practice rule inputs are
// distinct enough that ties don't arise.
func (r *Registry) Match(head Tag) (*Rule, bool) {
	type cand struct {
		rule *Rule
		spec int // specificity score
	}
	var best *cand
	for _, name := range r.order {
		rule := r.byName[name]
		if len(rule.Input) == 0 {
			continue
		}
		pat := rule.Input[0]
		if !tagMatches(pat, head) {
			continue
		}
		score := tagSpecificity(pat)
		if best == nil || score >= best.spec {
			best = &cand{rule: rule, spec: score}
		}
	}
	if best == nil {
		return nil, false
	}
	return best.rule, true
}

// Names returns rule names in registration order.
func (r *Registry) Names() []string {
	names := append([]string(nil), r.order...)
	sort.Strings(names)
	return names
}

// tagMatches reports whether pat matches the given head. Empty fields in
// pat are wildcards (the doc uses bare "Code" to mean "Code with any
// Lang/State"); the head's specific Lang/State still match a pattern that
// leaves those slots empty.
func tagMatches(pat, head Tag) bool {
	if pat.Kind != head.Kind {
		return false
	}
	if pat.State != StateUntyped && pat.State != head.State {
		return false
	}
	if pat.Lang != LangNone && pat.Lang != head.Lang {
		return false
	}
	return true
}

func tagSpecificity(t Tag) int {
	score := 1
	if t.State != StateUntyped {
		score += 2
	}
	if t.Lang != LangNone {
		score += 4
	}
	return score
}

// String returns a human-readable summary of the registry — useful for
// /typecalc list or similar debug surfaces.
func (r *Registry) String() string {
	var b strings.Builder
	b.WriteString("Type calculator rules:\n")
	for _, name := range r.order {
		rule := r.byName[name]
		var ins []string
		for _, t := range rule.Input {
			ins = append(ins, t.String())
		}
		fmt.Fprintf(&b, "  %s [actor=%s] %s ⇒ %s\n", rule.Name, rule.Actor,
			strings.Join(ins, " × "), rule.Output.String())
	}
	return b.String()
}
