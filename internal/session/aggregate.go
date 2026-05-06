package session

import (
	"time"
)

// Aggregate walks a session and all its descendants, collecting their
// output.{implementations, newSignatures, newAttributes, tests} fields into
// a single deduplicated list on the named session. Per CLAUDE.md §5.5 R1.
//
// Bottom-up semantics: calling Aggregate on a root session pulls in every
// descendant's outputs in one shot, regardless of whether intermediate
// sessions had been aggregated already.
//
// graphDiff is NOT aggregated — each session keeps its own diff for
// rollback purposes.
func Aggregate(dir, id string) (*Session, error) {
	s, err := Load(dir, id)
	if err != nil {
		return nil, err
	}
	// Initialize as empty slices so JSON renders as [] (not null) when
	// every descendant has empty outputs.
	impls := []string{}
	sigs := []string{}
	attrs := []string{}
	tests := []string{}
	var visit func(sid string) error
	visit = func(sid string) error {
		cur, err := Load(dir, sid)
		if err != nil {
			return err
		}
		impls = appendUnique(impls, cur.Output.Implementations)
		sigs = appendUnique(sigs, cur.Output.NewSignatures)
		attrs = appendUnique(attrs, cur.Output.NewAttributes)
		tests = appendUnique(tests, cur.Output.Tests)
		for _, child := range cur.Children {
			if !Exists(dir, child) {
				continue
			}
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(id); err != nil {
		return nil, err
	}
	s.Output.Implementations = impls
	s.Output.NewSignatures = sigs
	s.Output.NewAttributes = attrs
	s.Output.Tests = tests
	s.UpdatedAt = time.Now().UTC()
	if err := Save(dir, s); err != nil {
		return nil, err
	}
	return s, nil
}

func appendUnique(dst, src []string) []string {
	seen := map[string]bool{}
	for _, x := range dst {
		seen[x] = true
	}
	for _, x := range src {
		if seen[x] {
			continue
		}
		dst = append(dst, x)
		seen[x] = true
	}
	return dst
}
