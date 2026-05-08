package agent

import (
	"os"
	"regexp"
	"testing"
)

func TestStamp_DefaultFormat(t *testing.T) {
	os.Unsetenv("KCPOS_NO_TIMESTAMP")
	got := Stamp()
	// "[15:04:05] " — 11 chars total (including trailing space).
	if !regexp.MustCompile(`^\[\d{2}:\d{2}:\d{2}\] $`).MatchString(got) {
		t.Fatalf("unexpected stamp format: %q", got)
	}
}

func TestStamp_DisabledByEnv(t *testing.T) {
	t.Setenv("KCPOS_NO_TIMESTAMP", "1")
	if got := Stamp(); got != "" {
		t.Fatalf("expected empty when disabled, got %q", got)
	}
	t.Setenv("KCPOS_NO_TIMESTAMP", "true")
	if got := Stamp(); got != "" {
		t.Fatalf("expected empty for true, got %q", got)
	}
	t.Setenv("KCPOS_NO_TIMESTAMP", "yes")
	if got := Stamp(); got != "" {
		t.Fatalf("expected empty for yes, got %q", got)
	}
}

func TestStamp_NotDisabledByOtherValues(t *testing.T) {
	t.Setenv("KCPOS_NO_TIMESTAMP", "0")
	if got := Stamp(); got == "" {
		t.Fatalf("'0' should not disable, got empty")
	}
	t.Setenv("KCPOS_NO_TIMESTAMP", "")
	if got := Stamp(); got == "" {
		t.Fatalf("empty value should not disable, got empty")
	}
}

func TestSessionStartBanner_FullDate(t *testing.T) {
	t.Setenv("KCPOS_NO_TIMESTAMP", "")
	got := SessionStartBanner()
	// "[2006-01-02 15:04:05 -0700] " — date-time with offset.
	re := regexp.MustCompile(`^\[\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} [+-]\d{4}\] $`)
	if !re.MatchString(got) {
		t.Fatalf("unexpected banner format: %q", got)
	}
}

func TestSessionStartBanner_DisabledByEnv(t *testing.T) {
	t.Setenv("KCPOS_NO_TIMESTAMP", "1")
	if got := SessionStartBanner(); got != "" {
		t.Fatalf("expected empty when disabled, got %q", got)
	}
}
