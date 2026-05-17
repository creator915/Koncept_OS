// Package ledger implements the Storage system from 屎山代码维护Agent
// 设计文档 v1.0 Part 8.3 / Part 10.4. The backing-store CHOICE was
// 11-level "[决定于落地阶段]";落地 is now — and the doc's own simplest
// option (JSON, git-like) is chosen, consistent with kcpos's existing
// .kcpos/ JSON-bundle convention so the client can read/back-up the
// ledger WITHOUT the agent (Part 10.3/10.4).
//
// Properties the doc requires (Part 8.3):
//   - git-like IMMUTABLE history — facts are appended, never mutated;
//     a change is a new fact that Supersedes a prior one.
//   - tracks ASSUMPTION changes, not code diffs (Part 10.4).
//   - branch create / lineage.
//   - query "in branch X, what is property Y's Oracle".
//   - reverse query "which branches share assumption A".
//   - evidence invalidation propagation (Part 2.4b): invalidating an
//     evidence flags every Oracle that referenced it.
package ledger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// FactKind enumerates the immutable record kinds.
type FactKind string

const (
	FactAssumption        FactKind = "assumption"
	FactOracle            FactKind = "oracle"
	FactEvidenceInvalidate FactKind = "evidence-invalidate"
	FactBranch            FactKind = "branch"
)

// Fact is one immutable, append-only record (设计文档 Part 8.3 "类似
// git 的不可变历史"). A revision is a NEW fact whose Supersedes points
// at the prior one — the old fact stays in history forever.
type Fact struct {
	Seq        int             `json:"seq"`
	Kind       FactKind        `json:"kind"`
	EntityID   string          `json:"entityId"` // assumption id / oracle id / property id / branch id
	Branch     string          `json:"branch"`
	Body       json.RawMessage `json:"body,omitempty"`
	Supersedes int             `json:"supersedes,omitempty"` // prior fact Seq this revises (0 = none)
	// For oracle facts: what it is conditional on / references — drives
	// the two required reverse queries without re-parsing Body.
	PropertyID    string   `json:"propertyId,omitempty"`
	ConditionalOn []string `json:"conditionalOn,omitempty"`
	EvidenceRefs  []string `json:"evidenceRefs,omitempty"`
	Invalidated   bool     `json:"invalidated,omitempty"` // set on oracle facts hit by evidence invalidation
	At            time.Time `json:"at"`
}

// Branch is a versioned assumption container (设计文档 Part 2.2).
type Branch struct {
	ID               string   `json:"id"`
	Parent           string   `json:"parent,omitempty"`
	ActiveAssumptions []string `json:"activeAssumptions"`
	WorkingLayer     string   `json:"workingLayer"`
	Reason           string   `json:"reason,omitempty"`
}

// Ledger is the append-only store. Persisted as one JSON file
// (atomic temp+rename, mirroring core.SaveBundle).
type Ledger struct {
	Path     string            `json:"-"`
	Facts    []Fact            `json:"facts"`
	Branches map[string]Branch `json:"branches"`
}

// Open loads the ledger at path, or initializes an empty one with a
// root branch at the default BusinessLogic layer (设计文档 Part 3.1).
func Open(path string) (*Ledger, error) {
	l := &Ledger{Path: path, Branches: map[string]Branch{}}
	raw, err := os.ReadFile(path)
	if err == nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, l); err != nil {
			return nil, fmt.Errorf("ledger: corrupt store %s: %w", path, err)
		}
		l.Path = path
		if l.Branches == nil {
			l.Branches = map[string]Branch{}
		}
		return l, nil
	}
	l.Branches["root"] = Branch{ID: "root", ActiveAssumptions: []string{}, WorkingLayer: "BusinessLogic"}
	return l, nil
}

func (l *Ledger) save() error {
	if l.Path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	tmp := l.Path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, l.Path)
}

// append assigns the next seq, stamps time, records, persists.
func (l *Ledger) append(f Fact) (Fact, error) {
	f.Seq = len(l.Facts) + 1
	if f.At.IsZero() {
		f.At = time.Now().UTC()
	}
	l.Facts = append(l.Facts, f)
	return f, l.save()
}

// RecordAssumption appends an assumption fact (or a revision that
// Supersedes a prior seq; pass 0 for a new assumption).
func (l *Ledger) RecordAssumption(branch, assumptionID string, body any, supersedes int) (Fact, error) {
	b, _ := json.Marshal(body)
	return l.append(Fact{Kind: FactAssumption, EntityID: assumptionID, Branch: branch, Body: b, Supersedes: supersedes})
}

// RecordOracle appends an oracle fact. propertyID + conditionalOn +
// evidenceRefs power the two required queries and invalidation.
func (l *Ledger) RecordOracle(branch, oracleID, propertyID string, conditionalOn, evidenceRefs []string, body any, supersedes int) (Fact, error) {
	b, _ := json.Marshal(body)
	return l.append(Fact{
		Kind: FactOracle, EntityID: oracleID, Branch: branch, Body: b,
		PropertyID: propertyID, ConditionalOn: conditionalOn, EvidenceRefs: evidenceRefs,
		Supersedes: supersedes,
	})
}

// Fork creates a child branch inheriting the parent's active
// assumptions (设计文档 Part 2.2 / Part 3.1 fork). Recorded as a fact
// so the branch creation is itself in the immutable history.
func (l *Ledger) Fork(parent, child, workingLayer, reason string) (Branch, error) {
	p, ok := l.Branches[parent]
	if !ok {
		return Branch{}, fmt.Errorf("ledger: parent branch %q not found", parent)
	}
	inherited := append([]string{}, p.ActiveAssumptions...)
	if workingLayer == "" {
		workingLayer = p.WorkingLayer
	}
	nb := Branch{ID: child, Parent: parent, ActiveAssumptions: inherited, WorkingLayer: workingLayer, Reason: reason}
	l.Branches[child] = nb
	if _, err := l.append(Fact{Kind: FactBranch, EntityID: child, Branch: child, Supersedes: 0}); err != nil {
		return Branch{}, err
	}
	return nb, nil
}

// branchLineage returns child→…→root.
func (l *Ledger) branchLineage(branch string) []string {
	var chain []string
	for cur := branch; cur != ""; {
		chain = append(chain, cur)
		b, ok := l.Branches[cur]
		if !ok {
			break
		}
		cur = b.Parent
	}
	return chain
}

// OracleForProperty answers "in branch X, what is property Y's Oracle"
// (设计文档 Part 8.3). Walks the branch lineage, returns the LATEST
// non-superseded, non-invalidated oracle fact for that property.
func (l *Ledger) OracleForProperty(branch, propertyID string) (Fact, bool) {
	inScope := map[string]bool{}
	for _, b := range l.branchLineage(branch) {
		inScope[b] = true
	}
	// Supersession is BRANCH-SCOPED: a revision only counts where the
	// superseding fact is visible. From a parent branch, a child's
	// revision does not exist, so the parent still resolves its own
	// oracle (git-like: a commit on a branch you can't see can't
	// supersede yours).
	superseded := map[int]bool{}
	for _, f := range l.Facts {
		if f.Supersedes > 0 && inScope[f.Branch] {
			superseded[f.Supersedes] = true
		}
	}
	var best Fact
	found := false
	for _, f := range l.Facts {
		if f.Kind != FactOracle || f.PropertyID != propertyID || !inScope[f.Branch] {
			continue
		}
		if superseded[f.Seq] || f.Invalidated {
			continue
		}
		if !found || f.Seq > best.Seq {
			best, found = f, true
		}
	}
	return best, found
}

// BranchesSharingAssumption answers the reverse query "which branches
// share assumption A" (设计文档 Part 8.3) — over the live Branch set.
func (l *Ledger) BranchesSharingAssumption(assumptionID string) []string {
	var out []string
	for id, b := range l.Branches {
		for _, a := range b.ActiveAssumptions {
			if a == assumptionID {
				out = append(out, id)
				break
			}
		}
	}
	return out
}

// SetBranchAssumptions overwrites a branch's active assumption set
// (the change is observable via BranchesSharingAssumption; the prior
// state remains in the FactBranch history).
func (l *Ledger) SetBranchAssumptions(branch string, assumptions []string) error {
	b, ok := l.Branches[branch]
	if !ok {
		return fmt.Errorf("ledger: branch %q not found", branch)
	}
	b.ActiveAssumptions = append([]string{}, assumptions...)
	l.Branches[branch] = b
	return l.save()
}

// InvalidateEvidence appends an invalidation fact and flags every
// oracle fact that referenced it (设计文档 Part 2.4b "Evidence 失效的
// 传播": 遍历 invalidates_oracles，受影响 Oracle 重新计算/失效).
// Returns the affected oracle ids. Facts are never deleted — the
// invalidation is itself a new immutable fact.
func (l *Ledger) InvalidateEvidence(evidenceID, reason string) ([]string, error) {
	affected := []string{}
	for i := range l.Facts {
		f := &l.Facts[i]
		if f.Kind != FactOracle || f.Invalidated {
			continue
		}
		for _, ref := range f.EvidenceRefs {
			if ref == evidenceID {
				f.Invalidated = true
				affected = append(affected, f.EntityID)
				break
			}
		}
	}
	body, _ := json.Marshal(map[string]string{"evidence": evidenceID, "reason": reason})
	if _, err := l.append(Fact{Kind: FactEvidenceInvalidate, EntityID: evidenceID, Body: body}); err != nil {
		return nil, err
	}
	return affected, nil
}
