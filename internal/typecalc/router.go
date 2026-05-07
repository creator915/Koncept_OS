package typecalc

import (
	"context"
	"fmt"
)

// Router runs typed values through registered rules. The driver loop is
// modeled after §8.2:
//
//	while !isTerminal(v.Tag) {
//	    rule = registry.Match(v.Tag)
//	    v = rule.Handler(ctx, env, v)
//	    optional: format-check v before next iteration
//	}
//
// "Terminal" tags are those for which no rule is registered, or which
// the caller explicitly marks as terminal (e.g. KindObstacle bubbles up
// to the parent session — the router stops and returns it).
type Router struct {
	Registry *Registry
	Env      *RuleEnv

	// MaxSteps caps the number of router iterations. A handler returning
	// the same tag repeatedly (which a buggy rule might) is bounded here
	// rather than running forever.
	MaxSteps int

	// Terminals declares which tags should halt the loop and return the
	// value. Defaults to TerminalKinds() if empty.
	Terminals []Kind
}

// TerminalKinds returns the kinds the router halts on by default. These
// are escalation/error/handoff tags that the caller is expected to act on
// (rather than feed back into the rule loop).
func TerminalKinds() []Kind {
	return []Kind{
		KindObstacle,
		KindClarificationReq,
		KindFormatError,
		KindPermissionDenied,
		KindFaultLocated,
		KindDesignChange,
		KindCannotReproduce,
	}
}

// Run drives a typed value through the rule registry. Returns either the
// terminal value, or the value at the point of an error.
func (r *Router) Run(ctx context.Context, initial *TypedValue) (*TypedValue, error) {
	if r.Registry == nil {
		return initial, fmt.Errorf("router has no registry")
	}
	maxSteps := r.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 64
	}
	terminals := r.Terminals
	if len(terminals) == 0 {
		terminals = TerminalKinds()
	}
	terminalSet := make(map[Kind]struct{}, len(terminals))
	for _, k := range terminals {
		terminalSet[k] = struct{}{}
	}

	current := initial
	for step := 0; step < maxSteps; step++ {
		if _, halt := terminalSet[current.Kind]; halt {
			return current, nil
		}
		// State==Confirmed is also terminal (success). It can apply to
		// any content kind (Code, Description, Architecture).
		if current.State == StateConfirmed {
			return current, nil
		}
		// Format-check before dispatch so a malformed value short-circuits
		// to KindFormatError early.
		if fe := CheckFormat(current); fe != nil {
			return fe, nil
		}
		rule, ok := r.Registry.Match(current.Tag())
		if !ok {
			return current, nil // no rule = terminal
		}
		next, err := rule.Handler(ctx, r.Env, current)
		if err != nil {
			return current, fmt.Errorf("rule %s: %w", rule.Name, err)
		}
		if next == nil {
			return current, fmt.Errorf("rule %s returned nil", rule.Name)
		}
		// Validate the produced tag is in the rule's declared output sum.
		if len(rule.Output) > 0 {
			producedTag := next.Tag()
			if !rule.Output.Includes(producedTag) {
				// Tolerate kind-only matches: if any sum entry's Kind
				// matches the produced kind, accept the value.
				if _, kindMatches := rule.Output.FindKind(producedTag.Kind); !kindMatches {
					return formatErr("rule %s produced %s, not in declared output %s",
						rule.Name, producedTag, rule.Output.String()), nil
				}
			}
		}
		current = next
	}
	return current, fmt.Errorf("router exceeded MaxSteps=%d at tag %s", maxSteps, current.Tag())
}

// IsTerminal reports whether a value's kind is in the default terminal set.
// Useful for callers that want to decide between continuing and stopping
// without running the full router.
func IsTerminal(tv *TypedValue) bool {
	if tv == nil {
		return true
	}
	if tv.State == StateConfirmed {
		return true
	}
	for _, k := range TerminalKinds() {
		if tv.Kind == k {
			return true
		}
	}
	return false
}
