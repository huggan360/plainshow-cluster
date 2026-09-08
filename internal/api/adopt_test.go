package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/accountclient"
	"github.com/huggan360/plainshow-cluster/internal/accountserver"
	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

// fakeAuthority serves the one endpoint adoption depends on.
func fakeAuthority(t *testing.T, networks []accountserver.AccountNetwork) *accountclient.Client {
	return fakeAuthorityState(t, networks, nil)
}

func fakeAuthorityState(t *testing.T, networks []accountserver.AccountNetwork, deleted []string) *accountclient.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/networks/mine" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"networks": networks, "deleted_network_ids": deleted})
	}))
	t.Cleanup(server.Close)

	client, err := accountclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestDeletedNetworkIsPrunedFromTheDevice(t *testing.T) {
	srv := adoptingNode(t)
	srv.cfg.Memberships = []config.MembershipConfig{{
		ID: "deleted", Name: "Deleted", AccountRole: store.NetworkOwner,
		ManagementKey: goodKey, Enabled: true,
	}}
	srv.cfg.ActiveNetwork = "deleted"
	if err := srv.recordLocalMembership(srv.cfg.Memberships[0], store.Network{
		ID: "deleted", Name: "Deleted", OwnerAccountID: "acct-1",
	}); err != nil {
		t.Fatal(err)
	}
	client := fakeAuthorityState(t, nil, []string{"deleted"})
	if _, err := srv.AdoptAccountNetworks(context.Background(), client, "token"); err != nil {
		t.Fatal(err)
	}
	if len(srv.cfg.Memberships) != 0 || srv.cfg.ActiveNetwork != "" {
		t.Fatalf("deleted membership remains: %+v", srv.cfg.Memberships)
	}
	networks, err := srv.store.Networks("acct-1")
	if err != nil || len(networks) != 0 {
		t.Fatalf("deleted network remains in local store: %v, %v", networks, err)
	}
}

func adoptingNode(t *testing.T) *Server {
	t.Helper()
	srv, _ := newTestServer(t)
	srv.cfg.Account.ID = "acct-1"
	srv.cfg.Account.Username = "huggan360"
	srv.cfg.Account.Server = "https://clusteradmin.example"
	if err := srv.store.UpsertAccount(store.Account{
		ID: "acct-1", Username: "huggan360", Created: store.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	return srv
}

const goodKey = "0123456789abcdef0123456789abcdef0123456789"

// TestSigningInOnANewMachineBringsYourNetworks is the behaviour that was
// missing entirely: check-in only ever pushed what a device already had, so a
// second machine signed in successfully and showed nothing, with no code path
// that could have told it otherwise.
func TestSigningInOnANewMachineBringsYourNetworks(t *testing.T) {
	srv := adoptingNode(t)
	client := fakeAuthority(t, []accountserver.AccountNetwork{
		{ID: "net-lab", Name: "Research lab", Role: "owner", ManagementKey: goodKey, Owner: true},
		{ID: "net-albin", Name: "Albin", Role: "member", ManagementKey: goodKey},
	})

	adopted, err := srv.AdoptAccountNetworks(context.Background(), client, "token")
	if err != nil {
		t.Fatal(err)
	}
	if adopted != 2 {
		t.Fatalf("adopted %d networks, want 2", adopted)
	}
	if len(srv.cfg.Memberships) != 2 {
		t.Fatalf("config carries %d memberships, want 2", len(srv.cfg.Memberships))
	}
	// Membership discovery must not choose where local project code executes.
	if srv.cfg.ActiveNetwork != "" {
		t.Error("adoption silently selected a compute network")
	}
	// The interface reads the database, not the config, so both have to agree.
	networks, err := srv.store.Networks("acct-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(networks) != 2 {
		t.Fatalf("the store lists %d networks, want 2", len(networks))
	}
}

func TestAdoptionSeedsTheFirstPeerConnection(t *testing.T) {
	srv := adoptingNode(t)
	client := fakeAuthority(t, []accountserver.AccountNetwork{{
		ID: "net-lab", Name: "Research lab", Role: "owner", ManagementKey: goodKey, Owner: true,
		Devices: []accountserver.NetworkBootstrapNode{{
			ID: "peer", Name: "Other PC", OS: "linux", Arch: "amd64",
			Address: "https://100.64.0.2:10000", PublicKey: "public",
			Fingerprint: "fingerprint", LastSeen: store.Now(),
		}},
	}})
	if _, err := srv.AdoptAccountNetworks(context.Background(), client, "token"); err != nil {
		t.Fatal(err)
	}
	peer, err := srv.store.NetworkNode("net-lab", "peer")
	if err != nil {
		t.Fatal(err)
	}
	if peer.Address != "https://100.64.0.2:10000" || peer.Fingerprint != "fingerprint" {
		t.Fatalf("peer bootstrap = %+v", peer)
	}
}

// TestAdoptingIsIdempotent: it runs on every check-in, so a second pass must
// not duplicate anything.
func TestAdoptingIsIdempotent(t *testing.T) {
	srv := adoptingNode(t)
	client := fakeAuthority(t, []accountserver.AccountNetwork{
		{ID: "net-lab", Name: "Research lab", Role: "owner", ManagementKey: goodKey, Owner: true},
	})

	if _, err := srv.AdoptAccountNetworks(context.Background(), client, "token"); err != nil {
		t.Fatal(err)
	}
	adopted, err := srv.AdoptAccountNetworks(context.Background(), client, "token")
	if err != nil {
		t.Fatal(err)
	}
	if adopted != 0 {
		t.Errorf("a second pass adopted %d networks, want 0", adopted)
	}
	if len(srv.cfg.Memberships) != 1 {
		t.Errorf("config carries %d memberships, want 1", len(srv.cfg.Memberships))
	}
}

// TestAdoptingSkipsNetworksItCannotProveMembershipOf: without the management
// key this device cannot sync the network at all, so listing it would be a
// network that exists in the interface and nowhere else.
func TestAdoptingSkipsNetworksItCannotProveMembershipOf(t *testing.T) {
	srv := adoptingNode(t)
	client := fakeAuthority(t, []accountserver.AccountNetwork{
		{ID: "net-lab", Name: "Research lab", Role: "member", ManagementKey: "short"},
	})

	adopted, err := srv.AdoptAccountNetworks(context.Background(), client, "token")
	if err != nil {
		t.Fatal(err)
	}
	if adopted != 0 || len(srv.cfg.Memberships) != 0 {
		t.Fatalf("adopted a network with no usable key: %d, %+v", adopted, srv.cfg.Memberships)
	}
}

// TestAdoptingLeavesTheSelectedNetworkAlone: a machine already working in one
// network must not be moved by a background check-in.
func TestAdoptingLeavesTheSelectedNetworkAlone(t *testing.T) {
	srv := adoptingNode(t)
	srv.cfg.Memberships = []config.MembershipConfig{{ID: "net-here", Name: "Here", Enabled: true}}
	srv.cfg.ActiveNetwork = "net-here"

	client := fakeAuthority(t, []accountserver.AccountNetwork{
		{ID: "net-other", Name: "Other", Role: "member", ManagementKey: goodKey},
	})
	if _, err := srv.AdoptAccountNetworks(context.Background(), client, "token"); err != nil {
		t.Fatal(err)
	}
	if srv.cfg.ActiveNetwork != "net-here" {
		t.Fatalf("a background adoption moved this machine to %q", srv.cfg.ActiveNetwork)
	}
}

// TestAdoptedRoleIsKept: an account that is a member somewhere must not be
// recorded locally as its owner.
func TestAdoptedRoleIsKept(t *testing.T) {
	srv := adoptingNode(t)
	client := fakeAuthority(t, []accountserver.AccountNetwork{
		{ID: "net-albin", Name: "Albin", Role: "member", ManagementKey: goodKey},
	})
	if _, err := srv.AdoptAccountNetworks(context.Background(), client, "token"); err != nil {
		t.Fatal(err)
	}
	if got := srv.cfg.Memberships[0].AccountRole; got != store.NetworkMember {
		t.Fatalf("adopted role is %q, want %q", got, store.NetworkMember)
	}
	raw, _ := json.Marshal(srv.cfg.Memberships[0])
	if len(raw) == 0 {
		t.Fatal("membership does not serialise")
	}
}
