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
	checkIn := NodeCheckIn{ID: "node", Name: "Pi", GPUCount: 1, ProjectCount: 3,
		RunningJobs: 2, Networks: []NetworkRef{{ID: "network", Name: "Lab"}}}
	if err := store.CheckIn("a1", checkIn); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckIn("a2", checkIn); !errors.Is(err, ErrNodeOwner) {
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
}
