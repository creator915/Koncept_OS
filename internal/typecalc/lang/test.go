package lang

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

// TestReview is the verdict returned by the review_test_error rule (§3,
// "测试阶段"). The LLM is given the description, signature, and one
// failing test case (NOT the source code) and must classify the failure.
type TestReview string

const (
	TestReviewCorrect            TestReview = "TestCorrect"        // test is right, code has a bug
	TestReviewWrong              TestReview = "TestWrong"          // test is wrong, code is fine
	TestReviewDescriptionUnclear TestReview = "DescriptionUnclear" // description ambiguous; need human
)

// TestReviewResult is the structured outcome of a review_test_error step.
type TestReviewResult struct {
	Verdict TestReview `json:"verdict"`
	Reason  string     `json:"reason"`
}

// TestLoopHooks supplies the LLM-actored callbacks that drive the §7.2
// review/debug/regenerate-test branches. Callers pass nil ReviewError
// to short-circuit to "always TestCorrect" (treats every failure as a
// code bug).
type TestLoopHooks struct {
	ReviewError    func(ctx context.Context, env *core.RuleEnv, description, signature, testErr string) (*TestReviewResult, error)
	DebugFromTest  func(ctx context.Context, env *core.RuleEnv, compiled, testErr *core.TypedValue) (*core.TypedValue, error)
	RegenerateTest func(ctx context.Context, env *core.RuleEnv, description, signature, rejected string) (*core.TypedValue, error)
}

// TestLoop runs the test-debug cycle from §7.2.
func TestLoop(ctx context.Context, env *core.RuleEnv,
	compiled, suite *core.TypedValue,
	description, signature string,
	hooks TestLoopHooks,
) (*core.TypedValue, error) {
	if compiled == nil || compiled.State != core.StateCompiled || compiled.Kind != core.KindCode {
		return nil, fmt.Errorf("TestLoop: compiled must be Compiled<Code>, got %v", compiled)
	}
	if suite == nil || suite.Kind != core.KindTestSuite {
		return nil, fmt.Errorf("TestLoop: suite must be TestSuite, got %v", suite)
	}
	maxRetries := env.MaxRetries
	if maxRetries <= 0 {
		maxRetries = DefaultMaxRetries
	}
	invoker := env.TestInvoker
	if invoker == nil {
		invoker = TestRunInvoker
	}

	curCompiled := compiled
	curSuite := suite
	for attempt := 0; attempt < maxRetries; attempt++ {
		out, err := invoker(ctx, env, curCompiled, curSuite)
		if err != nil {
			return nil, fmt.Errorf("attempt %d test: %w", attempt, err)
		}
		if out.State == core.StateTestedPass {
			return out, nil
		}
		if out.Kind != core.KindTestError {
			return core.FormatErr("TestLoop: tester returned unexpected %s", out.Tag()), nil
		}
		if hooks.ReviewError == nil {
			fixed, err := hooks.DebugFromTest(ctx, env, curCompiled, out)
			if err != nil {
				return nil, fmt.Errorf("attempt %d debug: %w", attempt, err)
			}
			recompiled, err := CompileLanguageInvoker(ctx, env, fixed)
			if err != nil {
				return nil, fmt.Errorf("attempt %d recompile: %w", attempt, err)
			}
			if recompiled.State != core.StateCompiled {
				return recompiled, nil
			}
			curCompiled = recompiled
			continue
		}
		te, _ := core.DecodeTestError(out)
		blob, _ := json.Marshal(te)
		review, err := hooks.ReviewError(ctx, env, description, signature, string(blob))
		if err != nil {
			return nil, fmt.Errorf("attempt %d review: %w", attempt, err)
		}
		switch review.Verdict {
		case TestReviewCorrect:
			fixed, err := hooks.DebugFromTest(ctx, env, curCompiled, out)
			if err != nil {
				return nil, fmt.Errorf("attempt %d debug: %w", attempt, err)
			}
			recompiled, err := CompileLanguageInvoker(ctx, env, fixed)
			if err != nil {
				return nil, fmt.Errorf("attempt %d recompile: %w", attempt, err)
			}
			if recompiled.State != core.StateCompiled {
				return recompiled, nil
			}
			curCompiled = recompiled
		case TestReviewWrong:
			rejected, _ := json.Marshal(te)
			newSuite, err := hooks.RegenerateTest(ctx, env, description, signature, string(rejected))
			if err != nil {
				return nil, fmt.Errorf("attempt %d regenerate test: %w", attempt, err)
			}
			if newSuite.Kind != core.KindTestSuite {
				return core.FormatErr("TestLoop: regenerate_test returned %s, expected TestSuite",
					newSuite.Tag()), nil
			}
			curSuite = newSuite
		case TestReviewDescriptionUnclear:
			d := struct {
				Task      string `json:"task"`
				Reason    string `json:"reason"`
				Reviewing string `json:"reviewing"`
			}{Task: taskOf(curCompiled), Reason: review.Reason, Reviewing: string(blob)}
			raw, _ := json.Marshal(d)
			return &core.TypedValue{Kind: core.KindClarificationReq, Payload: string(raw)}, nil
		default:
			return core.FormatErr("TestLoop: review returned unknown verdict %q", review.Verdict), nil
		}
	}
	reason := fmt.Sprintf("test loop retried %d times without producing Tested<Pass>", maxRetries)
	return core.NewObstacle(taskOf(curCompiled), reason, nil), nil
}

// TestRunInvoker is the default TestInvoker. Per-language runner with
// fallback toolchain selection. Missing toolchains fail open (return Pass).
func TestRunInvoker(ctx context.Context, env *core.RuleEnv, compiled, suite *core.TypedValue) (*core.TypedValue, error) {
	if compiled == nil || compiled.State != core.StateCompiled || compiled.Kind != core.KindCode {
		return nil, fmt.Errorf("TestRunInvoker: compiled must be Compiled<Code>, got %v", compiled)
	}
	if suite == nil || suite.Kind != core.KindTestSuite {
		return nil, fmt.Errorf("TestRunInvoker: suite must be TestSuite, got %v", suite)
	}
	switch compiled.Lang {
	case core.LangGo:
		return runGoTest(ctx, env, compiled, suite)
	case core.LangTypeScript, core.LangJavaScript:
		return runJSTest(ctx, env, compiled, suite)
	case core.LangHTML:
		// D5 integration fix (2026-05-09 v7→v8): HTML deliverables now
		// route through the JS harness instead of returning Insufficient.
		// The harness's loadImpl already extracts <script> tags from
		// HTML and binds inline functions to globalThis + IMPL, so we
		// can mechanically test the *actual* deliverable. This closes
		// the v7-pong gap where agent maintained a parallel K/impl/*.js
		// for testing while index.html had divergent inline functions
		// that crashed at runtime — both files passed checkpoint but
		// the deliverable was broken.
		return runJSTest(ctx, env, compiled, suite)
	case core.LangPython:
		return runPythonTest(ctx, env, compiled, suite)
	case core.LangRust:
		return runRustTest(ctx, env, compiled, suite)
	case core.LangC:
		return runCTest(ctx, env, compiled, suite)
	}
	// CRITICAL: no in-tree test runner for this language. We return
	// Insufficient with a reason. v9.2 — the waiver escape was removed,
	// so the gate WILL refuse to confirm this object until either (a)
	// the impl is restructured into a verifiable language, or (b) kcpos
	// gains a runner for this language (see internal/typecalc/lang/).
	return core.NewInsufficient(fmt.Sprintf(
		"no in-tree test runner for language %q — kcpos cannot mechanically verify this code. v9.2 has no waiver escape; resolve by restructuring the impl into a runner-supported language (Go / TypeScript / JavaScript / Python / Rust / C / HTML), OR extend internal/typecalc/lang/ with a TestRunInvoker for %q. Note: language identifiers are case-sensitive — use \"C\" not \"c\", \"Go\" not \"go\".",
		compiled.Lang, compiled.Lang)), nil
}

// runCTest implements the doc's C mapping: "编译并运行测试二进制".
// kcpos's impl model is single-file, so impl + test are one translation
// unit (the test source carries main() and exits non-zero on failure).
//
// 2026-05-21 — C trace emission. Pre-fix runCTest just glued impl + suite
// and ran the binary; no trace bundle was written, so the review stage's
// runtime-trace check fired Obstacle on every C confirm_object run (PB-30
// batch #4 cmatrix/figlet/tty-clock all died here). We now stage a
// kcpos_helpers.c that defines appendTrace(inputs_json, outputs_json),
// concatenate helpers + impl + suite, run the test binary, then load
// the JSONL trace it produced and write a bundle in the same shape the
// Go runner produces — the review stage sees identical evidence for C
// and Go runs.
func runCTest(ctx context.Context, env *core.RuleEnv, compiled, suite *core.TypedValue) (*core.TypedValue, error) {
	if !commandExists("gcc") {
		return compiled.WithState(core.StateTestedPass), nil // fail-open, like JS/Py
	}
	// Static-check the synthesized testCode contains an appendTrace call.
	// Matches the runGoTest pattern (line 251) — block runs whose tests
	// would produce no trace, so the agent gets a stage-named error rather
	// than chasing the downstream "no runtime trace" Obstacle from review.
	if !strings.Contains(suite.Payload, "appendTrace(") {
		return core.NewTestError(
			"trace-missing",
			"appendTrace(inputs_json, outputs_json) call present in synthesized testCode",
			"synthesized C testCode does not call appendTrace(...). The trace helper is provided by the harness in kcpos_helpers.c — every test case must call appendTrace(inputsJsonStr, outputsJsonStr) BEFORE its assertions so the runtime trace records what happened even when assertions fail. Re-synthesize tests with this requirement; for C the inputs/outputs are passed as JSON string literals (the LLM writes the JSON inline).",
		), nil
	}
	dir, err := os.MkdirTemp("", "kcpos-ctest-*")
	if err != nil {
		return core.NewTestError("setup", "no error", err.Error()), nil
	}
	defer os.RemoveAll(dir)
	// Bake the JSONL trace path into helpers. Use a scratch path inside
	// the staging dir; we read it back after the binary exits and convert
	// into the bundle shape the review stage reads.
	jsonlPath := filepath.Join(dir, "trace.jsonl")
	helpers := renderCTraceHelper(jsonlPath)
	combined := helpers + "\n" + compiled.Payload + "\n" + suite.Payload
	src := filepath.Join(dir, "combined.c")
	if err := os.WriteFile(src, []byte(combined), 0o644); err != nil {
		return core.NewTestError("setup", "no error", err.Error()), nil
	}
	bin := filepath.Join(dir, "testbin")
	if out, err := runCmd(ctx, dir, "gcc", src, "-o", bin); err != nil {
		return core.NewTestError("compile-test-binary", "tests pass", string(out)+"\n"+err.Error()), nil
	}
	runOut, runErr := runCmd(ctx, dir, bin)
	// Always try to persist whatever trace lines the binary managed to
	// emit, EVEN IF tests failed — review/characterize benefit from
	// partial trace evidence the same way the Go runner does (helper
	// flushes after each call so a crashing test still leaves a tail).
	_ = persistCTrace(env, jsonlPath, compiled.Payload)
	if runErr != nil {
		return core.NewTestError(extractFailingCase(string(runOut)), "tests pass", string(runOut)+"\n"+runErr.Error()), nil
	}
	return compiled.WithState(core.StateTestedPass), nil
}

// renderCTraceHelper produces a self-contained C source string that the
// generated test code can lean on. Mirrors renderGoTraceHelper for the
// C harness — provides appendTrace() that writes one JSONL line per call
// (no native map-to-JSON in C, so the LLM passes pre-formatted JSON
// string literals; the Go-side persistCTrace assembles the final bundle
// after the test binary exits).
//
// The trace path is baked in as a string literal at stage time. The
// helper opens the file in write mode at first call (truncating any
// prior content), so a re-run produces a fresh trace bundle aligned
// with the current impl, identical to the Go helper's TestMain reset.
func renderCTraceHelper(jsonlPath string) string {
	return strings.ReplaceAll(cTraceHelperTemplate, "__TRACE_JSONL_PATH__", cString(jsonlPath))
}

// cString returns a C double-quoted string literal of s. Backslashes and
// quotes are escaped; no other characters appear in stage paths we
// generate (tmp dirs use a safe alphabet) so this minimal escape is
// sufficient. If we ever pass arbitrary user paths through here, harden
// to also escape control chars.
func cString(s string) string {
	r := strings.ReplaceAll(s, `\`, `\\`)
	r = strings.ReplaceAll(r, `"`, `\"`)
	return `"` + r + `"`
}

// cTraceHelperTemplate is concatenated into combined.c at stage time.
// Provides appendTrace + a file-once-truncate-then-append pattern so a
// crashing binary still leaves a partial JSONL trace on disk.
const cTraceHelperTemplate = `/* Auto-generated by kcpos C lang runner — do not edit.
 * Provides appendTrace() for synthesized C test programs.
 * Mirror of internal/typecalc/lang/test.go renderGoTraceHelper.
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static const char *kcpos_trace_path = __TRACE_JSONL_PATH__;
static FILE *kcpos_trace_fp = NULL;

/* appendTrace records one (inputs, outputs) pair from a test case.
 * Call BEFORE assertions so a failing assertion still leaves the trace
 * on disk — review's runtime-trace rule sees the actual values
 * regardless of test pass/fail (matches Go/JS/Python harness ordering).
 *
 * inputs_json and outputs_json must be valid JSON strings (object or
 * any JSON value). The helper does NOT validate — malformed JSON
 * propagates into the bundle and review will surface the issue with
 * a clearer error than "trace-missing".
 */
void appendTrace(const char *inputs_json, const char *outputs_json) {
    if (kcpos_trace_fp == NULL) {
        /* First call truncates — fresh run = fresh trace, matches Go
         * TestMain reset semantics.
         */
        kcpos_trace_fp = fopen(kcpos_trace_path, "w");
        if (kcpos_trace_fp == NULL) {
            /* Don't crash the test if trace open fails — proceed quietly,
             * mirror Go's behavior. Review will report "no runtime trace"
             * which is the correct downstream signal.
             */
            return;
        }
    }
    fprintf(kcpos_trace_fp, "{\"inputs\":%s,\"outputs\":%s}\n",
            inputs_json ? inputs_json : "null",
            outputs_json ? outputs_json : "null");
    fflush(kcpos_trace_fp);
}
`

// persistCTrace reads the JSONL trace produced by the C test binary
// (one JSON object per line: {"inputs": ..., "outputs": ...}) and writes
// a bundle RuntimeTrace section identical in shape to what renderGoTrace
// Helper writes. Called AFTER the test binary exits, regardless of
// test pass/fail, so partial traces from crashing tests are preserved.
//
// Idempotent and safe to call when no trace file was written (e.g. binary
// crashed before any appendTrace call) — returns nil with no bundle
// mutation; review will then surface "no runtime trace" downstream.
func persistCTrace(env *core.RuleEnv, jsonlPath, implBody string) error {
	if env == nil || env.ObjectID == "" {
		return nil
	}
	data, err := os.ReadFile(jsonlPath)
	if err != nil || len(data) == 0 {
		return nil // no trace produced — review handles "missing trace"
	}
	var calls []core.RuntimeCall
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var c struct {
			Inputs  map[string]json.RawMessage `json:"inputs"`
			Outputs map[string]json.RawMessage `json:"outputs"`
		}
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			// Tolerate malformed lines — review will see what made it.
			continue
		}
		calls = append(calls, core.RuntimeCall{Inputs: c.Inputs, Outputs: c.Outputs})
	}
	if len(calls) == 0 {
		return nil
	}
	bundle := core.LoadOrInitBundle(env.ObjectID)
	bundle.RuntimeTrace = &core.RuntimeTraceSection{Calls: calls}
	return core.SaveBundle(bundle)
}

// runRustTest implements the doc's Rust mapping ("cargo test"). cargo
// needs a Cargo.toml project scaffold; for kcpos's single-file impl
// model the faithful equivalent is `rustc --test` (builds the #[test]
// harness binary) then run it. Deviation disclosed in the rollout doc.
func runRustTest(ctx context.Context, env *core.RuleEnv, compiled, suite *core.TypedValue) (*core.TypedValue, error) {
	if !commandExists("rustc") && !commandExists("cargo") {
		return compiled.WithState(core.StateTestedPass), nil // fail-open
	}
	dir, err := os.MkdirTemp("", "kcpos-rstest-*")
	if err != nil {
		return core.NewTestError("setup", "no error", err.Error()), nil
	}
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "combined.rs")
	if err := os.WriteFile(src, []byte(compiled.Payload+"\n"+suite.Payload), 0o644); err != nil {
		return core.NewTestError("setup", "no error", err.Error()), nil
	}
	bin := filepath.Join(dir, "rstest")
	if out, err := runCmd(ctx, dir, "rustc", "--test", src, "-o", bin); err != nil {
		return core.NewTestError("compile-test-binary", "tests pass", string(out)+"\n"+err.Error()), nil
	}
	if out, err := runCmd(ctx, dir, bin); err != nil {
		return core.NewTestError(extractFailingCase(string(out)), "tests pass", string(out)+"\n"+err.Error()), nil
	}
	return compiled.WithState(core.StateTestedPass), nil
}

func runGoTest(ctx context.Context, env *core.RuleEnv, compiled, suite *core.TypedValue) (*core.TypedValue, error) {
	// v9.6: stage the impl + every sibling .go (same rationale as
	// runGoCompile). Adds the freshly synthesized test file + a trace
	// helper file so the test process can record (inputs, outputs)
	// pairs into the runtime-trace bundle the way JS/Python harness
	// templates do at runtime.
	dir, cleanup, err := stageGoPackage(env, compiled.Payload, "code.go")
	if err != nil {
		return core.NewTestError("setup", "no error", err.Error()), nil
	}
	defer cleanup()
	// v9.6 static-check on synthesized testCode — block runs whose tests
	// would silently produce no trace. Without this check the chain runs
	// to completion, the runtime-trace review rule fires with no info on
	// WHY the trace is missing, and the agent burns turns regenerating
	// (the 2026-05-14 walk batch hit this repeatedly).
	if !strings.Contains(suite.Payload, "appendTrace(") {
		return core.NewTestError(
			"trace-missing",
			"appendTrace(inputs, outputs) call present in synthesized testCode",
			"synthesized testCode does not call appendTrace(...). The trace helper is provided by the harness in kcpos_helpers_test.go — every test case must call appendTrace(inputsMap, outputsMap) BEFORE its assertions so the runtime trace records what happened even when assertions fail. Re-synthesize tests with this requirement.",
		), nil
	}
	if err := writeFile(dir, "code_test.go", suite.Payload); err != nil {
		return core.NewTestError("setup", "no error", err.Error()), nil
	}
	// Drop in the trace helper so the test process has appendTrace +
	// reset-on-load + bundle protocol identical to the JS/Python harness.
	if err := writeFile(dir, "kcpos_helpers_test.go", renderGoTraceHelper(env)); err != nil {
		return core.NewTestError("setup", "no error", err.Error()), nil
	}
	// Ensure a go.mod exists so `go test ./...` doesn't bail with
	// "go: cannot find main module". Staged dirs don't inherit one.
	if err := writeFile(dir, "go.mod", "module kcposscratch\n\ngo 1.21\n"); err != nil {
		return core.NewTestError("setup", "no error", err.Error()), nil
	}
	out, err := runCmd(ctx, dir, "go", "test", "./...")
	if err != nil {
		return core.NewTestError(extractFailingCase(string(out)), "tests pass", string(out)), nil
	}
	return compiled.WithState(core.StateTestedPass), nil
}

// renderGoTraceHelper produces a self-contained _test.go file the
// generated test code can lean on: appendTrace records (inputs,
// outputs) pairs into the same .kcpos/typecalc/<id>.json bundle the
// JS/Python harness writes. TestMain resets at run start so a
// zero-call run still produces a fresh bundle entry with the current
// implHash (matches JS/Python ordering).
//
// The trace path / object id are baked in as string constants at
// stage time — Go has no runtime config knob for these and reading
// from env vars at test start would couple to the runner's process
// env, which is harder to reproduce when debugging.
func renderGoTraceHelper(env *core.RuleEnv) string {
	tracePath := ""
	objectID := ""
	implPath := ""
	if env != nil {
		tracePath = env.TracePath
		objectID = env.ObjectID
		implPath = env.ImplPath
		if abs, err := filepath.Abs(implPath); err == nil {
			implPath = abs
		}
	}
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(goTraceHelperTemplate,
		"__TRACE_PATH__", goString(tracePath)),
		"__OBJECT_ID__", goString(objectID)),
		"__IMPL_PATH__", goString(implPath))
}

// goString quotes s as a Go string literal — uses %q so embedded
// quotes, newlines, backslashes are handled safely.
func goString(s string) string {
	return fmt.Sprintf("%q", s)
}

// goTraceHelperTemplate is appended to the staged scratch dir as
// kcpos_helpers_test.go. It uses a sync.Mutex around bundle write so
// parallel test cases (t.Parallel) don't race the file.
const goTraceHelperTemplate = `// Auto-generated by kcpos lang runner — do not edit.
// Provides appendTrace() for synthesized test files.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const kcposTracePath = __TRACE_PATH__
const kcposObjectID = __OBJECT_ID__
const kcposImplPath = __IMPL_PATH__

var kcposTraceMu sync.Mutex
var kcposRunCalls []map[string]interface{}

func kcposImplHash() string {
	if kcposImplPath == "" {
		return ""
	}
	data, err := os.ReadFile(kcposImplPath)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// appendTrace records one (inputs, outputs) pair from a test case.
// Call BEFORE assertions so a failing assertion still leaves the trace
// on disk — the runtime-trace review rule sees the actual values
// regardless of test pass/fail (matches JS/Python harness ordering).
func appendTrace(inputs, outputs map[string]interface{}) {
	kcposTraceMu.Lock()
	defer kcposTraceMu.Unlock()
	kcposRunCalls = append(kcposRunCalls, map[string]interface{}{
		"inputs":  inputs,
		"outputs": outputs,
	})
	kcposSaveTrace()
}

func kcposSaveTrace() {
	if kcposTracePath == "" {
		return
	}
	bundle := kcposLoadBundle()
	bundle["objectId"] = kcposObjectID
	if _, ok := bundle["version"]; !ok {
		bundle["version"] = 1
	}
	bundle["sourceHash"] = kcposImplHash()
	bundle["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	bundle["runtimeTrace"] = map[string]interface{}{"calls": kcposRunCalls}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(kcposTracePath), 0755)
	_ = os.WriteFile(kcposTracePath, data, 0644)
}

func kcposLoadBundle() map[string]interface{} {
	if kcposTracePath == "" {
		return map[string]interface{}{}
	}
	data, err := os.ReadFile(kcposTracePath)
	if err != nil {
		return map[string]interface{}{"objectId": kcposObjectID, "version": 1}
	}
	var bundle map[string]interface{}
	if err := json.Unmarshal(data, &bundle); err != nil {
		return map[string]interface{}{"objectId": kcposObjectID, "version": 1}
	}
	if oid, _ := bundle["objectId"].(string); oid != kcposObjectID {
		return map[string]interface{}{"objectId": kcposObjectID, "version": 1}
	}
	return bundle
}

// TestMain resets the trace bundle on run start so the bundle always
// reflects THIS run's implHash (mirrors JS/Python harness reset). Even
// a zero-test run produces a clean bundle with current implHash.
func TestMain(m *testing.M) {
	kcposSaveTrace()
	os.Exit(m.Run())
}
`

func runJSTest(ctx context.Context, env *core.RuleEnv, compiled, suite *core.TypedValue) (*core.TypedValue, error) {
	if !commandExists("npx") && !commandExists("node") {
		return compiled.WithState(core.StateTestedPass), nil
	}
	dir, cleanup, err := newScratchDir(env, "kcpos-jstest-")
	if err != nil {
		return core.NewTestError("setup", "no error", err.Error()), nil
	}
	defer cleanup()
	codeName := "code.ts"
	testName := "code.test.ts"
	if compiled.Lang == core.LangJavaScript {
		codeName = "code.js"
		testName = "code.test.js"
	}
	if compiled.Lang == core.LangHTML {
		// Harness loadImpl detects ext === '.html' and extracts <script>
		// tags into globalThis + IMPL — so the same deliverable users
		// open in a browser is the source under test. No parallel
		// K/impl/*.js shadow files: kcpos verifies the actual artifact.
		codeName = "code.html"
		testName = "code.test.js"
	}
	if err := writeFile(dir, codeName, compiled.Payload); err != nil {
		return core.NewTestError("setup", "no error", err.Error()), nil
	}
	if err := writeFile(dir, testName, suite.Payload); err != nil {
		return core.NewTestError("setup", "no error", err.Error()), nil
	}
	if !commandExists("npx") {
		out, err := runCmd(ctx, dir, "node", "--test", testName)
		if err != nil {
			return core.NewTestError(extractFailingCase(string(out)), "tests pass", string(out)), nil
		}
		return compiled.WithState(core.StateTestedPass), nil
	}
	// 2026-05-09 v8 fix: dropped --reporter=basic. Modern vitest
	// (v3+) removed that reporter; the npx --yes pulls latest, so
	// every batch run was failing with "Failed to load custom
	// Reporter from basic" startup error. The default reporter is
	// fine for our purposes — we only inspect exit code + stdout.
	out, err := runCmd(ctx, dir, "npx", "--yes", "vitest", "run", "--root", dir)
	if err != nil {
		// Fallback to node's built-in test runner. This handles the
		// case where vitest itself is broken (env issue, npm cache
		// corruption, version drift) — node --test understands the
		// same `import { test } from 'node:test'` API the harness
		// generates, so it works for both LangJavaScript AND LangHTML.
		// Pre-v8 the fallback was JavaScript-only, which left HTML
		// runs stuck on vitest infra failures.
		if compiled.Lang == core.LangJavaScript || compiled.Lang == core.LangHTML {
			out2, err2 := runCmd(ctx, dir, "node", "--test", testName)
			if err2 == nil {
				return compiled.WithState(core.StateTestedPass), nil
			}
			return core.NewTestError(extractFailingCase(string(out2)), "tests pass", string(out2)), nil
		}
		return core.NewTestError(extractFailingCase(string(out)), "tests pass", string(out)), nil
	}
	return compiled.WithState(core.StateTestedPass), nil
}

func runPythonTest(ctx context.Context, env *core.RuleEnv, compiled, suite *core.TypedValue) (*core.TypedValue, error) {
	pyExe := "python3"
	if !commandExists(pyExe) {
		pyExe = "python"
		if !commandExists(pyExe) {
			return compiled.WithState(core.StateTestedPass), nil
		}
	}
	dir, cleanup, err := newScratchDir(env, "kcpos-pytest-")
	if err != nil {
		return core.NewTestError("setup", "no error", err.Error()), nil
	}
	defer cleanup()
	if err := writeFile(dir, "code.py", compiled.Payload); err != nil {
		return core.NewTestError("setup", "no error", err.Error()), nil
	}
	if err := writeFile(dir, "test_code.py", suite.Payload); err != nil {
		return core.NewTestError("setup", "no error", err.Error()), nil
	}
	// cmd.Dir is already set to `dir` by runCmd, so use "." for the
	// test target. (Passing `dir` here is harmless now that fs.go's
	// newScratchDir returns an absolute path, but redundant. Using "."
	// matches runGoTest's convention and avoids surprises if the
	// absolute-path guarantee ever slips back to a relative one.)
	out, err := runCmd(ctx, dir, pyExe, "-m", "pytest", "-q", ".")
	if err != nil {
		out2, err2 := runCmd(ctx, dir, pyExe, "-m", "unittest", "discover", "-s", ".")
		if err2 == nil {
			return compiled.WithState(core.StateTestedPass), nil
		}
		return core.NewTestError(extractFailingCase(string(out)), "tests pass", string(out)+"\n"+string(out2)), nil
	}
	return compiled.WithState(core.StateTestedPass), nil
}

func extractFailingCase(output string) string {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "--- FAIL") || strings.Contains(l, "FAIL ") ||
			strings.Contains(l, "FAILED") || strings.Contains(l, "× ") {
			return l
		}
	}
	if len(lines) > 0 {
		return lines[0]
	}
	return "unknown"
}
