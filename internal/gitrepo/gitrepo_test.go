package gitrepo

import (
	"os/exec"
	"testing"
)

func TestRepositoryFromURL(t *testing.T) {
	tests := map[string]string{
		"https://github.com/plainshow/cluster.git": "plainshow/cluster",
		"git@github.com:plainshow/cluster.git":     "plainshow/cluster",
		"https://example.com/plainshow/cluster":    "",
		"not a url":                                "",
	}
	for input, want := range tests {
		if got := RepositoryFromURL(input); got != want {
			t.Errorf("RepositoryFromURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRemoveRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	repo := Open(dir)
	if err := repo.Init(); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetRemote("plainshow/cluster"); err != nil {
		t.Fatal(err)
	}
	if got := repo.RemoteRepository(); got != "plainshow/cluster" {
		t.Fatalf("remote = %q", got)
	}
	if err := repo.RemoveRemote(); err != nil {
		t.Fatal(err)
	}
	if got := repo.Remote("origin"); got != "" {
		t.Fatalf("origin survived removal: %q", got)
	}
}
