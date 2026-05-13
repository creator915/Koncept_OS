package runtimetools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"github.com/creator915/Koncept_OS/internal/llm/transport"
	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
	"github.com/creator915/Koncept_OS/internal/runtime/playwright"
	"github.com/creator915/Koncept_OS/internal/typecalc/core"
)

func runtimeSmokeTool() toolcall.Tool {
	return toolcall.Tool{
		Concurrent: true, // each invocation spawns its own chromium ctx
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name: "runtime_smoke",
				Description: "Boot the HTML deliverable in headless Chromium (via Playwright) and capture runtime diagnostics — what the user would see when they open the file in a browser. ONLY applicable to graph objects whose impl resolves to a .html file. Reads `graph.Objects[<id>].Impl`, opens file://<absolute path>, waits for window.load, and reports:\n\n  • pageErrors      uncaught exceptions thrown at script-eval or window time\n  • consoleErrors   console.error() calls\n  • requestFailures any subresource that 4xx/5xx'd\n  • loadFired       whether the load event fired within timeout\n  • canvas          when <canvas> exists, whether ANY pixel is non-black\n\nRecords a kind=runtime evidence section on the object's bundle. The session_gate_check requires this evidence for HTML deliverables (rule [runtime-smoke-required]).\n\nPrerequisites: playwright + chromium installed under ~/.kcpos/cache/playwright/. If missing, the tool returns an error directing you to call `runtime_install` first (which downloads ~200MB chromium one-shot per machine — re-used across all projects).\n\nReturns a structured RuntimeSmoke<ok=true|false, ...diagnostics>.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object_id":   map[string]interface{}{"type": "string", "description": "Graph object id. The tool reads its impl path from K/graph.json and runs the smoke test against that file. Impl must end in .html."},
						"timeout_ms":  map[string]interface{}{"type": "integer", "description": "Max wait for window.load event. Default 5000. Increase for projects with heavy async initialization."},
						"viewport":    map[string]interface{}{"type": "object", "description": "Optional canvas viewport {width, height}. Default 800x600.", "properties": map[string]interface{}{"width": map[string]interface{}{"type": "integer"}, "height": map[string]interface{}{"type": "integer"}}},
						"screenshot":  map[string]interface{}{"type": "boolean", "description": "When true, save a debug PNG at .kcpos/typecalc/<id>.smoke.png. Default false."},
					},
					"required": []string{"object_id"},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			objectID, _ := args["object_id"].(string)
			if objectID == "" {
				return "", fmt.Errorf("object_id required")
			}
			g, err := persistence.LoadGraphOrInit(persistence.GraphDefaultPath)
			if err != nil {
				return "", err
			}
			obj, ok := g.Objects[objectID]
			if !ok {
				return "", fmt.Errorf("object %q not found in K/graph.json", objectID)
			}
			if obj.Impl == nil || *obj.Impl == "" {
				return "", fmt.Errorf("object %q has no impl path set — runtime_smoke needs an HTML deliverable", objectID)
			}
			implPath := *obj.Impl
			if !isHTML(implPath) {
				return "", fmt.Errorf("runtime_smoke: impl %q is not an HTML file (got extension %q). Use typecalc_test for non-HTML languages — runtime_smoke is strictly the browser smoke layer for HTML deliverables", implPath, filepath.Ext(implPath))
			}

			opts := playwright.SmokeOptions{}
			if v, ok := args["timeout_ms"].(float64); ok {
				opts.TimeoutMs = int(v)
			}
			if v, ok := args["viewport"].(map[string]interface{}); ok {
				if w, ok := v["width"].(float64); ok {
					opts.ViewportW = int(w)
				}
				if h, ok := v["height"].(float64); ok {
					opts.ViewportH = int(h)
				}
			}
			if v, ok := args["screenshot"].(bool); ok && v {
				opts.ScreenshotPath = filepath.Join(core.BundleDir, objectID+".smoke.png")
				_ = os.MkdirAll(core.BundleDir, 0o755)
			}

			section, err := playwright.RunPlaywrightSmoke(ctx, implPath, opts)
			if err != nil {
				return "", err
			}
			if err := core.SetRuntimeSmoke(objectID, section); err != nil {
				return "", fmt.Errorf("record runtime_smoke evidence: %w", err)
			}
			return renderSmokeResult(objectID, section), nil
		},
	}
}

func isHTML(p string) bool {
	lower := strings.ToLower(p)
	return strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".htm")
}

func renderSmokeResult(objectID string, s *core.RuntimeSmokeSection) string {
	var b strings.Builder
	mark := "FAIL"
	if s.OK {
		mark = "OK"
	}
	fmt.Fprintf(&b, "runtime_smoke %s: %s\n", objectID, mark)
	fmt.Fprintf(&b, "  loadFired=%v  loadDurationMs=%d\n", s.LoadFired, s.LoadDurationMs)
	if s.Canvas != nil {
		c := s.Canvas
		fmt.Fprintf(&b, "  canvas: found=%v size=%dx%d nonBlackPixels=%d ok=%v\n", c.Found, c.Width, c.Height, c.NonBlackPixels, c.OK)
	} else {
		fmt.Fprintf(&b, "  canvas: (none on page)\n")
	}
	if len(s.PageErrors) > 0 {
		fmt.Fprintf(&b, "  pageErrors:\n")
		for _, e := range s.PageErrors {
			fmt.Fprintf(&b, "    - %s\n", truncate(e.Message, 200))
		}
	}
	if len(s.ConsoleErrors) > 0 {
		fmt.Fprintf(&b, "  consoleErrors:\n")
		for _, e := range s.ConsoleErrors {
			fmt.Fprintf(&b, "    - %s (%s)\n", truncate(e.Message, 200), e.Location)
		}
	}
	if len(s.RequestFailures) > 0 {
		fmt.Fprintf(&b, "  requestFailures:\n")
		for _, r := range s.RequestFailures {
			fmt.Fprintf(&b, "    - %s — %s\n", r.URL, r.Failure)
		}
	}
	if s.ScreenshotPath != "" {
		fmt.Fprintf(&b, "  screenshot: %s\n", s.ScreenshotPath)
	}
	fmt.Fprintf(&b, "  evidence: %s\n", core.BundlePath(objectID))
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
