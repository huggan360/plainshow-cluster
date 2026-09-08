package api

import (
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/config"
)

// TestOfflineActuallyRemovesTheMachineFromRay is the whole point of the switch.
// A control that changes a word on the screen while work keeps arriving is
// worse than no control: somebody trusts it and gets their GPU taken anyway.
func TestOfflineActuallyRemovesTheMachineFromRay(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.cfg.Worker = config.WorkerConfig{Enabled: true, AllowJobs: true, AllowGPU: true,
		AllowProjectSync: true}
	srv.cfg.Memberships = []config.MembershipConfig{{
		ID: "net-1", Name: "Lab", Enabled: true,
		Policy: config.WorkerConfig{Enabled: true, AllowJobs: true, AllowProjectSync: true},
	}}
	srv.cfg.ActiveNetwork = "net-1"

	if _, eligible := srv.rayPolicy("net-1"); !eligible {
		t.Fatal("a machine that accepts work is not eligible for Ray")
	}
	if err := srv.projectSyncAllowed("net-1"); err != nil {
		t.Fatalf("project files refused while available: %v", err)
	}

	srv.availability.offline.Store(true)

	if _, eligible := srv.rayPolicy("net-1"); eligible {
		t.Error("an offline machine is still eligible for Ray")
	}
	if err := srv.projectSyncAllowed("net-1"); err == nil {
		t.Error("an offline machine still accepts project files")
	}
}

// TestAvailabilityStartsOnAndIsNotSaved. Somebody who installed a cluster
// expects their machine to take part when it boots, and a switch flipped one
// afternoon should not be a setting rediscovered months later.
func TestAvailabilityStartsOnAndIsNotSaved(t *testing.T) {
	srv, _ := newTestServer(t)
	if !srv.Available() {
		t.Fatal("a freshly started node is not available")
	}
	srv.availability.offline.Store(true)
	if srv.Available() {
		t.Fatal("the switch did not take")
	}
	// Nothing about it reaches the settings document.
	if err := config.Save(srv.layout, srv.cfg); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Load(srv.layout)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Worker.Enabled {
		t.Error("going offline changed the durable policy, which it must not")
	}
}

// TestSettingsStillWins: a machine set to refuse work in Settings stays
// refusing however this switch is set, or one page of the interface would be
// contradicting another.
func TestSettingsStillWins(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.cfg.Worker = config.WorkerConfig{Enabled: false, AllowJobs: false}
	srv.cfg.Memberships = []config.MembershipConfig{{
		ID: "net-1", Enabled: true,
		Policy: config.WorkerConfig{Enabled: true, AllowJobs: true},
	}}
	if _, eligible := srv.rayPolicy("net-1"); eligible {
		t.Error("a machine refusing work in Settings was eligible because it was 'available'")
	}
	state := srv.presenceState()
	if state["accept_work"] != false {
		t.Errorf("presence reported accept_work=%v", state["accept_work"])
	}
}
