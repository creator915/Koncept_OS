package router

import (
	"context"
	"fmt"
)

// Router is the type-driven dispatch table. Register handlers by their
// input type tag; Run() iterates "current value → handler → next value"
// until the value reaches a terminal type or no handler matches.
//
// Routing is deterministic: same Type tag always maps to the same
// handler. The router never asks "what should run next?" — that
// question doesn't exist in this design. The current value's Type
// determines everything.
type Router struct {
	handlers map[string]Handler
	terminal map[string]bool

	// maxSteps guards against handler chains that fail to converge.
	// Default 200 steps — enough for any realistic typecalc pipeline,
	// small enough that a bug doesn't burn unbounded LLM calls.
	maxSteps int
}

// NewRouter constructs an empty router. Use Register / RegisterTerminal
// to populate before calling Run.
func NewRouter() *Router {
	return &Router{
		handlers: map[string]Handler{},
		terminal: map[string]bool{},
		maxSteps: 200,
	}
}

// SetMaxSteps overrides the convergence guard. Pass 0 to disable
// (only use in tests where you control inputs).
func (r *Router) SetMaxSteps(n int) {
	r.maxSteps = n
}

// Register installs h for its declared input type. Panics if a
// handler is already registered for the same input — name collision
// is always a programmer error.
func (r *Router) Register(h Handler) {
	in := h.Accepts()
	if in == "" {
		panic("router.Register: handler.Accepts() returned empty string")
	}
	if _, exists := r.handlers[in]; exists {
		panic(fmt.Sprintf("router.Register: handler for %q already registered", in))
	}
	r.handlers[in] = h
}

// RegisterTerminal marks a type as terminal. Run() halts when the
// current value matches. Use for absorbing states like
// "Confirmed<Code>" or "Obstacle<Task,Reason>" that don't need
// further processing.
func (r *Router) RegisterTerminal(typeTag string) {
	r.terminal[typeTag] = true
}

// Connectivity validates that every handler's declared Outputs has
// either (a) a registered handler, or (b) is marked terminal. Returns
// the list of orphan output tags. Call once after registration to
// catch missing handlers before Run().
//
// This is the type-driven analog of "did you forget to register the
// CompileError handler?" — a static check that the state graph is
// closed.
func (r *Router) Connectivity() []string {
	var orphans []string
	seen := map[string]bool{}
	for _, h := range r.handlers {
		for _, out := range h.Outputs() {
			if seen[out] {
				continue
			}
			seen[out] = true
			if _, ok := r.handlers[out]; ok {
				continue
			}
			if r.terminal[out] {
				continue
			}
			orphans = append(orphans, out)
		}
	}
	return orphans
}

// Run iterates the state machine from initial until a terminal state.
// Returns the terminal value or an error if (a) no handler matches a
// non-terminal type, (b) a handler returns an error, (c) a handler's
// output Type is not in its declared Outputs, or (d) maxSteps reached.
//
// Each step's pre/post values can be observed via Trace if attached
// (TODO: trace hook for debugging — Phase 5+).
func (r *Router) Run(ctx context.Context, initial TypedValue) (TypedValue, error) {
	current := initial
	steps := 0
	for {
		if r.terminal[current.Type] {
			return current, nil
		}
		h, ok := r.handlers[current.Type]
		if !ok {
			return current, fmt.Errorf("router: no handler for type %q (not terminal either) — registered: %d handlers, %d terminals", current.Type, len(r.handlers), len(r.terminal))
		}
		next, err := h.Handle(ctx, current)
		if err != nil {
			return current, fmt.Errorf("router: handler for %q returned error: %w", current.Type, err)
		}
		// Validate the handler stayed inside its declared outputs.
		allowed := h.Outputs()
		if !inSet(next.Type, allowed) {
			return current, fmt.Errorf("router: handler for %q produced unauthorized output type %q — declared outputs: %v", current.Type, next.Type, allowed)
		}
		current = next
		steps++
		if r.maxSteps > 0 && steps >= r.maxSteps {
			return current, fmt.Errorf("router: exceeded max steps (%d) at type %q — handler chain failed to converge", r.maxSteps, current.Type)
		}
		// Cooperative cancellation.
		if err := ctx.Err(); err != nil {
			return current, err
		}
	}
}

// Step performs exactly one Router transition: looks up the handler
// registered for current.Type, invokes Handle, and validates the
// output Type is in the handler's declared Outputs.
//
// Used by drivers (e.g. agent/outer_loop.go) that need a per-step
// trace; Run() is the all-in-one variant for the common case.
//
// Returns an error when (a) current.Type has no registered handler
// and is not terminal, (b) the handler returns an error, or (c) the
// handler produces an output type not in its declared Outputs.
//
// Calling Step on a terminal value returns the same value with no
// error — callers should check IsTerminal before stepping.
func (r *Router) Step(ctx context.Context, current TypedValue) (TypedValue, error) {
	if r.terminal[current.Type] {
		return current, nil
	}
	h, ok := r.handlers[current.Type]
	if !ok {
		return current, fmt.Errorf("router: no handler for type %q (not terminal either) — registered: %d handlers, %d terminals", current.Type, len(r.handlers), len(r.terminal))
	}
	next, err := h.Handle(ctx, current)
	if err != nil {
		return current, fmt.Errorf("router: handler for %q returned error: %w", current.Type, err)
	}
	if !inSet(next.Type, h.Outputs()) {
		return current, fmt.Errorf("router: handler for %q produced unauthorized output type %q — declared outputs: %v", current.Type, next.Type, h.Outputs())
	}
	return next, nil
}

// IsTerminal exposes the terminal check for tests + tracing.
func (r *Router) IsTerminal(typeTag string) bool {
	return r.terminal[typeTag]
}

// Has reports whether a handler is registered for the type tag.
func (r *Router) Has(typeTag string) bool {
	_, ok := r.handlers[typeTag]
	return ok
}
