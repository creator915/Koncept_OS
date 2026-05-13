package playwright

// playwrightHarnessJS is the Node script kcpos spawns to drive headless
// Chromium against an HTML deliverable. It writes a single JSON object
// to stdout — the Go side parses that into a RuntimeSmokeSection.
//
// Substitution placeholders (replaced before exec):
//
//	__IMPL_URL__     — absolute file:// URL of the deliverable
//	__TIMEOUT_MS__   — load-event timeout (default 5000)
//	__VIEWPORT_W__   — viewport width
//	__VIEWPORT_H__   — viewport height
//	__SCREENSHOT__   — abs path to write a debug PNG ("" to skip)
//
// The script catches every failure mode (chromium unavailable,
// playwright not installed, navigation timeout, etc.) and emits a
// structured ok=false report rather than throwing — so the Go side can
// always parse stdout.
const playwrightHarnessJS = `// kcpos runtime_smoke harness
"use strict";

const IMPL_URL    = __IMPL_URL__;
const TIMEOUT_MS  = __TIMEOUT_MS__;
const VIEWPORT_W  = __VIEWPORT_W__;
const VIEWPORT_H  = __VIEWPORT_H__;
const SCREENSHOT  = __SCREENSHOT__;

async function main() {
  let playwright;
  try {
    playwright = require('playwright');
  } catch (e) {
    return emit({
      ok: false,
      loadFired: false,
      pageErrors: [{ message: 'playwright module not loadable: ' + e.message }],
      consoleErrors: [],
      requestFailures: [],
      canvas: null
    });
  }

  const startedAt = Date.now();
  let browser;
  try {
    browser = await playwright.chromium.launch({ headless: true });
  } catch (e) {
    return emit({
      ok: false,
      loadFired: false,
      pageErrors: [{ message: 'chromium launch failed: ' + e.message, stack: e.stack || '' }],
      consoleErrors: [],
      requestFailures: [],
      canvas: null
    });
  }

  const ctx = await browser.newContext({ viewport: { width: VIEWPORT_W, height: VIEWPORT_H } });
  const page = await ctx.newPage();

  const pageErrors = [];
  const consoleErrors = [];
  const requestFailures = [];

  page.on('pageerror', err => {
    pageErrors.push({
      message: err.message || String(err),
      stack: err.stack || '',
      source: 'pageerror'
    });
  });
  page.on('console', msg => {
    if (msg.type() === 'error') {
      const loc = msg.location();
      consoleErrors.push({
        message: msg.text(),
        location: loc ? (loc.url + ':' + loc.lineNumber + ':' + loc.columnNumber) : ''
      });
    }
  });
  page.on('requestfailed', req => {
    requestFailures.push({
      url: req.url(),
      failure: (req.failure() && req.failure().errorText) || 'unknown'
    });
  });

  let loadFired = false;
  let loadDurationMs = 0;
  try {
    const navStart = Date.now();
    await page.goto(IMPL_URL, { waitUntil: 'load', timeout: TIMEOUT_MS });
    loadFired = true;
    loadDurationMs = Date.now() - navStart;
  } catch (e) {
    pageErrors.push({
      message: 'navigation failed: ' + e.message,
      stack: e.stack || '',
      source: 'goto'
    });
  }

  // Give one frame for top-level scripts to settle (deferred starts).
  try {
    await page.waitForTimeout(200);
  } catch (_) {}

  let canvas = null;
  try {
    canvas = await page.evaluate(() => {
      const cs = document.getElementsByTagName('canvas');
      if (!cs || cs.length === 0) return { found: false };
      const c = cs[0];
      const w = c.width, h = c.height;
      let ctx;
      try { ctx = c.getContext('2d'); } catch (e) { ctx = null; }
      if (!ctx) return { found: true, width: w, height: h, nonBlackPixels: 0, ok: false };
      // Sample at most a 320x180 region to keep ImageData transfer cheap.
      const sw = Math.min(w, 320), sh = Math.min(h, 180);
      const sx = Math.max(0, (w - sw) >> 1), sy = Math.max(0, (h - sh) >> 1);
      let img;
      try { img = ctx.getImageData(sx, sy, sw, sh); } catch (e) { return { found: true, width: w, height: h, nonBlackPixels: 0, ok: false }; }
      let nz = 0;
      const d = img.data;
      for (let i = 0; i < d.length; i += 4) {
        if (d[i] > 5 || d[i+1] > 5 || d[i+2] > 5) nz++;
      }
      return { found: true, width: w, height: h, nonBlackPixels: nz, ok: nz > 0 };
    });
  } catch (e) {
    pageErrors.push({ message: 'canvas probe failed: ' + e.message, source: 'evaluate' });
  }

  if (SCREENSHOT) {
    try { await page.screenshot({ path: SCREENSHOT, fullPage: false }); } catch (_) {}
  }

  await browser.close();

  // OK rule: load fired AND no page errors. v9.3 — canvas pixel state
  // is ADVISORY only. Pre-v9.3 we failed OK when a canvas existed and
  // was all-black, but legitimate intro/paused/menu screens often render
  // a black canvas at load time, and the v92 batch (notably v92-04) had
  // working pages fail gate on this single check. The canvas struct is
  // still reported in the section so reviewers can see what the page
  // drew, but it no longer drives the pass/fail bit.
  let ok = loadFired && pageErrors.length === 0;

  emit({
    ok,
    loadFired,
    loadDurationMs,
    pageErrors,
    consoleErrors,
    requestFailures,
    canvas,
    playwrightVersion: (playwright && playwright._version) || ''
  });
}

function emit(obj) {
  process.stdout.write(JSON.stringify(obj) + '\n');
  process.exit(0);
}

main().catch(e => {
  emit({
    ok: false,
    loadFired: false,
    pageErrors: [{ message: 'harness crashed: ' + (e && e.message), stack: e && e.stack }],
    consoleErrors: [],
    requestFailures: [],
    canvas: null
  });
});
`
