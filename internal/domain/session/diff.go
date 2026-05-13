package session

import (
	"encoding/json"

	"github.com/creator915/Koncept_OS/internal/domain/graph"
)

// ApplyDiff is the PURE in-memory diff merge. Given a session and the
// before/after graph snapshots, mutates s.Output.GraphDiff to record
// what changed. No I/O.
//
// v9.3.2 refactor: separated from the I/O-bearing CaptureDiff (which
// lives in app/workflow). CaptureDiff is the orchestrator: read focus,
// load session, call ApplyDiff, save session.
//
// Merge semantics across multiple captures within the same session:
//   - added: a new id present in `after` but not `before` is recorded;
//     if a later capture shows the same id was modified, the snapshot
//     in `added` is updated to the latest `after`.
//   - modified: tracked once; on subsequent captures the original
//     `before` is preserved and `after` is replaced with the newest
//     state.
//   - removed: ids present in `before` but not `after` go to `removed`.
//     If the same id was tracked as added in this session, it is
//     removed from `added` (net: nothing happened).
func ApplyDiff(s *Session, before, after *graph.Graph) {
	mergeAttrDiff(s, before, after)
	mergeObjectDiff(s, before, after)
}

func mergeAttrDiff(s *Session, before, after *graph.Graph) {
	d := &s.Output.GraphDiff

	// Added: in after, not in before.
	for id, a := range after.Attributes {
		if _, was := before.Attributes[id]; was {
			continue
		}
		raw, _ := json.Marshal(a)
		// If id was queued for removal earlier, that cancels out — net no change.
		if idx := indexOf(d.Removed.Attributes, id); idx >= 0 {
			d.Removed.Attributes = removeAt(d.Removed.Attributes, idx)
			continue
		}
		d.Added.Attributes[id] = raw
	}

	// Removed: in before, not in after.
	for id := range before.Attributes {
		if _, is := after.Attributes[id]; is {
			continue
		}
		// If this id was added in this session, it's a net no-op.
		if _, wasAdded := d.Added.Attributes[id]; wasAdded {
			delete(d.Added.Attributes, id)
			continue
		}
		// If it was modified in this session, drop the modified record (we now have a clean removal).
		delete(d.Modified.Attributes, id)
		if indexOf(d.Removed.Attributes, id) < 0 {
			d.Removed.Attributes = append(d.Removed.Attributes, id)
		}
	}

	// Modified: in both, but content differs.
	for id, afterA := range after.Attributes {
		beforeA, was := before.Attributes[id]
		if !was {
			continue
		}
		if attributeEqual(beforeA, afterA) {
			continue
		}
		// If tracked as added in this session, just refresh the snapshot.
		if _, isAdded := d.Added.Attributes[id]; isAdded {
			raw, _ := json.Marshal(afterA)
			d.Added.Attributes[id] = raw
			continue
		}
		// If already modified earlier in this session, keep the original "before" but update "after".
		if existing, isMod := d.Modified.Attributes[id]; isMod {
			afterRaw, _ := json.Marshal(afterA)
			existing.After = afterRaw
			d.Modified.Attributes[id] = existing
			continue
		}
		// First-time modification in this session.
		beforeRaw, _ := json.Marshal(beforeA)
		afterRaw, _ := json.Marshal(afterA)
		d.Modified.Attributes[id] = ModifiedRecord{Before: beforeRaw, After: afterRaw}
	}
}

func mergeObjectDiff(s *Session, before, after *graph.Graph) {
	d := &s.Output.GraphDiff

	for id, o := range after.Objects {
		if _, was := before.Objects[id]; was {
			continue
		}
		raw, _ := json.Marshal(o)
		if idx := indexOf(d.Removed.Objects, id); idx >= 0 {
			d.Removed.Objects = removeAt(d.Removed.Objects, idx)
			continue
		}
		d.Added.Objects[id] = raw
	}

	for id := range before.Objects {
		if _, is := after.Objects[id]; is {
			continue
		}
		if _, wasAdded := d.Added.Objects[id]; wasAdded {
			delete(d.Added.Objects, id)
			continue
		}
		delete(d.Modified.Objects, id)
		if indexOf(d.Removed.Objects, id) < 0 {
			d.Removed.Objects = append(d.Removed.Objects, id)
		}
	}

	for id, afterO := range after.Objects {
		beforeO, was := before.Objects[id]
		if !was {
			continue
		}
		if objectEqual(beforeO, afterO) {
			continue
		}
		if _, isAdded := d.Added.Objects[id]; isAdded {
			raw, _ := json.Marshal(afterO)
			d.Added.Objects[id] = raw
			continue
		}
		if existing, isMod := d.Modified.Objects[id]; isMod {
			afterRaw, _ := json.Marshal(afterO)
			existing.After = afterRaw
			d.Modified.Objects[id] = existing
			continue
		}
		beforeRaw, _ := json.Marshal(beforeO)
		afterRaw, _ := json.Marshal(afterO)
		d.Modified.Objects[id] = ModifiedRecord{Before: beforeRaw, After: afterRaw}
	}
}

func attributeEqual(a, b *graph.Attribute) bool {
	aData, _ := json.Marshal(a)
	bData, _ := json.Marshal(b)
	return string(aData) == string(bData)
}

func objectEqual(a, b *graph.Object) bool {
	aData, _ := json.Marshal(a)
	bData, _ := json.Marshal(b)
	return string(aData) == string(bData)
}

func indexOf(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}

func removeAt(xs []string, i int) []string {
	return append(xs[:i], xs[i+1:]...)
}
