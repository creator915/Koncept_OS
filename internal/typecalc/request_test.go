package typecalc

import "testing"

func TestRequest_EnrichmentAccumulates(t *testing.T) {
	req := NewRequest("build counter")
	first, err := EnrichRequest(req, "compile_error", &CompileErrorDetail{
		Task:      "build counter",
		ErrorCode: "TS2304",
		ErrorLog:  "cannot find name 'foo'",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := EnrichRequest(first, "compile_error", &CompileErrorDetail{
		Task:      "build counter",
		ErrorCode: "TS1109",
		ErrorLog:  "expression expected",
	})
	if err != nil {
		t.Fatal(err)
	}
	env, err := DecodeRequest(second)
	if err != nil {
		t.Fatal(err)
	}
	if env.Task != "build counter" {
		t.Fatalf("task lost: %q", env.Task)
	}
	if len(env.History) != 2 {
		t.Fatalf("history len = %d, want 2", len(env.History))
	}
	if env.History[0].Tag != "compile_error" {
		t.Fatalf("history[0].Tag = %q", env.History[0].Tag)
	}
}

func TestRequest_EnrichmentImmutable(t *testing.T) {
	original := NewRequest("t")
	enriched, err := EnrichRequest(original, "x", "y")
	if err != nil {
		t.Fatal(err)
	}
	origEnv, _ := DecodeRequest(original)
	enrEnv, _ := DecodeRequest(enriched)
	if len(origEnv.History) != 0 {
		t.Fatalf("original mutated: %d entries", len(origEnv.History))
	}
	if len(enrEnv.History) != 1 {
		t.Fatalf("enriched len = %d", len(enrEnv.History))
	}
}

func TestObstacle_RoundTrip(t *testing.T) {
	o := NewObstacle("task X", "compile retried 5 times", []RequestEntry{
		{Tag: "compile_error", Detail: []byte(`{"errorCode":"X"}`)},
	})
	d, err := DecodeObstacle(o)
	if err != nil {
		t.Fatal(err)
	}
	if d.Task != "task X" || d.Reason != "compile retried 5 times" {
		t.Fatalf("decoded incorrectly: %+v", d)
	}
	if len(d.Trail) != 1 {
		t.Fatalf("trail len = %d", len(d.Trail))
	}
}
