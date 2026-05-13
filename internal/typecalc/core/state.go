package core

import (
	"os"
	"path/filepath"
)

// ObjectState is a structured view of an object's verification evidence
// on disk. Callers that want to know "is this object verifiable?" /
// "does it have an escape pair?" / "can it pass the confirm hook?"
// query this struct rather than each opening files independently.
//
// Pre-v9.0 the same questions were answered in three places:
//   - internal/agent/hooks.go (typecalc-use warning hook)
//   - internal/tools/graph/graph.go (typecalc-use blocking check at merge)
//   - internal/session/gate.go (root-finish gate)
//
// Each maintained its own path resolution + os.Stat + ReadFile logic,
// drifting whenever a new evidence kind landed (v8.5 obstacle+waiver
// pair, v8.6 accepted-evidence-required carve-out). v9.0 routes all
// three callers through ObjectState so adding a new evidence dimension
// (kind, hash, etc.) touches one place.
type ObjectState struct {
	ObjectID string

	HasBundle bool

	// HasCompileEvidence / HasTestEvidence indicate that the
	// corresponding bundle section is populated. They do NOT speak to
	// whether the recorded run was OK — see CompileOK / TestOK.
	HasCompileEvidence bool
	HasTestEvidence    bool

	// Evidence kind tags. "compile", "test", "insufficient". Empty when
	// the section doesn't exist.
	CompileKind string
	TestKind    string

	// OK flags from the most recent runs. False when the section is
	// missing or the run reported failure.
	CompileOK bool
	TestOK    bool

	// Lang the latest evidence was recorded for.
	Lang string

	// HasAccepted indicates whether the reviewer verdict section is
	// present. AcceptedOK reflects its OK field.
	HasAccepted bool
	AcceptedOK  bool

	// HasRuntimeTrace + RuntimeTraceCallCount carry the v9.0 trace
	// section's existence and size. Callers that need full Calls
	// should load the bundle directly.
	HasRuntimeTrace      bool
	RuntimeTraceCallCount int

	// HasRuntimeSmoke + RuntimeSmokeOK reflect the v9.1 browser smoke
	// section. The gate's [runtime-smoke-required] rule fires when
	// HasRuntimeSmoke=false for HTML deliverables.
	HasRuntimeSmoke bool
	RuntimeSmokeOK  bool

	// SourceHash is the bundle-level staleness anchor.
	SourceHash string
}

// HasUsableEvidence reports whether the object has any passing evidence
// — compile or test — recorded. Used by the typecalc-use hook to decide
// whether status=confirmed is premature.
//
// v9.2 — the obstacle+waiver-as-evidence carve-out was removed. There is
// no longer any escape pathway; an object either has real passing
// evidence or it cannot be confirmed.
func (s ObjectState) HasUsableEvidence() bool {
	if s.HasCompileEvidence && s.CompileOK {
		return true
	}
	if s.HasTestEvidence && s.TestOK {
		return true
	}
	return false
}

// LoadObjectState reads the unified bundle for objectID under cwd
// (passing cwd="" or "." resolves against the process working directory)
// and produces the consolidated ObjectState. Missing bundle returns an
// ObjectState with HasBundle=false and every other flag false — safe
// to query.
func LoadObjectState(objectID, cwd string) ObjectState {
	s := ObjectState{ObjectID: objectID}
	if objectID == "" {
		return s
	}
	// v9.0 layout: one bundle file per object at BundleDir/<id>.json.
	// We support cwd-relative reads so the gate can run against a
	// specific project tree (fixtures, tests) without chdir.
	path := filepath.Join(BundleDir, objectID+".json")
	if cwd != "" && cwd != "." && !filepath.IsAbs(path) {
		path = filepath.Join(cwd, BundleDir, objectID+".json")
	}
	if _, err := os.Stat(path); err != nil {
		return s
	}
	// Load via the package reader so version-tag enforcement applies
	// uniformly. ReadBundle uses the process cwd; we chdir-in for
	// non-trivial cwd to keep semantics identical to the file Stat.
	var b *EvidenceBundle
	if cwd == "" || cwd == "." {
		var ok bool
		b, ok = ReadBundle(objectID)
		if !ok {
			return s
		}
	} else {
		prev, err := os.Getwd()
		if err != nil {
			return s
		}
		if err := os.Chdir(cwd); err != nil {
			return s
		}
		var ok bool
		b, ok = ReadBundle(objectID)
		_ = os.Chdir(prev)
		if !ok {
			return s
		}
	}

	s.HasBundle = true
	s.SourceHash = b.SourceHash
	if b.Compile != nil {
		s.HasCompileEvidence = true
		s.CompileKind = b.Compile.Kind
		s.CompileOK = b.Compile.OK
		s.Lang = b.Compile.Lang
	}
	if b.Test != nil {
		s.HasTestEvidence = true
		s.TestKind = b.Test.Kind
		s.TestOK = b.Test.OK
		if s.Lang == "" {
			s.Lang = b.Test.Lang
		}
	}
	// v9.2 — Obstacle / Waiver sections may still appear in older
	// bundles but are no longer consulted. The reader ignores them.
	if b.Accepted != nil {
		s.HasAccepted = true
		s.AcceptedOK = b.Accepted.OK
	}
	if b.RuntimeTrace != nil {
		s.HasRuntimeTrace = true
		s.RuntimeTraceCallCount = len(b.RuntimeTrace.Calls)
	}
	if b.RuntimeSmoke != nil {
		s.HasRuntimeSmoke = true
		s.RuntimeSmokeOK = b.RuntimeSmoke.OK
	}
	return s
}
