package dataset

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

func TestRegisterDeduplicatesAndMaterializes(t *testing.T) {
	root := t.TempDir()
	layout, err := config.NewLayout(filepath.Join(root, "node"))
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(layout.Database())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	manager := New(layout, st)
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	payload := []byte("same content")
	for _, name := range []string{"a.txt", "nested/b.txt"} {
		if err := os.WriteFile(filepath.Join(source, name), payload, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	dataset, err := manager.Register("network", "node", "sample", "v1", source)
	if err != nil {
		t.Fatal(err)
	}
	if dataset.FileCount != 2 || dataset.SizeBytes != int64(len(payload)*2) {
		t.Fatal(dataset)
	}
	manifest, err := manager.Manifest(dataset)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Files[0].Chunks[0].Hash != manifest.Files[1].Chunks[0].Hash {
		t.Fatal("duplicate content was not deduplicated")
	}
	target := filepath.Join(root, "target")
	if err := manager.Materialize(dataset, target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(target, "nested", "b.txt"))
	if err != nil || string(got) != string(payload) {
		t.Fatalf("%q %v", got, err)
	}
}

func TestImportRejectsDamagedChunk(t *testing.T) {
	root := t.TempDir()
	layout, _ := config.NewLayout(filepath.Join(root, "node"))
	_ = layout.EnsureDirs()
	st, err := store.Open(layout.Database())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := New(layout, st)
	dataset := store.Dataset{ID: "d", NetworkID: "n", Name: "x", Version: "v1", Manifest: `{"id":"d","network_id":"n","name":"x","version":"v1","root_hash":"x","files":[]}`}
	if err := m.Import(dataset, map[string][]byte{"wrong": []byte("data")}, "node"); err == nil {
		t.Fatal("damaged chunk accepted")
	}
}
