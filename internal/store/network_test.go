package store

import "testing"

func TestNetworksAreIndependent(t *testing.T) {
	st := open(t)
	account := Account{ID: "a1", Username: "fredrik", PublicKey: "pub"}
	if err := st.UpsertAccount(account); err != nil {
		t.Fatal(err)
	}
	for _, network := range []Network{{ID: "n1", Name: "Home"}, {ID: "n2", Name: "Friends"}} {
		network.OwnerAccountID = account.ID
		if err := st.UpsertNetwork(network); err != nil {
			t.Fatal(err)
		}
		if err := st.AddNetworkMember(network.ID, account.ID, NetworkOwner); err != nil {
			t.Fatal(err)
		}
		project := Project{ID: "p-" + network.ID, NetworkID: network.ID, Name: "same-name"}
		if err := st.CreateProject(&project); err != nil {
			t.Fatalf("same project name should work across networks: %v", err)
		}
		if err := st.UpsertNetworkNode(NetworkNode{
			NetworkID: network.ID, NodeID: "device", Name: "desktop",
			Roles: []string{"worker"}, Policy: map[string]any{"enabled": true},
			Capacity: map[string]any{"cpu_cores": 8}, IsSelf: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	networks, err := st.Networks(account.ID)
	if err != nil || len(networks) != 2 {
		t.Fatalf("networks = %#v, %v", networks, err)
	}
	projects, err := st.ProjectsInNetwork("n1")
	if err != nil || len(projects) != 1 || projects[0].NetworkID != "n1" {
		t.Fatalf("network projects = %#v, %v", projects, err)
	}
	nodes, err := st.NetworkNodes("n2")
	if err != nil || len(nodes) != 1 || nodes[0].Capacity["cpu_cores"] != float64(8) {
		t.Fatalf("network nodes = %#v, %v", nodes, err)
	}
	members, err := st.NetworkMembers("n1")
	if err != nil || len(members) != 1 || !members[0].Permissions.ManageNetwork {
		t.Fatalf("network members = %#v, %v", members, err)
	}
}

func TestNetworkRoles(t *testing.T) {
	if !PermissionsForRole(NetworkOwner).ManageMembers {
		t.Fatal("owner cannot manage members")
	}
	if PermissionsForRole(NetworkMember).ManageMembers {
		t.Fatal("ordinary member can manage members")
	}
	if PermissionsForRole(NetworkViewer).RunJobs {
		t.Fatal("viewer can run jobs")
	}
	if ValidNetworkRole("root") {
		t.Fatal("accepted unknown role")
	}
}

// TestStoredRetiredRolesBecomeOrdinaryDevices covers the database half of the
// role migration. Config normalisation alone does not touch peer rows learned
// from an older machine.
func TestStoredRetiredRolesBecomeOrdinaryDevices(t *testing.T) {
	st := open(t)
	cases := []struct {
		id    string
		roles []string
	}{
		{"old-master", []string{"master"}},
		{"old-controller-worker", []string{"controller", "worker"}},
		{"empty", nil},
	}
	for _, tc := range cases {
		if err := st.UpsertNetworkNode(NetworkNode{
			NetworkID: "network", NodeID: tc.id, Name: tc.id, Roles: tc.roles,
		}); err != nil {
			t.Fatal(err)
		}
	}
	nodes, err := st.NetworkNodes("network")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != len(cases) {
		t.Fatalf("got %d nodes, want %d", len(nodes), len(cases))
	}
	for _, node := range nodes {
		if len(node.Roles) != 1 || node.Roles[0] != "worker" {
			t.Errorf("%s roles = %v, want [worker]", node.NodeID, node.Roles)
		}
	}
}

func TestTouchNetworkNodeRequiresAnExistingNode(t *testing.T) {
	st := open(t)
	if err := st.UpsertNetworkNode(NetworkNode{
		NetworkID: "network", NodeID: "device", Name: "device", LastSeen: "old",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchNetworkNode("network", "device", "new"); err != nil {
		t.Fatal(err)
	}
	node, err := st.NetworkNode("network", "device")
	if err != nil || node.LastSeen != "new" {
		t.Fatalf("last seen = %q, %v", node.LastSeen, err)
	}
	if err := st.TouchNetworkNode("network", "missing", "new"); err != ErrNotFound {
		t.Fatalf("missing node error = %v, want ErrNotFound", err)
	}
}

func TestAdoptGlobalAccountMovesNetworkOwnershipNotDeviceIdentity(t *testing.T) {
	st := open(t)
	legacy := Account{ID: "device", Username: "desktop"}
	if err := st.UpsertAccount(legacy); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertNetwork(Network{ID: "network", Name: "Lab", OwnerAccountID: legacy.ID}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddNetworkMember("network", legacy.ID, NetworkOwner); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertNetworkNode(NetworkNode{NetworkID: "network", NodeID: "device", Name: "desktop"}); err != nil {
		t.Fatal(err)
	}
	global := Account{ID: "account", Username: "hugo", DisplayName: "Hugo"}
	if err := st.AdoptGlobalAccount(legacy.ID, global); err != nil {
		t.Fatal(err)
	}
	network, _ := st.NetworkByID("network")
	members, _ := st.NetworkMembers("network")
	node, nodeErr := st.NetworkNode("network", "device")
	if network.OwnerAccountID != global.ID || len(members) != 1 || members[0].Account.ID != global.ID {
		t.Fatalf("ownership was not migrated: network=%#v members=%#v", network, members)
	}
	if nodeErr != nil || node.NodeID != legacy.ID {
		t.Fatalf("device identity moved with account: %#v, %v", node, nodeErr)
	}
}
