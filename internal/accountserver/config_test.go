package accountserver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/config"
)

func TestConfigRoundTripUsesOnlyAdminDirectories(t *testing.T) {
	layout, err := config.NewLayout(filepath.Join(t.TempDir(), "admin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureAdminDirs(); err != nil {
		t.Fatal(err)
	}
	want := Defaults()
	want.Listen.Port = 10002
	want.PublicURL = "https://clusteradmin.example"
	want.RegistrationOpen = false
	want.Tailnet.LoginServer = "https://tailnet.example"
	if err := Save(layout, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(layout)
	if err != nil {
		t.Fatal(err)
	}
	if got.Listen.Port != want.Listen.Port || got.PublicURL != want.PublicURL || got.RegistrationOpen ||
		got.Tailnet.LoginServer != want.Tailnet.LoginServer || got.Tailnet.EnrollmentTTL != "10m" {
		t.Fatalf("round trip = %#v", got)
	}
	for _, nodeOnly := range []string{layout.Keys(), layout.Projects(), layout.Datasets(), layout.Artifacts(), layout.Database()} {
		if _, err := os.Stat(nodeOnly); !os.IsNotExist(err) {
			t.Errorf("admin created node-only path %s", nodeOnly)
		}
	}
	info, err := os.Stat(layout.AdminConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("admin config mode = %v", info.Mode().Perm())
	}
}
