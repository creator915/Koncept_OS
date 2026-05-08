// Package harness renders schema-driven test cases into language-specific
// runnable test code. The synthesizer (typecalc_synthesize_tests) emits
// structured TestCase data; the harness handles the test-runner
// scaffolding (test framework imports, trace logging, port snapshotting,
// assertion ordering) so the LLM doesn't write any of that and can't get
// it subtly wrong.
//
// Architectural intent (B-redesign): in earlier iterations the LLM wrote
// raw test code, including the appendTrace/JSON-write helper. Many
// failure modes (sparse traces, framework inconsistency, missing module
// exports) traced back to LLMs improvising trace logging. With the
// harness owning that flow, "appendTrace BEFORE assert" is enforced by
// construction rather than by prompt nagging.
package harness

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/creator915/Koncept_OS/internal/typecalc"
)

// RenderInputs bundles everything the harness needs at render time.
// portObservation is the per-port extractor map from graph.Object —
// the harness uses it to know HOW to read each port's value at runtime
// (D2 redesign). Without it, port reading is undefined.
type RenderInputs struct {
	Tests           *typecalc.TestsEvidence
	InputPorts      []string
	OutputPorts     []string
	ImplPath        string
	TracePath       string
	PortObservation map[string]string // port_name → extractor expression
}

// Render takes the synthesized TestsEvidence + I/O port lists +
// per-port extractor declarations and produces the runnable test
// source. Returns ("", false) when the language has no harness.
//
// implPath and tracePath MUST be absolute — the test runner subprocess
// runs in a scratch directory, so any relative path would resolve
// against the wrong root.
func Render(in RenderInputs) (string, bool) {
	if in.Tests == nil || len(in.Tests.Cases) == 0 {
		return "", false
	}
	switch in.Tests.Lang {
	case "JavaScript", "TypeScript", "HTML":
		return renderJavaScript(in), true
	}
	return "", false
}

// renderJavaScript inlines the case data and per-port extractor map as
// JSON literals into the harness template. The harness's snapshot()
// function reads each port via its declared extractor — no globalThis
// guessing.
func renderJavaScript(in RenderInputs) string {
	cases, _ := json.Marshal(in.Tests.Cases)
	inJSON, _ := json.Marshal(in.InputPorts)
	outJSON, _ := json.Marshal(in.OutputPorts)
	po := in.PortObservation
	if po == nil {
		po = map[string]string{}
	}
	poJSON, _ := json.Marshal(po)
	src := jsHarnessTemplate
	src = strings.ReplaceAll(src, "__OBJECT_ID__", jsString(in.Tests.ObjectID))
	src = strings.ReplaceAll(src, "__IMPL_PATH__", jsString(in.ImplPath))
	src = strings.ReplaceAll(src, "__TRACE_PATH__", jsString(in.TracePath))
	src = strings.ReplaceAll(src, "__CASES__", string(cases))
	src = strings.ReplaceAll(src, "__INPUT_PORTS__", string(inJSON))
	src = strings.ReplaceAll(src, "__OUTPUT_PORTS__", string(outJSON))
	src = strings.ReplaceAll(src, "__PORT_OBSERVATION__", string(poJSON))
	return src
}

// jsString returns the value as a JSON-encoded string literal — safe to
// substitute into the JS source as a string token.
func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// jsHarnessTemplate is the runnable test scaffold. It uses node:test +
// node:fs + node:assert (all built-in, no external deps). Module
// loading uses dynamic import so HTML+script bundles can be evaluated
// in a vm context if needed (TODO future).
//
// The fixed flow per case:
//   1. setup: assign globalThis[port] = value
//   2. snapshot inputs (before call)
//   3. invoke call
//   4. snapshot outputs (after call)
//   5. appendTrace(inputs, outputs)  ← BEFORE assertions, always
//   6. for each expectation: check & assert
//
// If step 6 fails the trace is already on disk. The runtime check sees
// the actual values regardless of test pass/fail.
const jsHarnessTemplate = `import { test } from 'node:test';
import assert from 'node:assert';
import fs from 'node:fs';
import path from 'node:path';

const OBJECT_ID = __OBJECT_ID__;
const IMPL_PATH = __IMPL_PATH__;
const TRACE_FILE = __TRACE_PATH__;
const CASES = __CASES__;
const INPUT_PORTS = __INPUT_PORTS__;
const OUTPUT_PORTS = __OUTPUT_PORTS__;
// PORT_OBSERVATION (D2): maps each port name to an extractor string
// declared on the graph object. The harness uses these to read port
// values without guessing about impl style. Recognized:
//   "global"          → globalThis[port]
//   "return.<path>"   → resolve <path> on the call's return value
//   "args.<n>.<path>" → resolve <path> on the n-th argument after the call
//   "side_effect"     → port has only externally-observable effects;
//                       the harness records the port as undefined and
//                       leaves verification to the waiver process
const PORT_OBSERVATION = __PORT_OBSERVATION__;

// Compute impl hash once at module load. Stored on every trace
// write so the static-check rule (evidence-stale) can detect when a
// trace is from a stale impl.
import crypto from 'node:crypto';
function computeImplHash() {
  try {
    const buf = fs.readFileSync(IMPL_PATH);
    return crypto.createHash('sha256').update(buf).digest('hex');
  } catch (_) { return ''; }
}
const IMPL_HASH = computeImplHash();

function loadTrace() {
  try {
    if (fs.existsSync(TRACE_FILE)) {
      return JSON.parse(fs.readFileSync(TRACE_FILE, 'utf-8'));
    }
  } catch (_) { /* fresh start */ }
  return { objectId: OBJECT_ID, implHash: IMPL_HASH, calls: [] };
}

function saveTrace(t) {
  fs.mkdirSync(path.dirname(TRACE_FILE), { recursive: true });
  fs.writeFileSync(TRACE_FILE, JSON.stringify(t, null, 2), 'utf-8');
}

function appendTrace(inputs, outputs) {
  const t = loadTrace();
  // Always refresh implHash on save — if a previous run left a stale
  // hash, this overwrites it with the current one.
  t.implHash = IMPL_HASH;
  t.calls.push({ inputs, outputs });
  saveTrace(t);
}

// resolvePath walks a dotted path on an object. Used to extract
// port values from a return value (e.g. "return.ball.x") or an
// argument (e.g. "args.0.score"). Missing segments yield undefined.
function resolvePath(obj, path) {
  if (obj == null) return undefined;
  if (!path) return obj;
  const segs = path.split('.');
  let cur = obj;
  for (const s of segs) {
    if (cur == null) return undefined;
    cur = cur[s];
  }
  return cur;
}

// snapshotPorts: read each port via its declared extractor.
//   - lastReturn   = the value of the most-recent call expression (if any)
//   - callArgs     = an array — preserved for "args.<n>.<path>" extractors
// If a port has no entry in PORT_OBSERVATION, default to "global"
// (legacy behaviour, but only kicks in when the graph forgot to
// declare; the gate's port-observation-required rule blocks confirm
// in that case).
function snapshotPorts(ports, lastReturn, callArgs) {
  const out = {};
  for (const p of ports) {
    const ex = PORT_OBSERVATION[p] || 'global';
    if (ex === 'side_effect') {
      out[p] = '__side_effect__';
      continue;
    }
    if (ex === 'global') {
      out[p] = globalThis[p];
      continue;
    }
    if (ex.startsWith('return.')) {
      out[p] = resolvePath(lastReturn, ex.slice(7));
      continue;
    }
    if (ex === 'return') {
      out[p] = lastReturn;
      continue;
    }
    if (ex.startsWith('args.')) {
      const rest = ex.slice(5);
      const dot = rest.indexOf('.');
      const idx = dot < 0 ? rest : rest.slice(0, dot);
      const path = dot < 0 ? '' : rest.slice(dot + 1);
      const arg = callArgs ? callArgs[Number(idx)] : undefined;
      out[p] = path ? resolvePath(arg, path) : arg;
      continue;
    }
    out[p] = undefined;
  }
  return out;
}

function checkExpectation(exp, value) {
  if (exp.equals !== undefined) {
    return [JSON.stringify(value) === JSON.stringify(exp.equals),
            'expected ' + JSON.stringify(exp.equals) + ', got ' + JSON.stringify(value)];
  }
  if (exp.between) {
    const [lo, hi] = exp.between;
    return [typeof value === 'number' && value >= lo && value <= hi,
            'expected in [' + lo + ',' + hi + '], got ' + value];
  }
  if (exp.type) {
    let t = typeof value;
    if (Array.isArray(value)) t = 'array';
    if (value === null) t = 'object';
    if (t === 'number' && Number.isInteger(value)) {
      // accept "number" too
      return [exp.type === 'number' || exp.type === 'integer',
              'expected type ' + exp.type + ', got integer'];
    }
    return [t === exp.type, 'expected type ' + exp.type + ', got ' + t];
  }
  if (exp.enum) {
    const enc = JSON.stringify(value);
    const ok = exp.enum.some(v => JSON.stringify(v) === enc);
    return [ok, 'expected one of ' + JSON.stringify(exp.enum) + ', got ' + enc];
  }
  if (exp.truthy !== undefined) {
    return [Boolean(value) === exp.truthy,
            'expected ' + (exp.truthy ? 'truthy' : 'falsy') + ', got ' + value];
  }
  return [true, ''];
}

// loadImpl reads the impl file and makes its exports / globals
// reachable to the cases. The result is exposed as globalThis.IMPL —
// a namespace object the case "call" expression can use as IMPL.fn(...).
//
// For .js / .mjs / .cjs / .ts: dynamic import returns the ESM
// namespace. We assign it to globalThis.IMPL.
//
// For .html: extract <script> bodies and eval into the global scope.
// We turn const/let into var so declarations leak to globalThis (best
// effort — vm.runInContext would be cleaner). After eval the impl's
// top-level functions are reachable as globals; we also build IMPL by
// scanning the script source for "function NAME" declarations.
async function loadImpl() {
  const ext = path.extname(IMPL_PATH).toLowerCase();
  if (ext === '.html' || ext === '.htm') {
    const html = fs.readFileSync(IMPL_PATH, 'utf-8');
    const re = /<script[^>]*>([\s\S]*?)<\/script>/gi;
    let m;
    let combined = '';
    while ((m = re.exec(html)) !== null) {
      combined += m[1] + '\n';
    }
    new Function(combined.replace(/\b(const|let)\b/g, 'var'))();
    // Scan for function declarations and bind them on IMPL too.
    const fnRe = /\bfunction\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*\(/g;
    const ns = {};
    let fm;
    while ((fm = fnRe.exec(combined)) !== null) {
      const name = fm[1];
      if (typeof globalThis[name] === 'function') ns[name] = globalThis[name];
    }
    globalThis.IMPL = ns;
    return;
  }
  if (ext === '.js' || ext === '.mjs' || ext === '.cjs' || ext === '.ts' || ext === '.tsx') {
    const mod = await import(path.resolve(IMPL_PATH));
    globalThis.IMPL = mod;
    return;
  }
  throw new Error('harness: unsupported impl extension: ' + ext);
}

// Run all cases.
test('kcpos-harness: load impl', async () => {
  await loadImpl();
});

for (const c of CASES) {
  test(c.name, async () => {
    // 1. setup
    if (c.setup) {
      for (const s of c.setup) globalThis[s.set] = s.value;
    }
    // 2. snapshot inputs (input ports always read via extractors —
    //    pre-call values come from globalThis or args[n] if applicable)
    const inputs = snapshotPorts(INPUT_PORTS, undefined, undefined);
    // 3. invoke. The case's "call" is a single JS expression. We wrap
    //    it in "return (...)" so the value flows to lastReturn.
    let callError = null;
    let lastReturn = undefined;
    try {
      const fn = new Function('IMPL', 'return (' + c.call + ');');
      lastReturn = fn(globalThis.IMPL);
    } catch (e) {
      callError = e;
    }
    // 4. snapshot outputs via the per-port extractors
    const outputs = snapshotPorts(OUTPUT_PORTS, lastReturn, undefined);
    // 5. trace BEFORE assertions (D2/B1: harness-enforced ordering)
    appendTrace(inputs, outputs);
    if (callError) throw callError;
    // 6. assertions
    for (const exp of (c.expect || [])) {
      const v = outputs[exp.port];
      const [pass, msg] = checkExpectation(exp, v);
      assert.ok(pass, '[' + exp.port + '] ' + msg);
    }
  });
}
`

// LegacyFallbackNotice is the message returned when callers ask for a
// harness on a language we don't yet support. Useful for explaining to
// the agent why the run is using TestCode instead of cases.
func LegacyFallbackNotice(lang string) string {
	return fmt.Sprintf("[harness] no harness for language %q; using legacy TestCode (LLM-written test source)", lang)
}
