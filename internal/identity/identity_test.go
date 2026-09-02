package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateIsStableAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "device.key")
	first, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("identity changed: %s != %s", first.ID, second.ID)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %o, want 600", info.Mode().Perm())
	}
	message := []byte("join network")
	if !Verify(first.Public, message, first.Sign(message)) {
		t.Fatal("signature did not verify")
	}
}
