package graphtools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 2026-05-21 — structural Go testable-contract guard.
//
// Replaces the pre-existing prompt-level creosote in H_graph_declare
// that told the LLM "do not declare graph object impls in package main".
// PB-30 batch #3 burned ~40min on `entr` because the agent set impl to
// a root-level `package main` file; typecalc cannot import it, so the
// chain's compile/synthesize/test pipeline spun.
//
// The guard lives in graph_merge_object's impl-validation block — fail
// loudly at the moment impl is set, before the chain costs anything.

func TestScanGoPackage_Variants(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"basic main", "package main\n\nfunc main() {}\n", "main"},
		{"basic lib", "package mylib\n", "mylib"},
		{"leading line comments", "// header\n// more\npackage core\n", "core"},
		{"leading block comment", "/* block */\npackage core\n", "core"},
		{"multi-line block comment", "/*\n multi\n line\n*/\npackage core\n", "core"},
		{"blanks before package", "\n\n\npackage tools\n", "tools"},
		{"no package clause", "func foo() {}\n", ""},
		{"empty file", "", ""},
		{"package with trailing comment", "package svc // staging\n", "svc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scanGoPackage([]byte(tc.src))
			if got != tc.want {
				t.Errorf("scanGoPackage(%q) = %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}

func TestGraphMergeObject_RejectsGoPackageMainImpl(t *testing.T) {
	freshGraphCwd(t)

	// Create the object first (declared) so merge has a target.
	_, err := graphCreateObjectTool().Run(context.Background(), map[string]interface{}{
		"id":              "BuildCommand",
		"intent":          "Construct the command-line invocation",
		"storyPoints":     2,
		"storyRationale": "single straight-line transform from opts to argv",
	})
	if err != nil {
		t.Fatalf("create object: %v", err)
	}

	// Write a package-main file in cwd root — the failure mode entr hit.
	if err := os.WriteFile("main.go", []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := graphMergeObjectTool()
	_, err = tool.Run(context.Background(), map[string]interface{}{
		"id":    "BuildCommand",
		"patch": `{"impl":"main.go"}`,
	})
	if err == nil {
		t.Fatal("expected refusal of impl=main.go declaring package main")
	}
	if !strings.Contains(err.Error(), "package main") || !strings.Contains(err.Error(), "sub-package") {
		t.Fatalf("error must name `package main` and direct to sub-package, got: %v", err)
	}
}

func TestGraphMergeObject_AcceptsGoSubPackageImpl(t *testing.T) {
	freshGraphCwd(t)
	_, err := graphCreateObjectTool().Run(context.Background(), map[string]interface{}{
		"id":              "BuildCommand",
		"intent":          "Construct the command-line invocation",
		"storyPoints":     2,
		"storyRationale": "single straight-line transform from opts to argv",
	})
	if err != nil {
		t.Fatalf("create object: %v", err)
	}
	if err := os.MkdirAll("pkg/core", 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join("pkg", "core", "build.go")
	if err := os.WriteFile(src, []byte("package core\n\nfunc Build() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := graphMergeObjectTool()
	_, err = tool.Run(context.Background(), map[string]interface{}{
		"id":    "BuildCommand",
		"patch": `{"impl":"pkg/core/build.go"}`,
	})
	if err != nil {
		t.Fatalf("sub-package Go impl should be accepted, got: %v", err)
	}
}

func TestGraphMergeObject_DefersWhenGoImplFileMissing(t *testing.T) {
	freshGraphCwd(t)
	_, err := graphCreateObjectTool().Run(context.Background(), map[string]interface{}{
		"id":              "BuildCommand",
		"intent":          "Construct the command-line invocation",
		"storyPoints":     2,
		"storyRationale": "single straight-line transform from opts to argv",
	})
	if err != nil {
		t.Fatalf("create object: %v", err)
	}

	// File doesn't exist yet — agent often sets impl before write_file.
	// The guard must NOT reject; it has no way to know the package and
	// must defer to a later merge (when content is written).
	tool := graphMergeObjectTool()
	_, err = tool.Run(context.Background(), map[string]interface{}{
		"id":    "BuildCommand",
		"patch": `{"impl":"pkg/core/build.go"}`,
	})
	if err != nil {
		t.Fatalf("missing impl file must NOT trip the guard (defer to later), got: %v", err)
	}
}
