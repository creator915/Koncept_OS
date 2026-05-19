package memory

import (
	"path/filepath"
	"testing"
)

// KCPOS_TRANSCRIPT_DIR support: unset ⇒ historical <cwd>/.kcpos/transcripts;
// set ⇒ that exact dir (so the JSON transcript can land directly in a
// chosen output folder, no post-hoc copy).

func TestDirIn_DefaultIsCwdDotKcpos(t *testing.T) {
	t.Setenv("KCPOS_TRANSCRIPT_DIR", "") // explicitly unset/empty
	got := New("/work/proj").Dir
	want := filepath.Join("/work/proj", ".kcpos", "transcripts")
	if got != want {
		t.Fatalf("default transcript dir = %q, want %q", got, want)
	}
}

func TestDirIn_EnvOverrideWins(t *testing.T) {
	t.Setenv("KCPOS_TRANSCRIPT_DIR", "/out/pong-01")
	tr := New("/work/proj")
	if tr.Dir != "/out/pong-01" {
		t.Fatalf("override dir = %q, want /out/pong-01", tr.Dir)
	}
	if want := filepath.Join("/out/pong-01", tr.ID+".json"); tr.Path() != want {
		t.Fatalf("Path() = %q, want %q (JSON lands directly in the chosen folder)", tr.Path(), want)
	}
}

func TestDirIn_EmptyEnvFallsBackToCwd(t *testing.T) {
	t.Setenv("KCPOS_TRANSCRIPT_DIR", "")
	if got := New("/x").Dir; got != filepath.Join("/x", ".kcpos", "transcripts") {
		t.Fatalf("empty env must fall back to cwd default, got %q", got)
	}
}
