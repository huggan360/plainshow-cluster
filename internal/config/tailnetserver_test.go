package config

import (
	"os"
	"testing"
)

func TestTailnetServerRoundTrip(t *testing.T) {
	layout, err := NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadTailnetServer(layout); err != nil || got != "" {
		t.Fatalf("empty marker = %q, %v", got, err)
	}
	if err := SaveTailnetServer(layout, " https://tailnet.example/ "); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadTailnetServer(layout); err != nil || got != "https://tailnet.example" {
		t.Fatalf("marker = %q, %v", got, err)
	}
	info, err := os.Stat(layout.TailnetServer())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("marker mode = %v", info.Mode().Perm())
	}
}
