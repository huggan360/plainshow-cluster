package config

import (
	"os"
	"testing"
)

func TestRuntimeDescriptorRoundTripAndPermissions(t *testing.T) {
	layout, err := NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	want := Runtime{URL: "http://127.0.0.1:43127", PID: 1234}
	if err := SaveRuntime(layout, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRuntime(layout)
	if err != nil || got != want {
		t.Fatalf("runtime = %+v, %v; want %+v", got, err, want)
	}
	info, err := os.Stat(layout.RuntimeFile())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o044 == 0 {
		t.Fatalf("runtime descriptor is not readable by the desktop user: %o", info.Mode().Perm())
	}
}

func TestLoadRuntimeRejectsEmptyAddress(t *testing.T) {
	layout, _ := NewLayout(t.TempDir())
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.RuntimeFile(), []byte(`{"pid":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRuntime(layout); err == nil {
		t.Fatal("empty runtime address was accepted")
	}
}
