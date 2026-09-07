package accountserver

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "accounts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestBootstrapCreatesExactlyOneAdministrator(t *testing.T) {
	store := openTestStore(t)
	if err := store.InitialiseBootstrap(TokenHash("secret")); err != nil {
		t.Fatal(err)
	}
	first := Account{ID: "a1", Username: "hugo", DisplayName: "Hugo", PasswordHash: "hash"}
	if err := store.CreateAccount(first, TokenHash("wrong"), true); !errors.Is(err, ErrBootstrapToken) {
		t.Fatalf("wrong bootstrap error = %v", err)
	}
	if err := store.CreateAccount(first, TokenHash("secret"), false); err != nil {
		t.Fatal(err)
	}
	got, err := store.AccountByUsername("HUGO")
	if err != nil || !got.Admin {
		t.Fatalf("first account = %#v, %v", got, err)
	}
	second := Account{ID: "a2", Username: "albin", DisplayName: "Albin", PasswordHash: "hash"}
	if err := store.CreateAccount(second, "", false); !errors.Is(err, ErrRegistrationClosed) {
		t.Fatalf("closed registration error = %v", err)
	}
}

func TestSessionsAndDisabledAccounts(t *testing.T) {
	store := openTestStore(t)
	_ = store.InitialiseBootstrap(TokenHash("secret"))
	if err := store.CreateAccount(Account{ID: "a1", Username: "hugo", DisplayName: "Hugo", PasswordHash: "hash"}, TokenHash("secret"), true); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession("session", "a1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if account, err := store.SessionAccount("session"); err != nil || account.ID != "a1" {
		t.Fatalf("session account = %#v, %v", account, err)
	}
	if err := store.SetAccountDisabled("a1", true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SessionAccount("session"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled session error = %v", err)
	}
}

func TestCheckInFeedsGlobalStatsAndPreservesOwnership(t *testing.T) {
	store := openTestStore(t)
	_ = store.InitialiseBootstrap(TokenHash("secret"))
	if err := store.CreateAccount(Account{ID: "a1", Username: "hugo", DisplayName: "Hugo", PasswordHash: "hash"}, TokenHash("secret"), true); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAccount(Account{ID: "a2", Username: "albin", DisplayName: "Albin", PasswordHash: "hash"}, "", true); err != nil {
		t.Fatal(err)
	}
	registered, err := store.RegisterNetwork("a1", NetworkRegistration{ID: "network", Name: "Lab",
		ManagementKey: "a-management-key-that-is-long-enough", Role: "owner"})
	if err != nil || registered.OwnerAccountID != "a1" {
		t.Fatalf("network registration = %+v, %v", registered, err)
	}
	checkIn := NodeCheckIn{ID: "node", Name: "Pi", GPUCount: 1, ProjectCount: 3,
		RunningJobs: 2, Networks: []NetworkRef{{ID: "network", Name: "Lab"}},
		Address: "https://100.64.0.1:10000", PublicKey: "public", Fingerprint: "fingerprint"}
	if _, err := store.CheckIn("a1", checkIn); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CheckIn("a2", checkIn); !errors.Is(err, ErrNodeOwner) {
		t.Fatalf("node takeover error = %v", err)
	}
	stats, err := store.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Accounts != 2 || stats.Nodes != 1 || stats.OnlineNodes != 1 ||
		stats.Networks != 1 || stats.GPUs != 1 || stats.Projects != 3 || stats.RunningJobs != 2 {
		t.Fatalf("stats = %#v", stats)
	}
	mine, err := store.NetworksForAccount("a1")
	if err != nil || len(mine) != 1 || len(mine[0].Devices) != 1 ||
		mine[0].Devices[0].Address != "https://100.64.0.1:10000" {
		t.Fatalf("bootstrap devices = %+v, %v", mine, err)
	}
}

func TestNetworkRegistryRequiresKeyAndRecordsMembers(t *testing.T) {
	store := openTestStore(t)
	_ = store.InitialiseBootstrap(TokenHash("secret"))
	_ = store.CreateAccount(Account{ID: "a1", Username: "one", DisplayName: "One", PasswordHash: "hash"}, TokenHash("secret"), true)
	_ = store.CreateAccount(Account{ID: "a2", Username: "two", DisplayName: "Two", PasswordHash: "hash"}, "", true)
	key := "0123456789012345678901234567890123456789"
	if _, err := store.RegisterNetwork("a1", NetworkRegistration{ID: "n", Name: "Lab", ManagementKey: key, Role: "member"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterNetwork("a2", NetworkRegistration{ID: "n", Name: "Lab", ManagementKey: "wrong-wrong-wrong-wrong-wrong-wrong", Role: "member"}); !errors.Is(err, ErrNetworkKey) {
		t.Fatalf("wrong management key = %v", err)
	}
	if _, err := store.RegisterNetwork("a2", NetworkRegistration{ID: "n", Name: "Lab", ManagementKey: key, Role: "member"}); !errors.Is(err, ErrNetworkMember) {
		t.Fatalf("uninvited account = %v", err)
	}
	if err := store.GrantNetworkMember("a1", "n", key, "a2", "member"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterNetwork("a2", NetworkRegistration{ID: "n", Name: "Lab", ManagementKey: key, Role: "member"}); err != nil {
		t.Fatal(err)
	}
	networks, err := store.Networks()
	if err != nil || len(networks) != 1 || networks[0].Members != 2 || networks[0].ManagementKey != key {
		t.Fatalf("networks = %+v, %v", networks, err)
	}
}

func TestDeletedNetworkCannotBeRecreatedByAStaleDevice(t *testing.T) {
	store := openTestStore(t)
	_ = store.InitialiseBootstrap(TokenHash("secret"))
	_ = store.CreateAccount(Account{ID: "owner", Username: "owner", DisplayName: "Owner", PasswordHash: "hash"}, TokenHash("secret"), true)
	key := "0123456789012345678901234567890123456789"
	registration := NetworkRegistration{ID: "old-network", Name: "Old", ManagementKey: key, Role: "owner"}
	if _, err := store.RegisterNetwork("owner", registration); err != nil {
		t.Fatal(err)
	}
	accounts, err := store.DeleteNetwork("owner", "old-network", key)
	if err != nil || len(accounts) != 1 || accounts[0] != "owner" {
		t.Fatalf("delete returned %v, %v", accounts, err)
	}
	if _, err := store.RegisterNetwork("owner", registration); !errors.Is(err, ErrNetworkDeleted) {
		t.Fatalf("stale registration recreated deleted network: %v", err)
	}
	deleted, err := store.DeletedNetworksForAccount("owner")
	if err != nil || len(deleted) != 1 || deleted[0] != "old-network" {
		t.Fatalf("deleted ids = %v, %v", deleted, err)
	}
}

func TestNetworkAdministratorsCanChangeAndRemoveMembers(t *testing.T) {
	store := openTestStore(t)
	_ = store.InitialiseBootstrap(TokenHash("secret"))
	_ = store.CreateAccount(Account{ID: "owner", Username: "owner", DisplayName: "Owner", PasswordHash: "hash"}, TokenHash("secret"), true)
	_ = store.CreateAccount(Account{ID: "admin", Username: "admin", DisplayName: "Admin", PasswordHash: "hash"}, "", true)
	_ = store.CreateAccount(Account{ID: "member", Username: "member", DisplayName: "Member", PasswordHash: "hash"}, "", true)
	key := "0123456789012345678901234567890123456789"
	if _, err := store.RegisterNetwork("owner", NetworkRegistration{ID: "network", Name: "Lab", ManagementKey: key, Role: "owner"}); err != nil {
		t.Fatal(err)
	}
	if err := store.GrantNetworkMember("owner", "network", key, "admin", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := store.GrantNetworkMember("owner", "network", key, "member", "member"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetNetworkMemberRole("admin", "network", key, "member", "operator"); err != nil {
		t.Fatal(err)
	}
	members, err := store.NetworkMembers("network")
	if err != nil || len(members) != 3 || members[2].AccountID != "member" || members[2].Role != "operator" {
		t.Fatalf("members after role update = %+v, %v", members, err)
	}
	if err := store.SetNetworkMemberRole("admin", "network", key, "owner", "viewer"); err == nil {
		t.Fatal("network owner was demoted")
	}
	if err := store.RemoveNetworkMember("admin", "network", key, "owner"); err == nil {
		t.Fatal("network owner was removed")
	}
	if err := store.RemoveNetworkMember("admin", "network", key, "member"); err != nil {
		t.Fatal(err)
	}
	members, err = store.NetworkMembers("network")
	if err != nil || len(members) != 2 {
		t.Fatalf("members after removal = %+v, %v", members, err)
	}
}

// TestNetworksForAccountIsScopedToMembership is the read that makes an account
// portable between machines, so it has to be exactly as wide as membership and
// not one row wider.
func TestNetworksForAccountIsScopedToMembership(t *testing.T) {
	store := openTestStore(t)
	if err := store.InitialiseBootstrap(TokenHash("secret")); err != nil {
		t.Fatal(err)
	}
	hugo := Account{ID: "a1", Username: "huggan360", DisplayName: "Hugo", PasswordHash: "h"}
	if err := store.CreateAccount(hugo, TokenHash("secret"), false); err != nil {
		t.Fatal(err)
	}
	albin := Account{ID: "a2", Username: "albin", DisplayName: "Albin", PasswordHash: "h"}
	if err := store.CreateAccount(albin, "", true); err != nil {
		t.Fatal(err)
	}

	const key = "0123456789abcdef0123456789abcdef0123456789"
	if _, err := store.RegisterNetwork(hugo.ID, NetworkRegistration{
		ID: "net-lab", Name: "Research lab", ManagementKey: key, Role: "owner",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterNetwork(albin.ID, NetworkRegistration{
		ID: "net-albin", Name: "Albin's", ManagementKey: key, Role: "owner",
	}); err != nil {
		t.Fatal(err)
	}

	mine, err := store.NetworksForAccount(hugo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 1 || mine[0].ID != "net-lab" {
		t.Fatalf("networks for hugo = %+v, want only net-lab", mine)
	}
	if !mine[0].Owner || mine[0].Role != "owner" {
		t.Errorf("the network's own owner came back as %+v", mine[0])
	}
	// The key travels deliberately: it is what lets another machine of the same
	// account take part. Without it the row is decoration.
	if mine[0].ManagementKey != key {
		t.Error("the management key was not returned, so a second machine could not participate")
	}

	// Being added to somebody else's network makes it appear, as a member.
	if err := store.GrantNetworkMember(albin.ID, "net-albin", key, hugo.ID, "member"); err != nil {
		t.Fatal(err)
	}
	mine, err = store.NetworksForAccount(hugo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 2 {
		t.Fatalf("after being added, networks = %+v, want 2", mine)
	}
	for _, network := range mine {
		if network.ID == "net-albin" && (network.Owner || network.Role != "member") {
			t.Errorf("a member's row claims ownership: %+v", network)
		}
	}
}
