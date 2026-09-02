package controller

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/config"
)

func TestConfigRoundTripUsesOnlyControllerDirectories(t *testing.T) {
	layout, err := config.NewLayout(filepath.Join(t.TempDir(), "controller"))
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureControllerDirs(); err != nil {
		t.Fatal(err)
	}
	want := Defaults()
	want.ID = "controller-id"
	want.Listen.Port = 10001
	want.Networks = []NetworkConfig{{ID: "network", Name: "Lab"}}
	if err := Save(layout, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(layout)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.Listen.Port != want.Listen.Port || len(got.Networks) != 1 {
		t.Fatalf("round trip = %#v", got)
	}
	for _, nodeOnly := range []string{layout.Projects(), layout.Datasets(), layout.Artifacts(), layout.Database()} {
		if _, err := os.Stat(nodeOnly); !os.IsNotExist(err) {
			t.Errorf("controller created node-only path %s", nodeOnly)
		}
	}
	info, err := os.Stat(layout.ControllerConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("controller config mode = %v", info.Mode().Perm())
	}
}
