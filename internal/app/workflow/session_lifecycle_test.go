package workflow

import (
	"github.com/creator915/Koncept_OS/internal/domain/session"
	"github.com/creator915/Koncept_OS/internal/infra/persistence"
	"sort"
	"testing"
)

func TestNormalizeID(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"foo", "s_foo", false},
		{"s_foo", "s_foo", false},
		{"weather_proc", "s_weather_proc", false},
		{"  s_root  ", "s_root", false},
		{"", "", true},
		{"s_", "", true},          // empty after prefix
		{"S_foo", "", true},       // capital not allowed
		{"s_Foo", "", true},       // capital in body
		{"s_1foo", "", true},      // must start with letter
		{"s_foo-bar", "", true},   // dash not allowed
	}
	for _, c := range cases {
		got, err := session.NormalizeID(c.in)
		if c.wantErr && err == nil {
			t.Errorf("session.NormalizeID(%q) expected error, got %q", c.in, got)
		}
		if !c.wantErr && err != nil {
			t.Errorf("session.NormalizeID(%q) unexpected error: %v", c.in, err)
		}
		if !c.wantErr && got != c.want {
			t.Errorf("session.NormalizeID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTransition_LinearProgression(t *testing.T) {
	s := session.New("s_x", "", "task", session.Input{})
	if s.Status != session.StatusWaiting {
		t.Fatalf("new session should be waiting, got %s", s.Status)
	}
	if err := s.Transition(session.StatusActive); err != nil {
		t.Fatalf("waiting → active: %v", err)
	}
	if err := s.Transition(session.StatusFinished); err != nil {
		t.Fatalf("active → finished: %v", err)
	}
}

func TestTransition_InvalidMoves(t *testing.T) {
	s := session.New("s_x", "", "task", session.Input{})
	// waiting → finished should fail
	if err := s.Transition(session.StatusFinished); err == nil {
		t.Errorf("waiting → finished should be invalid")
	}
	s.Status = session.StatusFinished
	if err := s.Transition(session.StatusActive); err == nil {
		t.Errorf("finished → active should be invalid")
	}
	if err := s.Transition(session.StatusWaiting); err == nil {
		t.Errorf("finished → waiting should be invalid")
	}
}

func TestCreateAndLoad(t *testing.T) {
	dir := t.TempDir()
	created, err := Create(dir, "s_root", "", "root task", session.Input{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	loaded, err := persistence.LoadSession(dir, "s_root")
	if err != nil {
		t.Fatalf("persistence.Load: %v", err)
	}
	if loaded.ID != created.ID || loaded.Task != "root task" || loaded.Status != session.StatusWaiting {
		t.Errorf("loaded mismatch: %+v", loaded)
	}
}

func TestCreate_DuplicateRejected(t *testing.T) {
	dir := t.TempDir()
	if _, err := Create(dir, "s_x", "", "t", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir, "s_x", "", "t", session.Input{}); err == nil {
		t.Error("duplicate Create should error")
	}
}

func TestCreate_ParentMustExist(t *testing.T) {
	dir := t.TempDir()
	if _, err := Create(dir, "s_child", "s_missing", "t", session.Input{}); err == nil {
		t.Error("parent missing should error")
	}
}

func TestCreate_AttachesToParentChildren(t *testing.T) {
	dir := t.TempDir()
	if _, err := Create(dir, "s_root", "", "root", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir, "s_a", "s_root", "child a", session.Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir, "s_b", "s_root", "child b", session.Input{}); err != nil {
		t.Fatal(err)
	}
	root, err := persistence.LoadSession(dir, "s_root")
	if err != nil {
		t.Fatal(err)
	}
	got := append([]string(nil), root.Children...)
	sort.Strings(got)
	want := []string{"s_a", "s_b"}
	if !equalSlices(got, want) {
		t.Errorf("root children = %v, want %v", got, want)
	}
}

func TestDeleteRecursive_RemovesSubtreeAndDetachesParent(t *testing.T) {
	dir := t.TempDir()
	mustCreate(t, dir, "s_root", "", "root")
	mustCreate(t, dir, "s_mid", "s_root", "mid")
	mustCreate(t, dir, "s_leaf1", "s_mid", "leaf1")
	mustCreate(t, dir, "s_leaf2", "s_mid", "leaf2")
	mustCreate(t, dir, "s_sib", "s_root", "sibling")

	deleted, err := DeleteRecursive(dir, "s_mid")
	if err != nil {
		t.Fatal(err)
	}
	// Expect s_leaf1, s_leaf2, s_mid (in some leaf-then-parent order)
	sort.Strings(deleted)
	want := []string{"s_leaf1", "s_leaf2", "s_mid"}
	if !equalSlices(deleted, want) {
		t.Errorf("deleted = %v, want %v", deleted, want)
	}
	if persistence.ExistsSession(dir, "s_mid") || persistence.ExistsSession(dir, "s_leaf1") || persistence.ExistsSession(dir, "s_leaf2") {
		t.Error("subtree files should be gone")
	}
	if !persistence.ExistsSession(dir, "s_root") || !persistence.ExistsSession(dir, "s_sib") {
		t.Error("untouched siblings should remain")
	}
	root, _ := persistence.LoadSession(dir, "s_root")
	if contains(root.Children, "s_mid") {
		t.Errorf("root.Children still has s_mid: %v", root.Children)
	}
	if !contains(root.Children, "s_sib") {
		t.Errorf("root.Children missing s_sib: %v", root.Children)
	}
}

func TestDeleteRecursive_MissingIDIsNoOp(t *testing.T) {
	dir := t.TempDir()
	deleted, err := DeleteRecursive(dir, "s_ghost")
	if err != nil {
		t.Errorf("missing id should not error: %v", err)
	}
	if len(deleted) != 0 {
		t.Errorf("missing id should yield no deletions, got %v", deleted)
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	mustCreate(t, dir, "s_b", "", "b")
	mustCreate(t, dir, "s_a", "", "a")
	mustCreate(t, dir, "s_c", "", "c")
	ids, err := persistence.ListSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"s_a", "s_b", "s_c"}
	if !equalSlices(ids, want) {
		t.Errorf("persistence.List = %v, want %v", ids, want)
	}
}

func TestList_EmptyDirNoError(t *testing.T) {
	dir := t.TempDir() + "/nope"
	ids, err := persistence.ListSessions(dir)
	if err != nil {
		t.Errorf("persistence.List on missing dir should not error: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected empty, got %v", ids)
	}
}

func TestSetStatus_Persisted(t *testing.T) {
	dir := t.TempDir()
	mustCreate(t, dir, "s_x", "", "task")
	if _, err := SetStatus(dir, "s_x", session.StatusActive); err != nil {
		t.Fatal(err)
	}
	loaded, _ := persistence.LoadSession(dir, "s_x")
	if loaded.Status != session.StatusActive {
		t.Errorf("status not persisted, got %s", loaded.Status)
	}
}

func TestSetStatus_FinishBlockedByUnfinishedChild(t *testing.T) {
	dir := t.TempDir()
	mustCreate(t, dir, "s_root", "", "root")
	mustCreate(t, dir, "s_child", "s_root", "child")
	mustSet(t, dir, "s_root", session.StatusActive)
	mustSet(t, dir, "s_child", session.StatusActive)

	if _, err := SetStatus(dir, "s_root", session.StatusFinished); err == nil {
		t.Error("finishing parent with active child should error")
	}

	mustSet(t, dir, "s_child", session.StatusFinished)
	if _, err := SetStatus(dir, "s_root", session.StatusFinished); err != nil {
		t.Errorf("after child finished, parent should finish: %v", err)
	}
}

func TestSetStatus_FinishOKWhenChildDeleted(t *testing.T) {
	dir := t.TempDir()
	mustCreate(t, dir, "s_root", "", "root")
	mustCreate(t, dir, "s_child", "s_root", "child")
	mustSet(t, dir, "s_root", session.StatusActive)
	if _, err := DeleteRecursive(dir, "s_child"); err != nil {
		t.Fatal(err)
	}
	// After delete, root.Children no longer contains s_child, so finish should pass.
	if _, err := SetStatus(dir, "s_root", session.StatusFinished); err != nil {
		t.Errorf("after child deleted, parent should finish: %v", err)
	}
}

func mustSet(t *testing.T, dir, id string, to session.Status) {
	t.Helper()
	if _, err := SetStatus(dir, id, to); err != nil {
		t.Fatalf("SetStatus(%s, %s): %v", id, to, err)
	}
}

// helpers

func mustCreate(t *testing.T, dir, id, parent, task string) {
	t.Helper()
	if _, err := Create(dir, id, parent, task, session.Input{}); err != nil {
		t.Fatalf("Create %s: %v", id, err)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
