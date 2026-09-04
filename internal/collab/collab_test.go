package collab

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/store"
)

func TestConcurrentInsertTransforms(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := New(st)
	written := ""
	write := func(content string) error { written = content; return nil }
	first, err := m.Apply(Operation{NetworkID: "n", ProjectID: "p", Path: "a", ClientID: "a", Base: 0, From: 1, To: 1, Insert: "X"}, "ab", write)
	if err != nil || first.Revision != 1 {
		t.Fatal(first, err)
	}
	_, err = m.Apply(Operation{NetworkID: "n", ProjectID: "p", Path: "a", ClientID: "b", Base: 0, From: 2, To: 2, Insert: "Y"}, "ab", write)
	if err != nil {
		t.Fatal(err)
	}
	if written != "aXbY" {
		t.Fatalf("got %q", written)
	}
	snap, err := m.Open("n", "p", "a", "")
	if err != nil || snap.Revision != 2 || snap.Content != written {
		t.Fatal(snap, err)
	}
}

func TestConcurrentSamePositionInsertsConvergeAcrossClones(t *testing.T) {
	applyInOrder := func(t *testing.T, first, second Operation) string {
		t.Helper()
		st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		manager := New(st)
		content := "xy"
		write := func(next string) error { content = next; return nil }
		if _, err := manager.Apply(first, content, write); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Apply(second, content, write); err != nil {
			t.Fatal(err)
		}
		return content
	}
	a := Operation{NetworkID: "n", ProjectID: "p", Path: "a", ClientID: "alpha",
		Sequence: 1, Base: 0, From: 1, To: 1, Insert: "A"}
	b := Operation{NetworkID: "n", ProjectID: "p", Path: "a", ClientID: "bravo",
		Sequence: 1, Base: 0, From: 1, To: 1, Insert: "B"}
	left := applyInOrder(t, a, b)
	right := applyInOrder(t, b, a)
	if left != "xABy" || right != left {
		t.Fatalf("clones diverged: %q != %q", left, right)
	}
}

func TestDuplicateControllerDeliveryIsIgnored(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := New(st)
	op := Operation{NetworkID: "n", ProjectID: "p", Path: "a", ClientID: "browser",
		Sequence: 7, Base: 0, From: 1, To: 1, Insert: "X"}
	if _, err := m.Apply(op, "ab", func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Apply(op, "aXb", func(string) error { return nil }); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate result = %v", err)
	}
	snapshot, err := m.Open("n", "p", "a", "")
	if err != nil || snapshot.Content != "aXb" || snapshot.Revision != 1 {
		t.Fatalf("duplicate changed document: %+v, %v", snapshot, err)
	}
}

func TestResetUsesReplacementFromDisk(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := New(st)
	if _, err := m.Apply(Operation{NetworkID: "n", ProjectID: "p", Path: "a.txt",
		ClientID: "browser", Sequence: 1, From: 0, To: 0, Insert: "old"}, "", func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := m.Reset("n", "p", "a.txt"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.Open("n", "p", "a.txt", "replacement")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Content != "replacement" || snapshot.Revision != 0 {
		t.Fatalf("snapshot after reset = %+v", snapshot)
	}
}

func TestResetTreeForgetsNestedDocumentsOnly(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := New(st)
	for _, path := range []string{"src/a.py", "src/deep/b.py", "src-old/keep.py"} {
		if _, err := m.Apply(Operation{NetworkID: "n", ProjectID: "p", Path: path,
			ClientID: path, Sequence: 1, From: 0, To: 0, Insert: "cached"}, "", func(string) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.ResetTree("n", "p", "src"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"src/a.py", "src/deep/b.py"} {
		snapshot, err := m.Open("n", "p", path, "disk")
		if err != nil || snapshot.Content != "disk" || snapshot.Revision != 0 {
			t.Fatalf("%s survived tree reset: %+v, %v", path, snapshot, err)
		}
	}
	kept, err := m.Open("n", "p", "src-old/keep.py", "disk")
	if err != nil || kept.Content != "cached" || kept.Revision != 1 {
		t.Fatalf("sibling was reset: %+v, %v", kept, err)
	}
}

func TestWriteFailureLeavesDurableContentRecoverable(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := New(st)
	wantErr := errors.New("disk unavailable")
	op := Operation{NetworkID: "n", ProjectID: "p", Path: "a.txt",
		ClientID: "browser", Sequence: 1, From: 0, To: 0, Insert: "safe"}
	if _, err := m.Apply(op, "", func(string) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("write error = %v", err)
	}
	// A fresh manager models a daemon restart. SQLite remains the recovery
	// source even though the project filesystem rejected the write.
	restarted := New(st)
	snapshot, err := restarted.Open("n", "p", "a.txt", "")
	if err != nil || snapshot.Content != "safe" || snapshot.Revision != 1 {
		t.Fatalf("recovered snapshot = %+v, %v", snapshot, err)
	}
}
