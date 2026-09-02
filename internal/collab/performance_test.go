package collab

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/store"
)

// TestKeystrokeCostDoesNotGrowWithHistory guards the reason the operation log
// exists.
//
// Edits were once persisted by rewriting the document's whole history as one
// JSON blob, so a keystroke cost more the longer the session had run: about
// 5 ms at ten edits and over 30 ms at a thousand, heading for 100 ms at the
// retention cap. Typing is the most latency-sensitive thing this product does,
// so the cost has to be flat.
func TestKeystrokeCostDoesNotGrowWithHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "perf.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m := New(st)
	content := "x"
	write := func(next string) error { content = next; return nil }

	measure := func(from, count int) time.Duration {
		start := time.Now()
		for i := from; i < from+count; i++ {
			op := Operation{NetworkID: "n", ProjectID: "p", Path: "a.py",
				Base: int64(i), From: 0, To: 0, Insert: "a"}
			if _, err := m.Apply(op, content, write); err != nil {
				t.Fatalf("edit %d: %v", i, err)
			}
		}
		return time.Since(start) / time.Duration(count)
	}

	early := measure(0, 200)
	measure(200, 800) // build up a long history
	late := measure(1000, 200)

	t.Logf("early %v/edit, late %v/edit (after 1000 edits)",
		early.Round(time.Microsecond), late.Round(time.Microsecond))

	// Flat, not linear. A generous ceiling: this runs on shared CI and on a
	// Raspberry Pi, so it must catch a regression in shape without failing on
	// ordinary noise.
	if late > early*3 && late > 5*time.Millisecond {
		t.Errorf("keystroke cost grew with history: %v early, %v late — "+
			"the per-edit write is no longer O(1)", early, late)
	}
}

// TestHistorySurvivesReopen: a reconnecting client is reconciled against
// operations that outlived the process, so the log must be replayable.
func TestHistorySurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	content := "abc"
	write := func(next string) error { content = next; return nil }

	m := New(st)
	for i := 0; i < 5; i++ {
		op := Operation{NetworkID: "n", ProjectID: "p", Path: "a.py",
			Base: int64(i), From: 0, To: 0, Insert: "z"}
		if _, err := m.Apply(op, content, write); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()

	reopened, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	// A fresh manager, as after a restart. An edit based on an older revision
	// must still be transformable, which needs the history back.
	fresh := New(reopened)
	stale := Operation{NetworkID: "n", ProjectID: "p", Path: "a.py",
		Base: 2, From: 0, To: 0, Insert: "!"}
	out, err := fresh.Apply(stale, content, write)
	if err != nil {
		t.Fatalf("a client reconnecting after a restart was refused: %v", err)
	}
	if out.Revision != 6 {
		t.Errorf("revision continued at %d, want 6 — history was lost", out.Revision)
	}
}

// TestRetentionTrimsTheLog: the log must not grow without bound.
func TestRetentionTrimsTheLog(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a lot of rows")
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "trim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m := New(st)
	content := "x"
	write := func(next string) error { content = next; return nil }

	total := historyLimit + trimEvery + 50
	for i := 0; i < total; i++ {
		op := Operation{NetworkID: "n", ProjectID: "p", Path: "a.py",
			Base: int64(i), From: 0, To: 0, Insert: "a"}
		if _, err := m.Apply(op, content, write); err != nil {
			t.Fatal(err)
		}
	}
	kept, err := st.CollabOperations("n", "p", "a.py", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) > historyLimit+trimEvery {
		t.Errorf("kept %d operations after %d edits; retention is not trimming",
			len(kept), total)
	}
	t.Logf("kept %d operations after %d edits", len(kept), total)
}
