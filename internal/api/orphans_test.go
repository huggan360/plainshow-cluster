package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/store"
)

// TestOnlyUnclaimedFoldersAreOffered is the safety property here. Offering a
// live project's folder for deletion would be a one-click way to destroy the
// work somebody is doing.
func TestOnlyUnclaimedFoldersAreOffered(t *testing.T) {
	srv, _ := newTestServer(t)
	root := srv.layout.Projects()

	live := store.Project{ID: "p1", Name: "vision"}
	if err := srv.store.CreateProject(&live); err != nil {
		t.Fatal(err)
	}
	makeFolder(t, filepath.Join(root, "vision"))
	makeFolder(t, filepath.Join(root, "abandoned"))
	// A download in progress is not abandoned.
	makeFolder(t, filepath.Join(root, "arriving.downloading"))

	names := map[string]bool{}
	for _, item := range srv.findOrphans() {
		names[item.Name] = true
	}
	if names["vision"] {
		t.Error("a live project's folder was offered for deletion")
	}
	if names["arriving.downloading"] {
		t.Error("a download in progress was offered for deletion")
	}
	if !names["abandoned"] {
		t.Errorf("the abandoned folder was not found: %v", names)
	}
}

// TestDeletingOrphansIgnoresCallerPaths: the request names folders, and the
// server re-derives what they are. Taking a path from the caller would turn
// this into a way to delete anything the node can reach.
func TestDeletingOrphansIgnoresCallerPaths(t *testing.T) {
	srv, _ := newTestServer(t)
	root := srv.layout.Projects()
	makeFolder(t, filepath.Join(root, "abandoned"))

	outside := filepath.Join(t.TempDir(), "precious")
	makeFolder(t, outside)

	// findOrphans is the only source of truth for what may be removed, so a
	// name that is not one of them removes nothing.
	found := srv.findOrphans()
	if len(found) != 1 || found[0].Name != "abandoned" {
		t.Fatalf("orphans = %+v", found)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("precondition: the outside directory should exist")
	}
	if _, err := os.Stat(found[0].Path); err != nil {
		t.Fatal(err)
	}
}

func makeFolder(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
