package characterize

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
	"github.com/creator915/Koncept_OS/internal/typecalc/harness"
	"github.com/creator915/Koncept_OS/internal/typecalc/lang"
	"github.com/creator915/Koncept_OS/internal/typecalc/synthesize"
)

// adapter.go binds the engine's pure seams to the real kcpos
// machinery. The engine (engine.go) stays free of network/process so
// it is unit-testable; everything that touches an LLM or a subprocess
// lives here, behind SynthProbes / RunHarness closures.

// RealSynthesizeWithInvoker wraps the greenfield synthesizer for the
// brownfield probe-generation step. Asymmetry vs greenfield (设计文档
// 原则 D, black/white parallel): we feed the RECOVERED signature +
// description (a white-box read of untrusted code) and we KEEP ONLY the
// synthesizer's stimuli (Call/Setup). Any Expect it emits is discarded
// — for legacy code the synthesizer cannot know the contract, only
// observation can (Feathers: char tests have no moral authority).
func RealSynthesizeWithInvoker(invoke synthesize.Invoker) SynthProbes {
	return func(ctx context.Context, spec ProbeSpec) ([]CharProbe, error) {
		out, err := synthesize.SynthesizeWithInvoker(ctx, synthesize.SynthesizeInputs{
			ObjectID:    spec.ObjectID,
			Lang:        spec.Lang,
			ImplSymbol:  spec.ImplSymbol,
			Intent:      "Characterize the CURRENT behavior of an untrusted legacy artifact. Generate diverse input probes (happy path, boundaries, empty, extreme, null, type edges). Do NOT assume any correct output — expectations are filled from observation, not from you.",
			Description: spec.Description,
			Signature:   spec.Signature,
			Consumes:    spec.Consumes,
			Produces:    spec.Produces,
		}, invoke)
		if err != nil {
			return nil, err
		}
		if out == nil || len(out.Cases) == 0 {
			return nil, fmt.Errorf("characterize: synthesizer produced no structured cases (got testCode-only or CANNOT_SYNTHESIZE) — cannot derive probes")
		}
		probes := make([]CharProbe, 0, len(out.Cases))
		for _, c := range out.Cases {
			// Discard c.Expect deliberately (see doc above).
			probes = append(probes, CharProbe{Name: c.Name, Setup: c.Setup, Call: c.Call})
		}
		return probes, nil
	}
}

// DefaultSynthesize uses the provider configured from the environment
// (same path greenfield synthesize_tests uses). Suitable for the live
// `kcpos characterize` command.
func DefaultSynthesize() SynthProbes {
	return func(ctx context.Context, spec ProbeSpec) ([]CharProbe, error) {
		out, err := synthesize.SynthesizeTests(ctx, synthesize.SynthesizeInputs{
			ObjectID:    spec.ObjectID,
			Lang:        spec.Lang,
			ImplSymbol:  spec.ImplSymbol,
			Intent:      "Characterize the CURRENT behavior of an untrusted legacy artifact. Generate diverse input probes; expectations come from observation, not from you.",
			Description: spec.Description,
			Signature:   spec.Signature,
			Consumes:    spec.Consumes,
			Produces:    spec.Produces,
		})
		if err != nil {
			return nil, err
		}
		if out == nil || len(out.Cases) == 0 {
			return nil, fmt.Errorf("characterize: synthesizer produced no structured cases — cannot derive probes")
		}
		probes := make([]CharProbe, 0, len(out.Cases))
		for _, c := range out.Cases {
			probes = append(probes, CharProbe{Name: c.Name, Setup: c.Setup, Call: c.Call})
		}
		return probes, nil
	}
}

// traceBundle is the on-disk shape the kcpos harness writes (a unified
// bundle with a runtimeTrace.calls array). We read it back to recover
// what the legacy artifact actually did.
type traceBundle struct {
	ObjectID     string `json:"objectId"`
	RuntimeTrace *struct {
		Calls []core.RuntimeCall `json:"calls"`
	} `json:"runtimeTrace"`
}

// RealHarness renders the probes into a runnable suite bound to the
// UNTRUSTED legacy artifact (ImplPath = the legacy file) and runs it
// through the same TestRunInvoker greenfield uses. It strips every
// probe's Expect before rendering: the harness then performs pure
// observation (snapshot inputs → call → snapshot outputs → appendTrace)
// with zero assertions, so the run never "fails" — char tests judge
// nothing, they only record (Feathers 6.6 核心姿态). The observed trace
// is read back from a temp bundle isolated from the object's real
// evidence so characterization never pollutes greenfield state.
func RealHarness() RunHarness {
	return func(ctx context.Context, req HarnessRequest) (*core.RuntimeTrace, error) {
		absArtifact, err := filepath.Abs(req.ArtifactPath)
		if err != nil {
			return nil, fmt.Errorf("characterize: resolve artifact path: %w", err)
		}
		body, err := os.ReadFile(absArtifact)
		if err != nil {
			return nil, fmt.Errorf("characterize: read legacy artifact: %w", err)
		}
		coreLang, langStr, ok := normalizeLang(req.Lang)
		if !ok {
			return nil, fmt.Errorf("characterize: unsupported language %q (MVP front stage supports the harness languages: Python/JavaScript/TypeScript/HTML)", req.Lang)
		}

		// Pure-observation cases: stimulus only, no assertions.
		cases := make([]core.TestCase, 0, len(req.Probes))
		for _, p := range req.Probes {
			cases = append(cases, core.TestCase{Name: p.Name, Setup: p.Setup, Call: p.Call})
		}
		te := &core.TestsEvidence{ObjectID: req.ObjectID, Lang: langStr, Cases: cases}

		tmpDir, err := os.MkdirTemp("", "kcpos-char-")
		if err != nil {
			return nil, fmt.Errorf("characterize: temp dir: %w", err)
		}
		defer os.RemoveAll(tmpDir)
		absTrace := filepath.Join(tmpDir, "char-trace.json")

		rendered, ok := harness.Render(harness.RenderInputs{
			Tests:           te,
			InputPorts:      nil,
			OutputPorts:     req.Produces,
			ImplPath:        absArtifact,
			TracePath:       absTrace,
			PortObservation: req.PortObservation,
			ImplSymbol:      req.ImplSymbol,
		})
		if !ok || rendered == "" {
			return nil, fmt.Errorf("characterize: no harness for language %q", req.Lang)
		}

		compiled := core.New(core.KindCode, string(body)).
			WithState(core.StateCompiled).
			WithLang(coreLang)
		suite := core.New(core.KindTestSuite, rendered).WithLang(coreLang)
		env := &core.RuleEnv{
			WorkDir:   ".",
			ImplPath:  absArtifact,
			TracePath: absTrace,
			ObjectID:  req.ObjectID,
		}
		// We intentionally ignore the verdict: a char run has no
		// pass/fail — assertions were stripped, and even a runtime error
		// in one probe is itself observed behavior (recorded as that
		// probe being unlocked downstream).
		if _, err := lang.TestRunInvoker(ctx, env, compiled, suite); err != nil {
			return nil, fmt.Errorf("characterize: harness run: %w", err)
		}

		raw, err := os.ReadFile(absTrace)
		if err != nil {
			// No trace file ⇒ nothing observable ran. Honest: every
			// probe becomes unlocked downstream rather than fabricated.
			return &core.RuntimeTrace{ObjectID: req.ObjectID}, nil
		}
		var tb traceBundle
		if err := json.Unmarshal(raw, &tb); err != nil {
			return nil, fmt.Errorf("characterize: parse trace bundle: %w", err)
		}
		rt := &core.RuntimeTrace{ObjectID: req.ObjectID}
		if tb.RuntimeTrace != nil {
			rt.Calls = tb.RuntimeTrace.Calls
		}
		return rt, nil
	}
}

// normalizeLang maps a free-form language string to the core.Lang and
// the harness's expected language token. Only harness-backed languages
// are valid for the MVP front stage.
func normalizeLang(s string) (core.Lang, string, bool) {
	switch s {
	case "python", "Python", "py":
		return core.LangPython, "Python", true
	case "javascript", "JavaScript", "js":
		return core.LangJavaScript, "JavaScript", true
	case "typescript", "TypeScript", "ts":
		return core.LangTypeScript, "TypeScript", true
	case "html", "HTML", "htm":
		return core.LangHTML, "HTML", true
	}
	return "", "", false
}
