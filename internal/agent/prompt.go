package agent

import (
	_ "embed"

	"github.com/creator915/Koncept_OS/internal/protocol"
)

//go:embed system.md
var systemMD string

// SystemPrompt is the canonical LLM system prompt assembled at process
// init. It concatenates:
//
//  1. protocol.Describe() — the v9.0 single source of truth for the
//     runtime protocol (product layers, finish checklist, anti-patterns,
//     decomposition thresholds, root finish flow, waiver discipline).
//     Generated from internal/protocol/protocol.go.
//
//  2. systemMD (embedded internal/agent/system.md) — the kcpos-specific
//     tool catalog + style guidance. Protocol prose was hoisted out of
//     this file when (1) was introduced so the two never drift again.
//
// Pre-v9.0 system.md duplicated protocol rules from CLAUDE.md verbatim
// and the agent never saw CLAUDE.md (kcpos didn't read it at runtime).
// v9.0 makes the protocol code-defined and feeds it to every agent.
var SystemPrompt = protocol.Describe() + "\n\n---\n\n" + systemMD
