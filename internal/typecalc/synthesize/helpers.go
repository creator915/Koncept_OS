package synthesize

import "context"

// Invoker is the synthesize package's local LLM-callable signature.
// Mirrors review.Invoker; kept as a separate type to keep packages independent.
type Invoker func(ctx context.Context, prompt string) (string, error)

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
