package typecalctools

import (
	"context"
	"fmt"
	"strings"

	"github.com/creator915/Koncept_OS/internal/llm"
	"github.com/creator915/Koncept_OS/internal/typecalc"
)

// typecalcWaiveTool records an explicit waiver: a human-readable
// acknowledgement that kcpos cannot mechanically verify this object
// (Lang has no in-tree runner, port_observation undeclared, side-effect
// only output, etc.), AND how it will be verified out-of-band
// (manual screenshot, downstream integration test, etc.).
//
// Waivers exist to make "I cannot verify" a first-class outcome rather
// than a silent fail-open. The gate refuses to confirm an object that
// has kind=insufficient evidence WITHOUT a matching waiver. The point
// is to force the agent (or human) to make and record an explicit
// decision rather than let unprovable code slide through.
//
// Reasons are checked against a stop-list of hand-wavy phrases — empty
// or one-word reasons are rejected. The verifier field is optional but
// strongly recommended (e.g. "manual play-test by reviewer", "pong
// screenshot stored in K/proofs/").
func typecalcWaiveTool() llm.Tool {
	return llm.Tool{
		Spec: llm.ToolSpec{
			Type: "function",
			Function: llm.ToolFunction{
				Name: "typecalc_waive",
				Description: "Record an explicit waiver for an object whose kind=insufficient evidence cannot be upgraded to test/compile. The waiver acknowledges the limitation and describes how the object will be verified out-of-band.\n\nUse this when typecalc_test or typecalc_compile reported `Insufficient` (e.g. for HTML, Rust, Java, or any language without an in-tree runner). The gate refuses to confirm such objects without a paired waiver.\n\nReason MUST be specific (\"manual play-testing on Chrome 120 confirmed all 8 SPEC items\", NOT \"works fine\"). One-word or hand-wavy reasons are rejected.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object_id": map[string]interface{}{
							"type":        "string",
							"description": "Graph object id requiring a waiver.",
						},
						"reason": map[string]interface{}{
							"type":        "string",
							"description": "Specific explanation of why mechanical verification isn't possible AND how the object will actually be verified.",
						},
						"verifier": map[string]interface{}{
							"type":        "string",
							"description": "Optional. Who or what performs the out-of-band check (e.g. 'human play-test', 'integration test in src/integration.test.js').",
						},
					},
					"required": []string{"object_id", "reason"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			objectID, _ := args["object_id"].(string)
			reason, _ := args["reason"].(string)
			verifier, _ := args["verifier"].(string)
			if objectID == "" {
				return "", fmt.Errorf("object_id required")
			}
			reason = strings.TrimSpace(reason)
			if err := validateWaiverReason(reason); err != nil {
				return "", err
			}
			rec := &typecalc.WaiverEvidence{
				ObjectID: objectID,
				Reason:   reason,
				Verifier: verifier,
			}
			if err := typecalc.WriteWaiver(rec); err != nil {
				return "", err
			}
			return fmt.Sprintf(
				"waived %s — wrote %s\n\n  reason: %s\n  verifier: %s\n",
				objectID,
				typecalc.WaiverEvidencePath(objectID),
				reason,
				ifNonEmpty(verifier, "(unspecified)"),
			), nil
		},
	}
}

// validateWaiverReason rejects empty / hand-wavy / single-word reasons.
// The list is small and conservative: it catches the obvious "works"
// / "ok" / "lgtm" cases that defeat the point. A motivated agent can
// of course write a longer non-reason — that's a human-review problem,
// not a syntactic one. We just stop the easiest abuse.
func validateWaiverReason(reason string) error {
	if reason == "" {
		return fmt.Errorf("reason required (specific explanation, not blank)")
	}
	if len(reason) < 30 {
		return fmt.Errorf("reason too short (%d chars) — describe specifically how the object will be verified", len(reason))
	}
	low := strings.ToLower(reason)
	for _, banned := range []string{"works", "ok", "lgtm", "fine", "looks good", "passing"} {
		if low == banned {
			return fmt.Errorf("reason %q is too vague — explain HOW verification happens out-of-band", reason)
		}
	}
	return nil
}

func ifNonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
