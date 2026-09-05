package api

import (
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/events"
	"github.com/huggan360/plainshow-cluster/internal/ray"
	"github.com/huggan360/plainshow-cluster/internal/store"
	"github.com/huggan360/plainshow-cluster/internal/sysinfo"
)

func discoveryNode(networkID, nodeID, name, seen string) store.NetworkNode {
	return store.NetworkNode{
		NetworkID: networkID,
		NodeID:    nodeID,
		Name:      name,
		Roles:     []string{"master"},
		PublicKey: base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
		Fingerprint: base64.RawURLEncoding.EncodeToString(
			make([]byte, 32)),
		Address:  "https://" + name + ":10000",
		Policy:   map[string]any{},
		Capacity: map[string]any{},
		LastSeen: seen,
	}
}

func TestOnlyOnlineNodesContributeCapacity(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	recent := store.NetworkNode{LastSeen: now.Add(-30 * time.Second).Format(time.RFC3339)}
	stale := store.NetworkNode{LastSeen: now.Add(-3 * time.Minute).Format(time.RFC3339)}
	self := store.NetworkNode{IsSelf: true, LastSeen: ""}
	if !nodeCapacityOnline(recent, now) {
		t.Fatal("recent peer was treated as offline")
	}
	if nodeCapacityOnline(stale, now) {
		t.Fatal("stale peer still contributed usable capacity")
	}
	if !nodeCapacityOnline(self, now) {
		t.Fatal("the running local node was treated as offline")
	}
}

func TestRayInventoryIncludesEveryGPUVendor(t *testing.T) {
	policy := ray.ResourcePolicy{AllowGPU: true, GPUsKnown: true}
	addGPUInventory(&policy, []sysinfo.GPU{
		{Vendor: "nvidia", Trainable: true},
		{Vendor: "amd", Trainable: true},
		{Vendor: "intel", Trainable: true},
		{Vendor: "unknown", Trainable: false},
	})
	if policy.GPUCount != 3 || policy.NVIDIAGPUCount != 1 ||
		policy.AMDGPUCount != 1 || policy.IntelGPUCount != 1 {
		t.Fatalf("mixed GPU inventory = %+v", policy)
	}
}

func TestRayAnnouncementsConvergeAndRespectTombstones(t *testing.T) {
	l, err := config.NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Node.ID = "bravo"
	cfg.Memberships = []config.MembershipConfig{{ID: "network", Enabled: true}}
	cfg.ActiveNetwork = "network"
	srv := &Server{cfg: cfg, layout: l, hub: events.NewHub()}

	started := rayAnnouncement{Head: "100.64.0.1:6379", NodeID: "alpha",
		Updated: "2026-09-04T10:00:00Z"}
	srv.mergeRayAnnouncement("network", started)
	if got := srv.rayAnnouncement("network"); got != started {
		t.Fatalf("announcement = %+v, want %+v", got, started)
	}

	// An older returning peer cannot resurrect an obsolete head after the
	// machine that owned it has stopped it.
	stopped := rayAnnouncement{NodeID: "alpha", Updated: "2026-09-04T10:01:00Z"}
	srv.mergeRayAnnouncement("network", stopped)
	srv.mergeRayAnnouncement("network", started)
	if got := srv.rayAnnouncement("network"); got != stopped {
		t.Fatalf("stale head was resurrected: %+v", got)
	}
}

func TestRayAnnouncementTieBreakIsDeterministic(t *testing.T) {
	at := "2026-09-04T10:00:00.123456789Z"
	a := rayAnnouncement{Head: "100.64.0.1:6379", NodeID: "alpha", Updated: at}
	b := rayAnnouncement{Head: "100.64.0.2:6379", NodeID: "bravo", Updated: at}
	if !rayAnnouncementNewer(b, a) || rayAnnouncementNewer(a, b) {
		t.Fatal("simultaneous head announcements do not have one stable winner")
	}
}

func TestLocalRayStateRoundTrip(t *testing.T) {
	l, err := config.NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	srv := &Server{layout: l}
	want := localRayState{NetworkID: "network", Head: "100.64.0.1:6379", Role: "worker",
		Policy: ray.ResourcePolicy{MaxCPU: 4, MaxRAMMB: 8192, AllowGPU: true}}
	if err := srv.writeLocalRayState(want); err != nil {
		t.Fatal(err)
	}
	got, err := srv.readLocalRayState()
	if err != nil || got != want {
		t.Fatalf("state = %+v, %v; want %+v", got, err, want)
	}
	if err := srv.clearLocalRayState(); err != nil {
		t.Fatal(err)
	}
	got, err = srv.readLocalRayState()
	if err != nil || got != (localRayState{}) {
		t.Fatalf("cleared state = %+v, %v", got, err)
	}
}

func TestRayPolicyIntersectsDeviceAndNetworkLimits(t *testing.T) {
	cfg := config.Defaults()
	cfg.ActiveNetwork = "network"
	cfg.Worker = config.WorkerConfig{
		Enabled: true, AllowJobs: true, AllowGPU: true, MaxCPU: 12, MaxRAMMB: 32000,
	}
	cfg.Memberships = []config.MembershipConfig{{
		ID: "network", Enabled: true,
		Policy: config.WorkerConfig{
			Enabled: true, AllowJobs: true, AllowGPU: false, MaxCPU: 6, MaxRAMMB: 64000,
		},
	}}
	srv := &Server{cfg: cfg}

	policy, eligible := srv.rayPolicy("network")
	want := ray.ResourcePolicy{MaxCPU: 6, MaxRAMMB: 32000, AllowGPU: false, GPUsKnown: true}
	if !eligible || policy != want {
		t.Fatalf("policy = %+v, eligible %v; want %+v, true", policy, eligible, want)
	}

	cfg.Memberships[0].Policy.AllowJobs = false
	if _, eligible := srv.rayPolicy("network"); eligible {
		t.Fatal("network that forbids jobs was eligible for Ray")
	}
	if _, eligible := srv.rayPolicy("missing"); eligible {
		t.Fatal("machine was eligible for a network it has not joined")
	}
}

// TestPeerExchangeConvergesWithoutCoordinator models the important three-node
// case: A enrolled C while B was not involved. A's next signed exchange with B
// must teach B enough about C for those two devices to talk directly.
func TestPeerExchangeConvergesWithoutCoordinator(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	old := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	fresh := time.Now().UTC().Format(time.RFC3339)
	for _, node := range []store.NetworkNode{
		discoveryNode("network", "a", "alpha", old),
		discoveryNode("network", "b", "bravo", old),
	} {
		node.IsSelf = node.NodeID == "b"
		if err := st.UpsertNetworkNode(node); err != nil {
			t.Fatal(err)
		}
	}

	srv := &Server{cfg: &config.Config{Node: config.NodeConfig{ID: "b"}}, store: st}
	srv.mergePeerNodes("network", []store.NetworkNode{
		discoveryNode("network", "a", "alpha", fresh),
		discoveryNode("network", "c", "charlie", fresh),
	})

	c, err := st.NetworkNode("network", "c")
	if err != nil {
		t.Fatalf("B did not learn about C: %v", err)
	}
	if len(c.Roles) != 1 || c.Roles[0] != "worker" {
		t.Errorf("gossiped legacy roles escaped normalisation: %v", c.Roles)
	}

	// An offline peer can return a stale directory later. It must not roll a
	// reachable address back, and no peer can rewrite this machine's own row.
	srv.mergePeerNodes("network", []store.NetworkNode{
		discoveryNode("network", "c", "stale-charlie", old),
		discoveryNode("network", "b", "not-bravo", fresh),
	})
	c, _ = st.NetworkNode("network", "c")
	b, _ := st.NetworkNode("network", "b")
	if c.Name != "charlie" {
		t.Errorf("stale gossip replaced C with %q", c.Name)
	}
	if b.Name != "bravo" || !b.IsSelf {
		t.Errorf("gossip overwrote the local node: %#v", b)
	}
}

func TestPeerExchangeRejectsIncompleteRecords(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := &Server{cfg: &config.Config{Node: config.NodeConfig{ID: "self"}}, store: st}

	broken := discoveryNode("network", "broken", "broken", store.Now())
	broken.Address = "http://unencrypted.example"
	srv.mergePeerNodes("network", []store.NetworkNode{broken})
	if _, err := st.NetworkNode("network", "broken"); err == nil {
		t.Fatal("accepted an incomplete peer record")
	}
}
