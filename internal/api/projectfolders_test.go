package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/store"
)

func TestRepositoryFolderMigrationKeepsFilesAndReadiness(t *testing.T) {
	s := syncingNode(t, true, true)
	p := store.Project{ID: "p-code", NetworkID: "net-1", Name: "display-alias", Repository: "owner/actual-repo"}
	if err := s.store.CreateProject(&p); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetProjectRepositoryID(p.ID, p.Repository); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(s.layout.Projects(), p.NetworkID, p.Name)
	if err := os.MkdirAll(old, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "main.py"), []byte("my code"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := s.MigrateProjectFolders(); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(s.projectDir(p)) != "actual-repo" {
		t.Fatal(s.projectDir(p))
	}
	if data, err := os.ReadFile(filepath.Join(s.projectDir(p), "main.py")); err != nil || string(data) != "my code" {
		t.Fatalf("lost file: %q %v", data, err)
	}
	if !s.hasProjectFiles(p.NetworkID, p.Name) {
		t.Fatal("renamed repository reported missing")
	}
	names := s.projectsOnDisk(p.NetworkID)
	if len(names) != 1 || names[0] != p.Name {
		t.Fatalf("readiness uses folder instead of project identity: %v", names)
	}
	if err := s.MigrateProjectFolders(); err != nil {
		t.Fatal("not idempotent:", err)
	}
}

func TestRepositoryFolderMigrationDoesNotOverwrite(t *testing.T) {
	s := syncingNode(t, true, true)
	p := store.Project{ID: "p", NetworkID: "net-1", Name: "alias", Repository: "owner/repo"}
	if err := s.store.CreateProject(&p); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetProjectRepositoryID(p.ID, p.Repository); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alias", "repo"} {
		if err := os.MkdirAll(filepath.Join(s.layout.Projects(), p.NetworkID, name), 0750); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MigrateProjectFolders(); err == nil {
		t.Fatal("collision was not reported")
	}
	if filepath.Base(s.projectDir(p)) != "alias" {
		t.Fatal("collision redirected the project to somebody else's folder")
	}
	for _, name := range []string{"alias", "repo"} {
		if _, err := os.Stat(filepath.Join(s.layout.Projects(), p.NetworkID, name)); err != nil {
			t.Fatal(err)
		}
	}
}
