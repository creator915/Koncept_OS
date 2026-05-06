package session

import (
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
		got, err := NormalizeID(c.in)
		if c.wantErr && err == nil {
			t.Errorf("NormalizeID(%q) expected error, got %q", c.in, got)
		}
		if !c.wantErr && err != nil {
			t.Errorf("NormalizeID(%q) unexpected error: %v", c.in, err)
		}
		if !c.wantErr && got != c.want {
			t.Errorf("NormalizeID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTransition_LinearProgression(t *testing.T) {
	s := New("s_x", "", "task", Input{})
	if s.Status != StatusWaiting {
		t.Fatalf("new session should be waiting, got %s", s.Status)
	}
	if err := s.Transition(StatusActive); err != nil {
		t.Fatalf("waiting → active: %v", err)
	}
	if err := s.Transition(StatusFinished); err != nil {
		t.Fatalf("active → finished: %v", err)
	}
}

func TestTransition_InvalidMoves(t *testing.T) {
	s := New("s_x", "", "task", Input{})
	// waiting → finished should fail
	if err := s.Transition(StatusFinished); err == nil {
		t.Errorf("waiting → finished should be invalid")
	}
	s.Status = StatusFinished
	if err := s.Transition(StatusActive); err == nil {
		t.Errorf("finished → active should be invalid")
	}
	if err := s.Transition(StatusWaiting); err == nil {
		t.Errorf("finished → waiting should be invalid")
	}
}

func TestCreateAndLoad(t *testing.T) {
	dir := t.TempDir()
	created, err := Create(dir, "s_root", "", "root task", Input{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	loaded, err := Load(dir, "s_root")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ID != created.ID || loaded.Task != "root task" || loaded.Status != StatusWaiting {
		t.Errorf("loaded mismatch: %+v", loaded)
	}
}

func TestCreate_DuplicateRejected(t *testing.T) {
	dir := t.TempDir()
	if _, err := Create(dir, "s_x", "", "t", Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir, "s_x", "", "t", Input{}); err == nil {
		t.Error("duplicate Create should error")
	}
}

func TestCreate_ParentMustExist(t *testing.T) {
	dir := t.TempDir()
	if _, err := Create(dir, "s_child", "s_missing", "t", Input{}); err == nil {
		t.Error("parent missing should error")
	}
}

func TestCreate_AttachesToParentChildren(t *testing.T) {
	dir := t.TempDir()
	if _, err := Create(dir, "s_root", "", "root", Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir, "s_a", "s_root", "child a", Input{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir, "s_b", "s_root", "child b", Input{}); err != nil {
		t.Fatal(err)
	}
	root, err := Load(dir, "s_root")
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
	if Exists(dir, "s_mid") || Exists(dir, "s_leaf1") || Exists(dir, "s_leaf2") {
		t.Error("subtree files should be gone")
	}
	if !Exists(dir, "s_root") || !Exists(dir, "s_sib") {
		t.Error("untouched siblings should remain")
	}
	root, _ := Load(dir, "s_root")
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
	ids, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"s_a", "s_b", "s_c"}
	if !equalSlices(ids, want) {
		t.Errorf("List = %v, want %v", ids, want)
	}
}

func TestList_EmptyDirNoError(t *testing.T) {
	dir := t.TempDir() + "/nope"
	ids, err := List(dir)
	if err != nil {
		t.Errorf("List on missing dir should not error: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected empty, got %v", ids)
	}
}

func TestSetStatus_Persisted(t *testing.T) {
	dir := t.TempDir()
	mustCreate(t, dir, "s_x", "", "task")
	if _, err := SetStatus(dir, "s_x", StatusActive); err != nil {
		t.Fatal(err)
	}
	loaded, _ := Load(dir, "s_x")
	if loaded.Status != StatusActive {
		t.Errorf("status not persisted, got %s", loaded.Status)
	}
}

func TestSetStatus_FinishBlockedByUnfinishedChild(t *testing.T) {
	dir := t.TempDir()
	mustCreate(t, dir, "s_root", "", "root")
	mustCreate(t, dir, "s_child", "s_root", "child")
	mustSet(t, dir, "s_root", StatusActive)
	mustSet(t, dir, "s_child", StatusActive)

	if _, err := SetStatus(dir, "s_root", StatusFinished); err == nil {
		t.Error("finishing parent with active child should error")
	}

	mustSet(t, dir, "s_child", StatusFinished)
	if _, err := SetStatus(dir, "s_root", StatusFinished); err != nil {
		t.Errorf("after child finished, parent should finish: %v", err)
	}
}

func TestSetStatus_FinishOKWhenChildDeleted(t *testing.T) {
	dir := t.TempDir()
	mustCreate(t, dir, "s_root", "", "root")
	mustCreate(t, dir, "s_child", "s_root", "child")
	mustSet(t, dir, "s_root", StatusActive)
	if _, err := DeleteRecursive(dir, "s_child"); err != nil {
		t.Fatal(err)
	}
	// After delete, root.Children no longer contains s_child, so finish should pass.
	if _, err := SetStatus(dir, "s_root", StatusFinished); err != nil {
		t.Errorf("after child deleted, parent should finish: %v", err)
	}
}

func mustSet(t *testing.T, dir, id string, to Status) {
	t.Helper()
	if _, err := SetStatus(dir, id, to); err != nil {
		t.Fatalf("SetStatus(%s, %s): %v", id, to, err)
	}
}

// helpers

func mustCreate(t *testing.T, dir, id, parent, task string) {
	t.Helper()
	if _, err := Create(dir, id, parent, task, Input{}); err != nil {
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
