package lang

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

	"github.com/creator915/Koncept_OS/internal/typecalc"
)

// FormatChecker validates that a payload conforms to the structural
// expectations of its type tag. §4.4 of the doc: the format checker
// verifies syntax/shape; semantic validity is the testers' / compilers'
// job.
type FormatChecker func(payload string) error

// formatCheckTimeout caps a single language-toolchain invocation. Format
// checking runs on every router dispatch and must not become a bottleneck.
const formatCheckTimeout = 4 * time.Second

// CheckFormat dispatches on the typed value's tag. Returns nil if the
// payload's format matches expectations, or a *typecalc.TypedValue of
// Kind KindFormatError otherwise. Unknown tags are treated as "no check
// available".
func CheckFormat(tv *typecalc.TypedValue) *typecalc.TypedValue {
	if tv == nil {
		return nil
	}
	checker := lookupFormatChecker(tv.Tag())
	if checker == nil {
		return nil
	}
	if err := checker(tv.Payload); err != nil {
		return typecalc.FormatErr("format check failed for %s: %v", tv.Tag(), err)
	}
	return nil
}

func lookupFormatChecker(t typecalc.Tag) FormatChecker {
	if t.Kind == typecalc.KindCode {
		switch t.Lang {
		case typecalc.LangGo:
			return checkGoSyntax
		case typecalc.LangTypeScript:
			return checkTypeScriptSyntax
		case typecalc.LangJavaScript:
			return checkJavaScriptSyntax
		case typecalc.LangPython:
			return checkPythonSyntax
		case typecalc.LangRust:
			return checkRustSyntax
		case typecalc.LangJava:
			return checkJavaSyntax
		case typecalc.LangHTML:
			return checkHTMLShape
		default:
			return nonEmpty("Code")
		}
	}
	switch t.Kind {
	case typecalc.KindSignature:
		return checkSignature
	case typecalc.KindTestSuite:
		return checkTestSuite
	case typecalc.KindArchitecture:
		return checkArchitecture
	case typecalc.KindTask, typecalc.KindReason, typecalc.KindDescription, typecalc.KindErrorLog, typecalc.KindErrorCode:
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

// --- Keyword fallback checkers ------------------------------------------

func checkJSLikeShapeKeyword(payload string) error {
	s := strings.TrimSpace(payload)
	if s == "" {
		return fmt.Errorf("payload empty")
	}
	for _, kw := range []string{"function", "class ", "const ", "let ", "var ", "import ", "export ", "interface ", "type "} {
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
	for _, kw := range []string{"def ", "class ", "import ", "from ", "async ", "@"} {
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
	for _, kw := range []string{"fn ", "struct ", "enum ", "impl ", "use ", "pub ", "mod ", "trait "} {
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
	for _, kw := range []string{"class ", "interface ", "enum ", "package ", "import ", "public ", "private ", "protected "} {
		if strings.Contains(s, kw) {
			return nil
		}
	}
	return fmt.Errorf("no recognizable Java keyword found")
}

// --- Structural checkers ------------------------------------------------

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

// --- helpers ------------------------------------------------------------

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
