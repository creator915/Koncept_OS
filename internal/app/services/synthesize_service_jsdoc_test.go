package services

import (
	"strings"
	"testing"
)

func TestExtractJSDocExamples_OneExample(t *testing.T) {
	src := `/**
 * Foo: computes the score.
 *
 * @param {{x:number}} input
 * @returns {{score:number}}
 *
 * @example
 * Foo({x: 1})  // → {score: 2}
 */
function Foo(x) { throw new Error("contract-only"); }
`
	got := extractJSDocExamples(src)
	if len(got) != 1 {
		t.Fatalf("expected 1 example, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "Foo({x: 1})") || !strings.Contains(got[0], "{score: 2}") {
		t.Errorf("example body lost its I/O: %q", got[0])
	}
}

func TestExtractJSDocExamples_MultipleExamples(t *testing.T) {
	src := `/**
 * @example
 * Foo({x: 1})  // → {y: 2}
 * @example boundary
 * Foo({x: 0})  // → {y: 0}
 * @example
 * Foo({x: 100})  // → {y: 200}
 */
function Foo(x) { throw new Error("c"); }
`
	got := extractJSDocExamples(src)
	if len(got) != 3 {
		t.Fatalf("expected 3 examples, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[1], "boundary") || !strings.Contains(got[1], "{y: 0}") {
		t.Errorf("boundary example missing label or value: %q", got[1])
	}
}

func TestExtractJSDocExamples_StopsAtNextJSDocTag(t *testing.T) {
	src := `/**
 * @example
 * Foo(1)  // → 2
 * @returns {number}
 */
function Foo(x) { throw new Error("c"); }
`
	got := extractJSDocExamples(src)
	if len(got) != 1 {
		t.Fatalf("expected 1 example, got %d: %v", len(got), got)
	}
	if strings.Contains(got[0], "@returns") {
		t.Errorf("example body should not include subsequent @returns tag: %q", got[0])
	}
}

func TestExtractJSDocExamples_NoneWhenAbsent(t *testing.T) {
	src := `/** @param {*} x @returns {*} */
function Foo(x) { throw new Error("c"); }
`
	got := extractJSDocExamples(src)
	if len(got) != 0 {
		t.Errorf("expected 0 examples for example-less doc, got %v", got)
	}
}

func TestExtractJSDocExamples_EmptyInputSafe(t *testing.T) {
	if got := extractJSDocExamples(""); len(got) != 0 {
		t.Errorf("empty input should return nil/empty, got %v", got)
	}
}
