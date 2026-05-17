// Package typedio implements the typed LLM exchange from 屎山代码维护
// Agent设计文档 v1.0 Part 2.7. 11.C (the IO mechanism choice) was
// "[决定于落地阶段]";落地 picks the doc's 当前倾向: 选项 2 — JSON +
// post-validator + reject/retry. 原则 A made concrete: a reply whose
// shape violates the DecisionAsk schema FAILS validation and the
// caller retries or escalates — the type, not an if-else, decides what
// is acceptable.
//
// This closes the loop with Part 2.5: a ChooseTechnique ask carries the
// ALREADY-FILTERED candidate set (technique.Filter output); a reply
// picking outside it is rejected — the LLM cannot escape the filtered
// inhabitants (原则 B).
package typedio

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/creator915/Koncept_OS/internal/legacy/characterize"
)

// DecisionKind — the DecisionAsk variants from 设计文档 Part 2.7.
type DecisionKind string

const (
	AskChooseTechnique       DecisionKind = "ChooseTechnique"
	AskInferProperty         DecisionKind = "InferProperty"
	AskResolveOracleConflict DecisionKind = "ResolveOracleConflict"
	AskEscalateLayer         DecisionKind = "EscalateLayer"
	AskGenerateArtifact      DecisionKind = "GenerateArtifact"
)

// DecisionAsk is the typed question put to the LLM (设计文档 Part 2.7).
type DecisionAsk struct {
	Kind               DecisionKind
	Target             string
	CandidateSet       []string // ChooseTechnique: pre-filtered technique ids (Part 2.5)
	ArtifactKind       string   // GenerateArtifact: TestCase|RefactoredCode|SproutBody|…
	ConflictingOracles []string // ResolveOracleConflict
	SuggestedLayer     string   // EscalateLayer
}

// TypedPrompt is the whole typed exchange context (设计文档 Part 2.7).
type TypedPrompt struct {
	TaskContext    string
	CurrentBranch  string
	DecisionAsk    DecisionAsk
	AvailableTools []string
}

// TypedReply is the LLM's reply. It MUST declare the assumptions the
// decision introduced (设计文档 Part 2.7 / Part 10.4: 每个 LLM 决策必须
// 显式声明引入的假设) and a reasoning trace.
type TypedReply struct {
	Decision              json.RawMessage           `json:"decision"`
	IntroducedAssumptions []characterize.Assumption `json:"introducedAssumptions"`
	ConfidenceClaim       string                    `json:"confidenceClaim,omitempty"`
	ReasoningTrace        string                    `json:"reasoningTrace"`
}

// BuildPrompt renders the strict-JSON contract for a TypedPrompt (选项
// 2). Deterministic — the schema the validator enforces is the schema
// the prompt states, so prompt and validator can't drift.
func BuildPrompt(p TypedPrompt) string {
	var b strings.Builder
	fmt.Fprintf(&b, "TASK: %s\nBRANCH: %s\nDECISION: %s (target=%s)\n",
		p.TaskContext, p.CurrentBranch, p.DecisionAsk.Kind, p.DecisionAsk.Target)
	switch p.DecisionAsk.Kind {
	case AskChooseTechnique:
		fmt.Fprintf(&b, "Pick EXACTLY ONE id from this pre-filtered candidate set (you may NOT invent one):\n  %s\n",
			strings.Join(p.DecisionAsk.CandidateSet, ", "))
		b.WriteString(`Reply JSON: {"decision":{"technique":"<id>"},"introducedAssumptions":[...],"reasoningTrace":"..."}`)
	case AskGenerateArtifact:
		fmt.Fprintf(&b, "Produce an artifact of kind %q.\n", p.DecisionAsk.ArtifactKind)
		b.WriteString(`Reply JSON: {"decision":{"kind":"` + p.DecisionAsk.ArtifactKind + `","body":<...>},"introducedAssumptions":[...],"reasoningTrace":"..."}`)
	case AskResolveOracleConflict:
		fmt.Fprintf(&b, "Choose the winning oracle from: %s\n", strings.Join(p.DecisionAsk.ConflictingOracles, ", "))
		b.WriteString(`Reply JSON: {"decision":{"winner":"<oracleId>"},"introducedAssumptions":[...],"reasoningTrace":"..."}`)
	case AskEscalateLayer:
		b.WriteString(`Reply JSON: {"decision":{"toLayer":"<layer>"},"introducedAssumptions":[...],"reasoningTrace":"..."}`)
	default:
		b.WriteString(`Reply JSON: {"decision":<...>,"introducedAssumptions":[...],"reasoningTrace":"..."}`)
	}
	return b.String()
}

// ValidateReply parses + schema-checks a raw reply against the ask
// (原则 A: type mismatch ⇒ error, caller retries/escalates). Returns
// the decision-specific selected value (e.g. the chosen technique id)
// for the caller's convenience.
func ValidateReply(ask DecisionAsk, raw []byte) (TypedReply, string, error) {
	var r TypedReply
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, "", fmt.Errorf("typedio: reply is not valid JSON: %w", err)
	}
	if strings.TrimSpace(r.ReasoningTrace) == "" {
		return r, "", fmt.Errorf("typedio: reply missing reasoningTrace (Part 2.7 requires it)")
	}
	if len(r.Decision) == 0 {
		return r, "", fmt.Errorf("typedio: reply missing decision")
	}
	switch ask.Kind {
	case AskChooseTechnique:
		var d struct {
			Technique string `json:"technique"`
		}
		if err := json.Unmarshal(r.Decision, &d); err != nil || d.Technique == "" {
			return r, "", fmt.Errorf("typedio: ChooseTechnique decision must be {\"technique\":\"<id>\"}")
		}
		if !contains(ask.CandidateSet, d.Technique) {
			// 原则 B: cannot escape the pre-filtered inhabitants.
			return r, "", fmt.Errorf("typedio: technique %q is NOT in the filtered candidate set %v — rejected", d.Technique, ask.CandidateSet)
		}
		return r, d.Technique, nil
	case AskResolveOracleConflict:
		var d struct {
			Winner string `json:"winner"`
		}
		if err := json.Unmarshal(r.Decision, &d); err != nil || d.Winner == "" {
			return r, "", fmt.Errorf("typedio: ResolveOracleConflict decision must be {\"winner\":\"<oracleId>\"}")
		}
		if !contains(ask.ConflictingOracles, d.Winner) {
			return r, "", fmt.Errorf("typedio: winner %q is not among the conflicting oracles %v", d.Winner, ask.ConflictingOracles)
		}
		return r, d.Winner, nil
	case AskGenerateArtifact:
		var d struct {
			Kind string          `json:"kind"`
			Body json.RawMessage `json:"body"`
		}
		if err := json.Unmarshal(r.Decision, &d); err != nil {
			return r, "", fmt.Errorf("typedio: GenerateArtifact decision must be {\"kind\":...,\"body\":...}")
		}
		if d.Kind != ask.ArtifactKind {
			return r, "", fmt.Errorf("typedio: artifact kind %q != asked %q", d.Kind, ask.ArtifactKind)
		}
		if len(d.Body) == 0 {
			return r, "", fmt.Errorf("typedio: GenerateArtifact reply has empty body")
		}
		return r, d.Kind, nil
	case AskEscalateLayer:
		var d struct {
			ToLayer string `json:"toLayer"`
		}
		if err := json.Unmarshal(r.Decision, &d); err != nil || d.ToLayer == "" {
			return r, "", fmt.Errorf("typedio: EscalateLayer decision must be {\"toLayer\":\"<layer>\"}")
		}
		return r, d.ToLayer, nil
	default:
		return r, "", nil
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
