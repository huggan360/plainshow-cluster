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
