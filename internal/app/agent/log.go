package agent

import (
	"os"
	"strings"
	"time"
)

// Stamp returns "[HH:MM:SS] " — used as a per-event prefix on stderr
// status lines (tool calls, [thinking] banners, hook violations,
// subagent banners). Empty when the user opts out via env (see
// timestampsDisabled).
//
// Granularity is one second: agents typically emit a few events per
// second, so finer precision would clutter without adding signal. The
// format is local-time and tz-agnostic to keep lines short — the
// session banner emits one absolute ISO-with-tz line so a reader can
// disambiguate when needed.
func Stamp() string {
	if timestampsDisabled() {
		return ""
	}
	return "[" + time.Now().Format("15:04:05") + "] "
}

// SessionStartBanner returns the once-per-process banner with full
// date + time + zone. The chat command prints this at startup so any
// later "[15:20:47]" line is unambiguous.
func SessionStartBanner() string {
	if timestampsDisabled() {
		return ""
	}
	return "[" + time.Now().Format("2006-01-02 15:04:05 -0700") + "] "
}

// timestampsDisabled honours the KCPOS_NO_TIMESTAMP env var. Anything
// truthy (1, true, yes — case-insensitive) suppresses the prefix.
// Useful in tests or when piping into a downstream logger that already
// timestamps.
func timestampsDisabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("KCPOS_NO_TIMESTAMP")))
	switch v {
	case "1", "true", "yes":
		return true
	}
	return false
}
