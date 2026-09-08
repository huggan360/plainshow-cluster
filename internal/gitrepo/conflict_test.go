package gitrepo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// conflictedRepo builds a real repository whose merge has actually failed, so
// these test git's behaviour rather than a guess about it.
func conflictedRepo(t *testing.T) Repo {
	t.Helper()
	dir := t.TempDir()
	repo := Repo{Dir: dir}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		if _, err := repo.run(args...); err != nil {
			t.Skipf("git unavailable: %v", err)
		}
	}
	write := func(text string) {
		if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("base\n")
	mustRun(t, repo, "add", "-A")
	mustRun(t, repo, "commit", "-m", "base")
	mustRun(t, repo, "checkout", "-b", "incoming")
	write("theirs\n")
	mustRun(t, repo, "add", "-A")
	mustRun(t, repo, "commit", "-m", "theirs")
	mustRun(t, repo, "checkout", "main")
	write("mine\n")
	mustRun(t, repo, "add", "-A")
	mustRun(t, repo, "commit", "-m", "mine")
	// Expected to fail: that failure is the state under test.
	_, _ = repo.run("merge", "incoming")
	if !repo.InMerge() {
		t.Fatal("the merge did not conflict, so there is nothing to resolve")
	}
	return repo
}

func mustRun(t *testing.T, repo Repo, args ...string) {
	t.Helper()
	if _, err := repo.run(args...); err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
}

func read(t *testing.T, repo Repo) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repo.Dir, "main.py"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestKeepingEitherSideResolvesTheMerge. "ours" and "theirs" invert during a
// rebase, so this pins which one the interface's words map to on a merge —
// getting it backwards would silently discard whichever version somebody meant
// to keep.
func TestKeepingEitherSideResolvesTheMerge(t *testing.T) {
	for _, item := range []struct{ choice, want string }{
		{KeepMine, "mine\n"},
		{KeepTheirs, "theirs\n"},
	} {
		repo := conflictedRepo(t)
		if err := repo.ResolveConflict("main.py", item.choice); err != nil {
			t.Fatalf("%s: %v", item.choice, err)
		}
		if got := read(t, repo); got != item.want {
			t.Errorf("%s left %q, want %q", item.choice, got, item.want)
		}
		// Resolving has to stage as well as write: a file with the right
		// content that is still unmerged blocks the commit, and nothing on
		// screen would explain why.
		left, err := repo.Conflicts()
		if err != nil || len(left) != 0 {
			t.Errorf("%s left %v unresolved (%v)", item.choice, left, err)
		}
		if err := repo.FinishMerge(); err != nil {
			t.Errorf("%s could not complete the merge: %v", item.choice, err)
		}
	}
}

func TestDroppingAFileResolvesIt(t *testing.T) {
	repo := conflictedRepo(t)
	if err := repo.ResolveConflict("main.py", DropFile); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, "main.py")); !os.IsNotExist(err) {
		t.Error("the file survived being dropped")
	}
	if err := repo.FinishMerge(); err != nil {
		t.Fatal(err)
	}
}

// TestMarkingAFileWithMarkersIsRefused is the whole reason this screen exists.
// Conflict markers look like code and compile in some languages, so a file
// committed with them still in it appears merged and is wrong.
func TestMarkingAFileWithMarkersIsRefused(t *testing.T) {
	repo := conflictedRepo(t)
	if err := repo.MarkResolved("main.py"); err == nil {
		t.Fatal("a file full of conflict markers was accepted as resolved")
	}
	// Edited by hand into something real, it is accepted.
	if err := os.WriteFile(filepath.Join(repo.Dir, "main.py"), []byte("both\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkResolved("main.py"); err != nil {
		t.Fatal(err)
	}
	if err := repo.FinishMerge(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, repo); got != "both\n" {
		t.Errorf("committed %q", got)
	}
}

// TestFinishingWithWorkLeftIsRefused: completing a merge is the irreversible
// step, so it checks rather than trusting the interface to have.
func TestFinishingWithWorkLeftIsRefused(t *testing.T) {
	repo := conflictedRepo(t)
	if err := repo.FinishMerge(); err == nil {
		t.Fatal("a merge with unresolved files was committed")
	}
	repo2 := Repo{Dir: t.TempDir()}
	if _, err := repo2.run("init", "-b", "main"); err == nil {
		if err := repo2.FinishMerge(); err == nil {
			t.Error("a repository with no merge in progress reported success")
		}
	}
}

func TestUnknownResolutionIsRefused(t *testing.T) {
	repo := conflictedRepo(t)
	if err := repo.ResolveConflict("main.py", "whatever"); err == nil {
		t.Fatal("an unknown resolution was accepted")
	}
	if err := repo.ResolveConflict("", KeepMine); err == nil {
		t.Fatal("an empty path was accepted")
	}
}
