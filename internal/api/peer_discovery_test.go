package api

import (
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/store"
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
