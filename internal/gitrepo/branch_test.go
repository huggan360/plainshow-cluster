package gitrepo

import (
	"os"
	"path/filepath"
	"testing"
)

func seeded(t *testing.T) Repo {
	t.Helper()
	repo := Repo{Dir: t.TempDir()}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
	} {
		if _, err := repo.run(args...); err != nil {
			t.Skipf("git unavailable: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo.Dir, "main.py"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, repo, "add", "-A")
	mustRun(t, repo, "commit", "-m", "first")
	return repo
}

func TestBranchesListsTheCurrentOneFirst(t *testing.T) {
	repo := seeded(t)
	if err := repo.CreateBranch("dev"); err != nil {
		t.Fatal(err)
	}
	// Creating a branch must not move to it: the working tree somebody is
	// looking at has to stay where it was.
	if repo.Branch() != "main" {
		t.Fatalf("creating a branch moved to it: now on %s", repo.Branch())
	}
	names, err := repo.Branches()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "main" {
		t.Fatalf("branches = %v, want main first", names)
	}
	if !repo.HasBranch("dev") || repo.HasBranch("nope") {
		t.Error("HasBranch disagrees with what was created")
	}
	// Creating one that exists is how "push to dev" behaves the second time.
	if err := repo.CreateBranch("dev"); err != nil {
		t.Errorf("re-creating an existing branch failed: %v", err)
	}
}

// TestCloneLocalGivesABranchItsOwnTree is what makes two branches usable at
// once. Checking out to switch would throw away uncommitted work and pull the
// files out from under anything running.
func TestCloneLocalGivesABranchItsOwnTree(t *testing.T) {
	source := seeded(t)
	if err := source.SetRemote("huggan360/vision"); err != nil {
		t.Fatal(err)
	}
	if err := source.CreateBranch("dev"); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "vision@dev")
	if err := CloneLocal(source.Dir, target, "dev"); err != nil {
		t.Fatal(err)
	}

	clone := Open(target)
	if got := clone.Branch(); got != "dev" {
		t.Fatalf("clone is on %q, want dev", got)
	}
	if _, err := os.Stat(filepath.Join(target, "main.py")); err != nil {
		t.Errorf("the clone has no files: %v", err)
	}
	// The remote has to come across, or the new folder can never push or pull.
	if got := clone.RemoteRepository(); got != "huggan360/vision" {
		t.Errorf("clone remote = %q, want the source's", got)
	}
	// And the original is untouched, still on its own branch.
	if got := source.Branch(); got != "main" {
		t.Errorf("cloning moved the source to %q", got)
	}
}

func TestPushToNeedsABranchName(t *testing.T) {
	repo := seeded(t)
	if _, err := repo.PushTo("token", "  "); err == nil {
		t.Fatal("an empty branch name was accepted")
	}
}
