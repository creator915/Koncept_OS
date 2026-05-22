package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WorkdirSnapshot is a path-to-sha index over the subset of the
// workdir kcpos considers "state". Taking a snapshot before each
// tool call and another after lets us compute the side_effects of
// that tool by diffing the two — without needing per-tool
// instrumentation.
//
// Coverage policy (what counts as "state"):
//
//   - K/                   — graph + sessions + defs + (frags if present)
//   - .kcpos/typecalc/     — evidence bundles
//   - compile.sh           — build script
//   - top-level source files matching *.c / *.h / *.go / *.py / *.ts /
//     *.js / *.html / *.htm / *.rs / *.sh
//   - subdirectories matching src/ / pkg/ / internal/ / cmd/ / lib/ /
//     defs/ (Go-style + agent-conventional layouts)
//
// Coverage excludes:
//
//   - transcripts/   — LLM history, not state (already in event chain)
//   - run.log        — display log, derivable
//   - .kcpos/snapshots/ — recursion guard: never snapshot the snapshot
//     store
//   - probe / executable.ref — harness-managed (never agent-modified)
//   - Anything under data/ / images/ / fonts/ that came from the
//     original PB container extraction (read-only fixtures)
//
// Paths in the snapshot are workdir-relative so replay can rebuild
// at a different absolute prefix.
type WorkdirSnapshot struct {
	Workdir string            // root that paths are relative to
	Files   map[string]string // workdir-relative path → sha256(content)
}

// TakeWorkdirSnapshot walks the configured roots under workdir and
// hashes every file found. Returns a fresh snapshot — caller decides
// how to use it (typically pair with another snapshot for Diff).
//
// Errors during walk are returned (rather than swallowed) so a
// permission problem doesn't silently produce a partial snapshot
// that would make a diff look like "many files deleted".
func TakeWorkdirSnapshot(workdir string) (*WorkdirSnapshot, error) {
	snap := &WorkdirSnapshot{
		Workdir: workdir,
		Files:   map[string]string{},
	}
	// Walk top-level workdir, decide per-entry whether to recurse.
	entries, err := os.ReadDir(workdir)
	if err != nil {
		if os.IsNotExist(err) {
			return snap, nil // empty workdir is a valid snapshot
		}
		return nil, fmt.Errorf("workdir snapshot: read root: %w", err)
	}
	for _, ent := range entries {
		name := ent.Name()
		if shouldSkipTopLevel(name) {
			continue
		}
		fullPath := filepath.Join(workdir, name)
		if ent.IsDir() {
			if !shouldRecurseDir(name) {
				continue
			}
			if err := walkAndHash(workdir, fullPath, snap.Files); err != nil {
				return nil, err
			}
		} else {
			if !shouldHashTopLevelFile(name) {
				continue
			}
			if err := hashOne(workdir, fullPath, snap.Files); err != nil {
				return nil, err
			}
		}
	}
	return snap, nil
}

// shouldSkipTopLevel returns true for top-level entries that are
// never snapshotted — recursion guards and harness-owned artifacts.
//
// `executable` is intentionally excluded in Phase 3 scope:
//   - In reconstruction-mode tasks, the `compile` tool docker-cp's
//     a built binary onto the host as `executable`. Capturing it
//     would add 5-50MB per compile event to the blob pool.
//   - Replay can rebuild `executable` by re-running compile.sh on
//     the captured sources; the binary itself is derivable, not
//     primary state.
//   - Phase 5 (rollback) may revisit this if "skip recompile on
//     replay" becomes load-bearing. At that point we add an
//     ExecutableReplace side_effect emitted from the compile tool
//     wrapper.
//
// `probe` and `executable.ref` are harness-owned (setup_real_pb.sh
// writes them once at container init, never modified by the agent),
// so excluding them keeps them out of the diff noise entirely.
//
// Symlink handling note: top-level entries are checked via DirEntry,
// which uses lstat — so a symlink-to-directory is NOT recursed into.
// A symlink-to-file with a recognised extension WILL be hashed (open
// follows the link). PB-30 workdirs don't use symlinks so this case
// stays under-tested; flag for revisit when SPEC-class projects
// surface real symlink usage.
func shouldSkipTopLevel(name string) bool {
	switch name {
	case "transcripts", "run.log", "trace.json", "probe",
		"executable", "executable.ref":
		return true
	}
	// Hidden dotfiles other than .kcpos are user/system files we
	// don't track. .kcpos itself we recurse into (for typecalc/) but
	// excluding the snapshots subdir.
	if name != ".kcpos" && strings.HasPrefix(name, ".") {
		return true
	}
	return false
}

// shouldRecurseDir returns true for directory names that are part of
// the agent's state surface. The list is permissive — better to hash
// a few irrelevant files than to miss state changes.
func shouldRecurseDir(name string) bool {
	switch name {
	case "K", "src", "pkg", "internal", "cmd", "lib", "defs", ".kcpos":
		return true
	}
	return false
}

// shouldHashTopLevelFile returns true for files in workdir root that
// look like agent source / build artifacts. Anything else is read-only
// fixture (README, LICENSE, FAQ, *.1 man pages, data/, fonts/, ...).
func shouldHashTopLevelFile(name string) bool {
	switch name {
	case "compile.sh", "SPEC.md":
		return true
	}
	// Source file extensions agents commonly write at top level.
	switch strings.ToLower(filepath.Ext(name)) {
	case ".c", ".h", ".go", ".py", ".ts", ".js", ".html", ".htm",
		".rs", ".sh", ".java", ".hs":
		return true
	}
	return false
}

// walkAndHash recursively hashes every file under dirPath into out
// using workdir-relative keys. Excludes the snapshots subtree (which
// would otherwise create unbounded recursion: snapshotting writes
// events under .kcpos/snapshots, which are themselves state...).
func walkAndHash(workdir, dirPath string, out map[string]string) error {
	return filepath.Walk(dirPath, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Hard skip the snapshot store — recursion guard.
			rel, _ := filepath.Rel(workdir, p)
			if rel == ".kcpos/snapshots" || strings.HasPrefix(rel, ".kcpos/snapshots/") {
				return filepath.SkipDir
			}
			// Don't recurse into transcripts (in case it's nested
			// inside K/ for some reason).
			if filepath.Base(p) == "transcripts" {
				return filepath.SkipDir
			}
			return nil
		}
		return hashOne(workdir, p, out)
	})
}

// hashOne reads `p`, computes sha256, and stores it in `out` under
// the workdir-relative key.
func hashOne(workdir, p string, out map[string]string) error {
	f, err := os.Open(p)
	if err != nil {
		// File may have been deleted concurrently — skip rather
		// than fail the whole snapshot.
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("hash %s: %w", p, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("read %s: %w", p, err)
	}
	rel, err := filepath.Rel(workdir, p)
	if err != nil {
		return err
	}
	out[filepath.ToSlash(rel)] = hex.EncodeToString(h.Sum(nil))
	return nil
}

// WorkdirDiff captures the set of files that changed between two
// snapshots. Three buckets so callers can map directly to
// side_effect kinds without re-categorising.
type WorkdirDiff struct {
	Added    []string          // present in post, not in pre
	Modified []string          // in both, but content sha differs
	Deleted  []string          // in pre, not in post
	HashPost map[string]string // sha lookup for added/modified — saves caller a second hash pass
}

// Diff computes added/modified/deleted between this (pre) snapshot
// and post. Returned slices are sorted for deterministic event
// content (otherwise the chain sha would depend on map iteration
// order, defeating the canonical-marshal stability we rely on).
func (pre *WorkdirSnapshot) Diff(post *WorkdirSnapshot) *WorkdirDiff {
	d := &WorkdirDiff{HashPost: post.Files}
	for path, postSha := range post.Files {
		preSha, ok := pre.Files[path]
		if !ok {
			d.Added = append(d.Added, path)
			continue
		}
		if preSha != postSha {
			d.Modified = append(d.Modified, path)
		}
	}
	for path := range pre.Files {
		if _, ok := post.Files[path]; !ok {
			d.Deleted = append(d.Deleted, path)
		}
	}
	sort.Strings(d.Added)
	sort.Strings(d.Modified)
	sort.Strings(d.Deleted)
	return d
}

// IsEmpty reports whether the diff has no changes — useful for
// skipping side_effect emission on read-only tool calls.
func (d *WorkdirDiff) IsEmpty() bool {
	return len(d.Added) == 0 && len(d.Modified) == 0 && len(d.Deleted) == 0
}
