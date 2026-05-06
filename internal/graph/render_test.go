package graph

import (
	"strings"
	"testing"
)

func tinyGraph() *Graph {
	g := NewGraph()
	g.AddAttribute("raw_data", NewAttribute("defs/raw_data.ts", "raw"))
	g.AddAttribute("clean_data", NewAttribute("defs/clean_data.ts", "clean"))
	g.AddAttribute("temperature_celsius", NewAttribute("defs/tc.ts", "celsius"))
	g.LinkRefine("temperature_celsius", "raw_data") // contrived for test
	g.AddObject("Loader", NewObject("defs/Loader.ts", "loads"))
	g.AddObject("Cleaner", NewObject("defs/Cleaner.ts", "cleans"))
	g.LinkProduce("Loader", "raw_data")
	g.LinkConsume("Cleaner", "raw_data")
	g.LinkProduce("Cleaner", "clean_data")
	return g
}

func TestRenderMermaid_Structure(t *testing.T) {
	g := tinyGraph()
	out := g.RenderMermaid()
	want := []string{
		"graph LR",
		"raw_data[",
		"Loader([",
		"Loader --> raw_data",        // produce
		"raw_data --> Cleaner",       // consume
		"Cleaner --> clean_data",     // produce
		"temperature_celsius -.->|refines| raw_data", // refine, dashed
		"classDef declared",
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in mermaid output:\n%s", w, out)
		}
	}
}

func TestRenderMermaid_Deterministic(t *testing.T) {
	g := tinyGraph()
	a := g.RenderMermaid()
	b := g.RenderMermaid()
	if a != b {
		t.Errorf("Mermaid output should be deterministic across runs")
	}
}

func TestRenderDot_Structure(t *testing.T) {
	g := tinyGraph()
	out := g.RenderDot()
	want := []string{
		"digraph hypergraph",
		"rankdir=LR",
		`"raw_data" [shape=box`,
		`"Loader" [shape=ellipse`,
		`"Loader" -> "raw_data"`,
		`"raw_data" -> "Cleaner"`,
		`"temperature_celsius" -> "raw_data" [style=dashed`,
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in dot output:\n%s", w, out)
		}
	}
}

func TestRenderMermaid_EmptyGraph(t *testing.T) {
	g := NewGraph()
	out := g.RenderMermaid()
	if !strings.Contains(out, "graph LR") {
		t.Errorf("empty graph should still produce valid mermaid header")
	}
}

func TestRenderMermaid_EscapesInLabels(t *testing.T) {
	// We don't currently emit user-supplied text into labels, but the escape
	// helper should be honest about its job.
	got := escapeMermaid(`a"b<c>d`)
	if !strings.Contains(got, "&quot;") || !strings.Contains(got, "&lt;") || !strings.Contains(got, "&gt;") {
		t.Errorf("escapeMermaid did not handle special chars: %q", got)
	}
}
