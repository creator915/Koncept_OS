package fs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/creator915/Koncept_OS/internal/llm/toolcall"
	"github.com/creator915/Koncept_OS/internal/llm/transport"
)

// This file provides the COMMAND-LOCKED replacements for the general
// `bash` tool, used when a capability contract (e.g. CapsBlackbox)
// removes shell access. See docs/experiments/pb-kcpos-FORENSIC-2026-05-18.md
// §7.
//
// The mechanical point: `bash` lets the model run *any* string
// (curl/git/docker — the entire forensic cheat surface). These tools
// run a FIXED program; the model supplies only typed argv/stdin, never
// a command string. There is no shell, no pipeline, no substitution.
//
// Honest residual (documented, NOT closed here): `compile` runs the
// agent-written `compile.sh`, which is itself a shell. We scrub the
// network environment in-code (strip proxy vars, GOPROXY=off) as a
// best-effort containment so a `curl` smuggled into compile.sh fails by
// default — but the HARD wall for that residual is the host pf+uid
// egress deny (forensic §5 C-network), which is deployment-layer and
// out of this code's scope. `./probe` is a workspace file the agent
// could in principle rewrite via write_file; its integrity is a
// path-jail / harness-pinned concern, also documented as residual.

// scrubbedNetEnv returns os.Environ() with outbound-network knobs
// neutralised. Best-effort in-code containment of the compile.sh
// residual — NOT a security boundary (pf+uid is). Kept deliberately
// small and auditable.
func scrubbedNetEnv() []string {
	drop := map[string]bool{
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "FTP_PROXY": true,
		"http_proxy": true, "https_proxy": true, "all_proxy": true, "ftp_proxy": true,
		"GOPROXY": true, "GOFLAGS": true, "GONOSUMCHECK": true, "GOSUMDB": true,
		"GIT_HTTP_PROXY": true, "GIT_HTTPS_PROXY": true,
	}
	out := make([]string, 0, 64)
	for _, kv := range os.Environ() {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if drop[k] {
			continue
		}
		out = append(out, kv)
	}
	// Force offline module resolution: a submission that needs to fetch
	// dependencies SHOULD fail to build — that failure is the correct
	// anti-cheat signal, not a regression.
	out = append(out,
		"GOPROXY=off",
		"GOFLAGS=-mod=mod",
		"GOSUMDB=off",
		"GONOSUMCHECK=1",
		"NO_PROXY=*",
	)
	return out
}

func runFixed(ctx context.Context, name string, argv []string, stdin string, timeout time.Duration) string {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, argv...)
	cmd.Env = scrubbedNetEnv()
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	result := string(out)
	if cctx.Err() == context.DeadlineExceeded {
		return result + fmt.Sprintf("\n[timed out after %s]", timeout)
	}
	if err != nil {
		return result + fmt.Sprintf("\n[exit: %v]", err)
	}
	return result
}

// amd64Container returns the linux/amd64 build+run container the
// harness configured via KCPOS_AMD64_CONTAINER. Empty ⇒ run on the
// host (interactive / non-PB default — unchanged behavior).
//
// When set, compile/run_local route through `docker exec` into that
// container so the agent builds AND self-verifies on the SAME platform
// ProgramBench scores on. This closes the process defect documented in
// forensic §7.8: compile/run_local previously ran on the macOS host,
// so the agent produced & "verified" a macOS-only binary (kqueue) that
// scored 0/compile_failed on the linux/amd64 eval. Mechanism mirrors
// the existing `probe` tool (docker exec into an amd64 container).
func amd64Container() string { return strings.TrimSpace(os.Getenv("KCPOS_AMD64_CONTAINER")) }

func dockerRun(ctx context.Context, timeout time.Duration, stdin string, argv ...string) string {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "docker", argv...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	res := string(out)
	if cctx.Err() == context.DeadlineExceeded {
		return res + fmt.Sprintf("\n[timed out after %s]", timeout)
	}
	if err != nil {
		return res + fmt.Sprintf("\n[exit: %v]", err)
	}
	return res
}

// stringArgs extracts a []string from a JSON-decoded "args" value
// (which arrives as []interface{}). Non-string elements are stringified
// defensively; nil/absent yields no args.
func stringArgs(v interface{}) []string {
	raw, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		switch s := e.(type) {
		case string:
			out = append(out, s)
		default:
			out = append(out, fmt.Sprintf("%v", e))
		}
	}
	return out
}

// compileTool runs the agreed build entrypoint `compile.sh` in the
// current working directory. Command-locked: the model cannot choose
// the program or its arguments. Network env scrubbed (best-effort).
func compileTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name: "compile",
				Description: "Build the deliverable by running ./compile.sh ON THE linux/amd64 TARGET PLATFORM — the SAME platform your work is scored on, NOT this host. " +
					"You author compile.sh with write_file; this tool only RUNS it (you cannot pass a custom command). " +
					"Network is hard-disabled during the build; a build that needs to fetch dependencies fails by design. " +
					"If your code uses host/OS-specific APIs it will fail to build here exactly as it would when scored — fix it for linux. " +
					"Returns combined stdout+stderr. Timeout 120s.",
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			if _, err := os.Stat("compile.sh"); err != nil {
				return "", fmt.Errorf("compile.sh not found in working directory — write it first with write_file")
			}
			if c := amd64Container(); c != "" {
				// Build on the linux/amd64 eval platform (forensic §7.8 fix).
				// 1. sync host source tree into the container workspace
				// 2. run compile.sh there (container is --network none ⇒
				//    hard egress wall for compile.sh, not just env-scrub)
				// 3. copy the produced ./executable back to the host workdir
				cwd, _ := os.Getwd()
				var b strings.Builder
				b.WriteString("[built on linux/amd64 in " + c + " — host build/verify is NOT representative]\n")
				b.WriteString(dockerRun(ctx, 30*time.Second, "", "cp", cwd+"/.", c+":/workspace/"))
				b.WriteString(dockerRun(ctx, 120*time.Second, "", "exec", "-w", "/workspace", c, "bash", "compile.sh"))
				b.WriteString(dockerRun(ctx, 30*time.Second, "", "cp", c+":/workspace/executable", cwd+"/executable"))
				return b.String(), nil
			}
			return runFixed(ctx, "bash", []string{"compile.sh"}, "", 120*time.Second), nil
		},
	}
}

// runLocalTool executes the freshly-built ./executable with a typed
// argv array and optional stdin. No shell, no pipeline. This replaces
// every `./executable ...` / `echo ... | ./executable ...` bash use.
func runLocalTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name: "run_local",
				Description: "Run the built ./executable ON THE linux/amd64 TARGET PLATFORM with the given argv and optional stdin. " +
					"No shell — pass arguments as a list, not a command string. This runs the SAME platform binary that gets scored, so its behavior here is representative. " +
					"Returns combined stdout+stderr (and [exit:] on non-zero). Timeout 30s.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"args":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "argv passed to ./executable"},
						"stdin": map[string]interface{}{"type": "string", "description": "optional stdin fed to the program"},
					},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			stdin, _ := args["stdin"].(string)
			if c := amd64Container(); c != "" {
				// Execute the linux/amd64 binary inside the container —
				// the host cannot run a linux binary; this is the only
				// representative self-verification (mirrors `probe`).
				argv := append([]string{"exec", "-i", "-w", "/workspace", c, "/workspace/executable"}, stringArgs(args["args"])...)
				return dockerRun(ctx, 30*time.Second, stdin, argv...), nil
			}
			if _, err := os.Stat("executable"); err != nil {
				return "", fmt.Errorf("./executable not found — run `compile` first")
			}
			return runFixed(ctx, "./executable", stringArgs(args["args"]), stdin, 30*time.Second), nil
		},
	}
}

// probeTool is the SANCTIONED reference channel. It runs the
// harness-provided ./probe with a typed argv array + optional stdin —
// the only way to observe the black box. No shell: the model cannot
// `docker exec strings/readelf` or overwrite the reference, because it
// has no command string and no docker tool.
func probeTool() toolcall.Tool {
	return toolcall.Tool{
		Spec: transport.ToolSpec{
			Type: "function",
			Function: transport.ToolFunction{
				Name: "probe",
				Description: "Observe the black-box reference by running ./probe with the given argv and optional stdin. " +
					"This is the ONLY sanctioned way to inspect the reference (no shell, no docker). Returns combined stdout+stderr. Timeout 15s.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"args":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "argv passed to ./probe"},
						"stdin": map[string]interface{}{"type": "string", "description": "optional stdin fed to ./probe"},
					},
				},
			},
		},
		Run: func(ctx context.Context, args map[string]interface{}) (string, error) {
			if _, err := os.Stat("probe"); err != nil {
				return "", fmt.Errorf("./probe not found in working directory")
			}
			stdin, _ := args["stdin"].(string)
			return runFixed(ctx, "./probe", stringArgs(args["args"]), stdin, 15*time.Second), nil
		},
	}
}
