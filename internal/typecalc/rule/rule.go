// Package rule implements the type-calculator rule registry, the router
// that dispatches typed values through registered rules, and the §3
// default rule set (defaults.go) that wires the whole system together.
//
// Sits one level above typecalc/lang and depends on it for the default
// CompileInvoker / TestInvoker referenced by the compile / run_test rule
// handlers.
package rule

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// Actor identifies who executes a rule.
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
// one TypedValue.
type Handler func(ctx context.Context, env *core.RuleEnv, inputs ...*core.TypedValue) (*core.TypedValue, error)

// Rule is one entry in the type calculation table from §3.
type Rule struct {
	Name        string
	Actor       Actor
	Input       []core.Tag    // ordered; matched positionally against router input
	Output      core.SumType  // legal alternatives — handler picks one
	Handler     Handler
	Description string
}

// Registry holds all rules indexed by name. Routing dispatches by
// matching the first input's Tag; the most specific rule wins.
type Registry struct {
	byName map[string]*Rule
	order  []string
}

// NewRegistry returns an empty registry. RegisterDefaults populates it
// with the rules from §3.
func NewRegistry() *Registry {
	return &Registry{byName: map[string]*Rule{}}
}

// Register adds a rule. Returns an error if name collides.
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
// is the most-specific rule.
func (r *Registry) Match(head core.Tag) (*Rule, bool) {
	type cand struct {
		rule *Rule
		spec int
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

// Names returns rule names in sorted order.
func (r *Registry) Names() []string {
	names := append([]string(nil), r.order...)
	sort.Strings(names)
	return names
}

// tagMatches reports whether pat matches head. Empty fields in pat are
// wildcards.
func tagMatches(pat, head core.Tag) bool {
	if pat.Kind != head.Kind {
		return false
	}
	if pat.State != core.StateUntyped && pat.State != head.State {
		return false
	}
	if pat.Lang != core.LangNone && pat.Lang != head.Lang {
		return false
	}
	return true
}

func tagSpecificity(t core.Tag) int {
	score := 1
	if t.State != core.StateUntyped {
		score += 2
	}
	if t.Lang != core.LangNone {
		score += 4
	}
	return score
}

// String returns a human-readable summary of the registry.
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
