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
