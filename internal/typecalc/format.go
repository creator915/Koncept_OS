package typecalc

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// FormatChecker validates that a payload conforms to the structural
// expectations of its type tag. §4.4 of the doc: the format checker
// verifies syntax/shape; semantic validity ("does the test actually pass")
// is the testers' / compilers' job.
//
// A nil return means OK. A non-nil return is a string explaining the
// format violation; the caller wraps it in a KindFormatError TypedValue.
type FormatChecker func(payload string) error

// formatCheckTimeout caps a single language-toolchain invocation. Keep
// this short — format checking runs on every router dispatch and must
// not become a bottleneck. The strict implementations call out to real
// parsers (rustc, javac, node, python, esbuild) and we don't want a
// stalled toolchain to stall the agent loop.
const formatCheckTimeout = 4 * time.Second

// CheckFormat dispatches on the typed value's tag. Returns nil if the
// payload's format matches expectations, or a *TypedValue of Kind
// KindFormatError otherwise. Unknown tags are treated as "no check
// available" — we don't fail closed here because plenty of content kinds
// have no machine-checkable format (Description, Reason, ErrorLog, ...).
func CheckFormat(tv *TypedValue) *TypedValue {
	if tv == nil {
		return nil
	}
	checker := lookupFormatChecker(tv.Tag())
	if checker == nil {
		return nil
	}
	if err := checker(tv.Payload); err != nil {
		return formatErr("format check failed for %s: %v", tv.Tag(), err)
	}
	return nil
}

// lookupFormatChecker resolves a checker by tag. We special-case Code with
// a Lang tag (per-language syntax checkers) and fall back to kind-only
// checkers for everything else.
func lookupFormatChecker(t Tag) FormatChecker {
	if t.Kind == KindCode {
		switch t.Lang {
		case LangGo:
			return checkGoSyntax
		case LangTypeScript:
			return checkTypeScriptSyntax
		case LangJavaScript:
			return checkJavaScriptSyntax
		case LangPython:
			return checkPythonSyntax
		case LangRust:
			return checkRustSyntax
		case LangJava:
			return checkJavaSyntax
		case LangHTML:
			return checkHTMLShape
		default:
			return nonEmpty("Code")
		}
	}
	switch t.Kind {
	case KindSignature:
		return checkSignature
	case KindTestSuite:
		return checkTestSuite
	case KindArchitecture:
		return checkArchitecture
	case KindTask, KindReason, KindDescription, KindErrorLog, KindErrorCode:
		return nonEmpty(string(t.Kind))
	}
	return nil
}

func nonEmpty(label string) FormatChecker {
	return func(payload string) error {
		if strings.TrimSpace(payload) == "" {
			return fmt.Errorf("%s payload is empty", label)
		}
		return nil
	}
}

// --- Code checkers (strict; toolchain-backed) ----------------------------
//
// Each language has a strict parser-backed implementation and a cheap
// keyword-fallback for environments without the toolchain. The strict
// path is the default; the fallback only runs when LookPath says the
// tool is missing. A missing toolchain emits a stderr warning so the
// operator notices the degraded mode.

// checkGoSyntax: Go uses go/parser from stdlib — fast, always available
// (it's part of the kcpos binary). Strict: a syntax error stops the
// router with FormatError before the value reaches a rule.
func checkGoSyntax(payload string) error {
	if strings.TrimSpace(payload) == "" {
		return fmt.Errorf("Go code payload empty")
	}
	fs := token.NewFileSet()
	if _, err := parser.ParseFile(fs, "snippet.go", payload, parser.AllErrors); err == nil {
		return nil
	}
	if _, err := parser.ParseExpr(payload); err == nil {
		return nil
	}
	return fmt.Errorf("Go syntax error (not a valid file or expression)")
}

// checkPythonSyntax: shells to `python3 -c "import ast; ast.parse(...)"`.
// AST parsing is parse-only — does NOT run the code, does NOT import
// modules — matching §4.4's "type checker validates format, not
// semantics".
func checkPythonSyntax(payload string) error {
	if strings.TrimSpace(payload) == "" {
		return fmt.Errorf("Python code payload empty")
	}
	pyExe := pickPython()
	if pyExe == "" {
		warnDegradedMode("python", "checkPythonShape (keyword fallback)")
		return checkPythonShapeKeyword(payload)
	}
	tmp, cleanup, err := writeFormatTemp("kcpos-py-fmt-*.py", payload)
	if err != nil {
		return fmt.Errorf("temp file: %v", err)
	}
	defer cleanup()
	script := fmt.Sprintf(`import ast,sys
try:
    ast.parse(open(%q).read())
except SyntaxError as e:
    sys.stderr.write(str(e))
    sys.exit(1)
`, tmp)
	out, err := runFormatCmd(pyExe, "-c", script)
	if err != nil {
		return fmt.Errorf("Python parse error: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// checkJavaScriptSyntax: `node --check <file>` is parse-only and built in.
func checkJavaScriptSyntax(payload string) error {
	if strings.TrimSpace(payload) == "" {
		return fmt.Errorf("JavaScript code payload empty")
	}
	if !commandExists("node") {
		warnDegradedMode("node", "checkJSLikeShape (keyword fallback)")
		return checkJSLikeShapeKeyword(payload)
	}
	tmp, cleanup, err := writeFormatTemp("kcpos-js-fmt-*.js", payload)
	if err != nil {
		return fmt.Errorf("temp file: %v", err)
	}
	defer cleanup()
	out, err := runFormatCmd("node", "--check", tmp)
	if err != nil {
		return fmt.Errorf("JavaScript syntax error: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// checkTypeScriptSyntax: prefer `esbuild --loader=ts --bundle=false` (parse
// only, ~50ms) on PATH; fall back to `tsc --noEmit --noResolve` (parse +
// type, slower but still strict) on PATH; finally fall back to the keyword
// heuristic if neither is installed.
//
// We intentionally do NOT shell to `npx`: its "ensure-then-run" semantics
// are unpredictable (interactive install prompts, slow cold start, exit
// codes that don't cleanly distinguish "tool missing" from "tool errored
// on user code"). Operators who want strict mode for TS should install
// esbuild or tsc to PATH directly.
func checkTypeScriptSyntax(payload string) error {
	if strings.TrimSpace(payload) == "" {
		return fmt.Errorf("TypeScript code payload empty")
	}
	tmp, cleanup, err := writeFormatTemp("kcpos-ts-fmt-*.ts", payload)
	if err != nil {
		return fmt.Errorf("temp file: %v", err)
	}
	defer cleanup()
	if commandExists("esbuild") {
		out, err := runFormatCmd("esbuild", "--loader=ts", "--bundle=false", "--log-level=error", tmp)
		if err != nil {
			return fmt.Errorf("TypeScript syntax error (esbuild): %s", strings.TrimSpace(string(out)))
		}
		return nil
	}
	if commandExists("tsc") {
		out, err := runFormatCmd("tsc", "--noEmit", "--noResolve", "--allowJs", "--target", "esnext", tmp)
		if err != nil {
			return fmt.Errorf("TypeScript syntax error (tsc): %s", strings.TrimSpace(string(out)))
		}
		return nil
	}
	warnDegradedMode("esbuild/tsc", "checkJSLikeShape (keyword fallback)")
	return checkJSLikeShapeKeyword(payload)
}

// checkRustSyntax: shell to `rustc --edition=2021 --emit=metadata
// --crate-type lib -o /dev/null`. Pure parse-and-typecheck without
// linking; ~500ms cold but accurate. We accept the cost in exchange for
// strict §4.4 compliance.
func checkRustSyntax(payload string) error {
	if strings.TrimSpace(payload) == "" {
		return fmt.Errorf("Rust code payload empty")
	}
	if !commandExists("rustc") {
		warnDegradedMode("rustc", "checkRustShape (keyword fallback)")
		return checkRustShapeKeyword(payload)
	}
	tmp, cleanup, err := writeFormatTemp("kcpos-rs-fmt-*.rs", payload)
	if err != nil {
		return fmt.Errorf("temp file: %v", err)
	}
	defer cleanup()
	out, err := runFormatCmd("rustc", "--edition=2021", "--emit=metadata",
		"--crate-type", "lib", "-o", os.DevNull, "--out-dir", os.TempDir(), tmp)
	if err != nil {
		return fmt.Errorf("Rust syntax error: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// checkJavaSyntax: `javac -d <tmpdir> -nowarn` parses + checks. javac
// requires the source file's class name to match its file name; we
// extract the class identifier from the payload and rename the temp
// file accordingly so a snippet like `public class Foo {...}` actually
// compiles.
func checkJavaSyntax(payload string) error {
	if strings.TrimSpace(payload) == "" {
		return fmt.Errorf("Java code payload empty")
	}
	if !commandExists("javac") {
		warnDegradedMode("javac", "checkJavaShape (keyword fallback)")
		return checkJavaShapeKeyword(payload)
	}
	className := extractJavaClassName(payload)
	if className == "" {
		className = "Snippet"
	}
	dir, err := os.MkdirTemp("", "kcpos-java-fmt-")
	if err != nil {
		return fmt.Errorf("temp dir: %v", err)
	}
	defer os.RemoveAll(dir)
	srcPath := filepath.Join(dir, className+".java")
	if err := os.WriteFile(srcPath, []byte(payload), 0o644); err != nil {
		return fmt.Errorf("write source: %v", err)
	}
	out, err := runFormatCmd("javac", "-d", dir, "-nowarn", srcPath)
	if err != nil {
		return fmt.Errorf("Java syntax error: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

var javaClassRe = regexp.MustCompile(`(?m)^\s*(?:public\s+)?(?:final\s+|abstract\s+)?(?:class|interface|enum|record)\s+([A-Za-z_][A-Za-z0-9_]*)`)

func extractJavaClassName(payload string) string {
	m := javaClassRe.FindStringSubmatch(payload)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func checkHTMLShape(payload string) error {
	s := strings.TrimSpace(payload)
	if s == "" {
		return fmt.Errorf("HTML payload empty")
	}
	if !strings.Contains(s, "<") || !strings.Contains(s, ">") {
		return fmt.Errorf("payload contains no HTML tags")
	}
	return nil
}

// --- Keyword fallback checkers (used when toolchain unavailable) --------

func checkJSLikeShapeKeyword(payload string) error {
	s := strings.TrimSpace(payload)
	if s == "" {
		return fmt.Errorf("payload empty")
	}
	keywords := []string{"function", "class ", "const ", "let ", "var ", "import ", "export ", "interface ", "type "}
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return nil
		}
	}
	return fmt.Errorf("no recognizable JS/TS structural keyword found")
}

func checkPythonShapeKeyword(payload string) error {
	s := strings.TrimSpace(payload)
	if s == "" {
		return fmt.Errorf("payload empty")
	}
	keywords := []string{"def ", "class ", "import ", "from ", "async ", "@"}
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return nil
		}
	}
	return fmt.Errorf("no recognizable Python keyword found")
}

func checkRustShapeKeyword(payload string) error {
	s := strings.TrimSpace(payload)
	if s == "" {
		return fmt.Errorf("payload empty")
	}
	keywords := []string{"fn ", "struct ", "enum ", "impl ", "use ", "pub ", "mod ", "trait "}
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return nil
		}
	}
	return fmt.Errorf("no recognizable Rust keyword found")
}

func checkJavaShapeKeyword(payload string) error {
	s := strings.TrimSpace(payload)
	if s == "" {
		return fmt.Errorf("payload empty")
	}
	keywords := []string{"class ", "interface ", "enum ", "package ", "import ", "public ", "private ", "protected "}
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return nil
		}
	}
	return fmt.Errorf("no recognizable Java keyword found")
}

// --- Structural checkers -------------------------------------------------

var sigArrowRe = regexp.MustCompile(`(?m)(=>|->|:\s*\w)`)

func checkSignature(payload string) error {
	s := strings.TrimSpace(payload)
	if s == "" {
		return fmt.Errorf("Signature payload empty")
	}
	if !sigArrowRe.MatchString(s) {
		return fmt.Errorf("Signature contains no input/output arrow or type annotation")
	}
	return nil
}

var testAssertionRe = regexp.MustCompile(`(?m)\b(assert|expect|t\.Errorf|t\.Fatalf|should|toBe|toEqual)\b`)

func checkTestSuite(payload string) error {
	s := strings.TrimSpace(payload)
	if s == "" {
		return fmt.Errorf("TestSuite payload empty")
	}
	if !testAssertionRe.MatchString(s) {
		return fmt.Errorf("TestSuite contains no assertions (assert/expect/t.Errorf/...)")
	}
	return nil
}

func checkArchitecture(payload string) error {
	s := strings.TrimSpace(payload)
	if s == "" {
		return fmt.Errorf("Architecture payload empty")
	}
	low := strings.ToLower(s)
	hasList := strings.Contains(s, "- ") || strings.Contains(s, "* ") ||
		strings.Contains(s, "1.") || strings.Contains(s, "1)")
	hasKeyword := strings.Contains(low, "module") || strings.Contains(low, "intermediate") ||
		strings.Contains(low, "子模块") || strings.Contains(low, "中间")
	if !hasList && !hasKeyword {
		return fmt.Errorf("Architecture lacks a list or 'modules/intermediate' keyword")
	}
	return nil
}

// --- helpers -------------------------------------------------------------

func writeFormatTemp(pattern, content string) (string, func(), error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", nil, err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, err
	}
	f.Close()
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}

func runFormatCmd(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), formatCheckTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func pickPython() string {
	if commandExists("python3") {
		return "python3"
	}
	if commandExists("python") {
		return "python"
	}
	return ""
}

// warnDegradedMode prints a one-time stderr notice when a strict format
// checker falls back to a keyword heuristic. The map below records which
// modes already warned so we don't spam an interactive session on every
// dispatch.
var warnedDegraded = map[string]bool{}

func warnDegradedMode(missing, fallback string) {
	key := missing + "->" + fallback
	if warnedDegraded[key] {
		return
	}
	warnedDegraded[key] = true
	fmt.Fprintf(os.Stderr,
		"[typecalc/format] %s not on PATH — falling back to %s. "+
			"For strict §4.4 syntax checking, install %s.\n",
		missing, fallback, missing)
}
