package mesh

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveRoundTrip(t *testing.T) {
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "src"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "src", "main.py"), []byte("print(42)\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	raw, err := ArchiveDir(source)
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := ExtractArchive(raw, target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(target, "src", "main.py"))
	if err != nil || string(got) != "print(42)\n" {
		t.Fatalf("round trip: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); !os.IsNotExist(err) {
		t.Fatal("git metadata was copied")
	}
}

func TestExtractRejectsTraversal(t *testing.T) {
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "../escape", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	_ = gz.Close()
	if err := ExtractArchive(raw.Bytes(), t.TempDir()); err == nil {
		t.Fatal("unsafe archive accepted")
	}
}
