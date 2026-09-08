package accountserver

import (
	"errors"
	"testing"
)

// TestAcceptingAnInvitationIsWhatCreatesMembership: an invitation is an offer,
// and until it is answered the invited account is not in the network. Anything
// less would make "invite" a way to conscript people.
func TestAcceptingAnInvitationIsWhatCreatesMembership(t *testing.T) {
	store, hugo, albin := twoAccounts(t)

	invite, err := store.CreateInvitation(hugo.ID, "net-lab", "albin", "member")
	if err != nil {
		t.Fatal(err)
	}
	if invite.Status != "pending" || invite.NetworkName != "Research lab" {
		t.Fatalf("invitation = %+v", invite)
	}
	if networks, _ := store.NetworksForAccount(albin.ID); len(networks) != 0 {
		t.Fatalf("a pending invitation already granted membership: %+v", networks)
	}

	waiting, err := store.InvitationsForAccount(albin.ID)
	if err != nil || len(waiting) != 1 {
		t.Fatalf("invitations for albin = %+v, %v", waiting, err)
	}

	if _, err := store.RespondToInvitation(albin.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	networks, err := store.NetworksForAccount(albin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(networks) != 1 || networks[0].ID != "net-lab" || networks[0].Role != "member" {
		t.Fatalf("after accepting, networks = %+v", networks)
	}
	// The key travels with it, or the machine could not participate.
	if networks[0].ManagementKey != testKey {
		t.Error("an accepted member cannot prove membership: no management key")
	}
}

func TestDecliningGrantsNothing(t *testing.T) {
	store, hugo, albin := twoAccounts(t)
	invite, err := store.CreateInvitation(hugo.ID, "net-lab", "albin", "member")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RespondToInvitation(albin.ID, invite.ID, false); err != nil {
		t.Fatal(err)
	}
	if networks, _ := store.NetworksForAccount(albin.ID); len(networks) != 0 {
		t.Fatalf("declining granted membership: %+v", networks)
	}
	if _, err := store.RespondToInvitation(albin.ID, invite.ID, true); !errors.Is(err, ErrInviteSettled) {
		t.Fatalf("answering twice = %v, want ErrInviteSettled", err)
	}
}

// TestOnlyTheAddresseeCanAnswer: an invitation id is not a capability.
func TestOnlyTheAddresseeCanAnswer(t *testing.T) {
	store, hugo, albin := twoAccounts(t)
	invite, err := store.CreateInvitation(hugo.ID, "net-lab", "albin", "member")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RespondToInvitation(hugo.ID, invite.ID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("somebody else answered an invitation: %v", err)
	}
	_ = albin
}

func TestOnlyOwnersAndAdminsCanInvite(t *testing.T) {
	store, hugo, albin := twoAccounts(t)
	invite, err := store.CreateInvitation(hugo.ID, "net-lab", "albin", "member")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RespondToInvitation(albin.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	// Albin is now a plain member, which is not enough to admit anyone else.
	if _, err := store.CreateInvitation(albin.ID, "net-lab", "huggan360", "member"); !errors.Is(err, ErrInviteNotAllowed) {
		t.Fatalf("a member invited somebody: %v", err)
	}
}

func TestOwnershipCannotBeInvited(t *testing.T) {
	store, hugo, _ := twoAccounts(t)
	if _, err := store.CreateInvitation(hugo.ID, "net-lab", "albin", "owner"); err == nil {
		t.Fatal("an invitation offered ownership")
	}
}

func TestInvitingAnExistingMemberIsRefused(t *testing.T) {
	store, hugo, albin := twoAccounts(t)
	invite, _ := store.CreateInvitation(hugo.ID, "net-lab", "albin", "member")
	if _, err := store.RespondToInvitation(albin.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateInvitation(hugo.ID, "net-lab", "albin", "admin"); !errors.Is(err, ErrAlreadyMember) {
		t.Fatalf("error = %v, want ErrAlreadyMember", err)
	}
}

// TestReinvitingAfterADeclineWorks: changing your mind is ordinary, and the
// unique row per (network, account) must not turn it into a conflict.
func TestReinvitingAfterADeclineWorks(t *testing.T) {
	store, hugo, albin := twoAccounts(t)
	first, _ := store.CreateInvitation(hugo.ID, "net-lab", "albin", "member")
	if _, err := store.RespondToInvitation(albin.ID, first.ID, false); err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateInvitation(hugo.ID, "net-lab", "albin", "admin")
	if err != nil {
		t.Fatalf("re-inviting after a decline failed: %v", err)
	}
	if second.Status != "pending" || second.Role != "admin" {
		t.Fatalf("re-invitation = %+v", second)
	}
	if _, err := store.RespondToInvitation(albin.ID, second.ID, true); err != nil {
		t.Fatal(err)
	}
}

// TestSearchNeedsSomethingToGoOn keeps the endpoint a completion aid rather
// than a way to page through everybody who has an account.
func TestSearchNeedsSomethingToGoOn(t *testing.T) {
	store, _, _ := twoAccounts(t)
	for _, query := range []string{"", " ", "a"} {
		found, err := store.SearchAccounts(query, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(found) != 0 {
			t.Errorf("search(%q) returned %d accounts, want none", query, len(found))
		}
	}
	found, err := store.SearchAccounts("alb", 10)
	if err != nil || len(found) != 1 || found[0].Username != "albin" {
		t.Fatalf("search(\"alb\") = %+v, %v", found, err)
	}
	// A password hash must never leave this package through a search.
	if found[0].PasswordHash != "" {
		t.Error("account search returned a password hash")
	}
}

func TestRevokingRemovesAPendingInvitation(t *testing.T) {
	store, hugo, albin := twoAccounts(t)
	invite, _ := store.CreateInvitation(hugo.ID, "net-lab", "albin", "member")
	if _, err := store.RevokeInvitation(hugo.ID, invite.ID); err != nil {
		t.Fatal(err)
	}
	waiting, err := store.InvitationsForAccount(albin.ID)
	if err != nil || len(waiting) != 0 {
		t.Fatalf("invitations after revoking = %+v, %v", waiting, err)
	}
}

// TestGitHubLoginTravelsWithAnAccount is what makes inviting somebody to a
// project by their Plainshow name reach GitHub at all. Repository collaborators
// are GitHub logins; an account name is not one, and a node is the only place
// the two are known together.
func TestGitHubLoginTravelsWithAnAccount(t *testing.T) {
	store, _, albin := twoAccounts(t)

	// Before their node reports it, an account has no GitHub name — and that is
	// a normal state, not an error: access is still granted locally.
	found, err := store.SearchAccounts("albin", 10)
	if err != nil || len(found) != 1 {
		t.Fatalf("search = %+v, %v", found, err)
	}
	if found[0].GitHubLogin != "" {
		t.Errorf("an account nobody has connected reported %q", found[0].GitHubLogin)
	}

	if err := store.SetGitHubLogin(albin.ID, "albin-gh"); err != nil {
		t.Fatal(err)
	}
	found, err = store.SearchAccounts("albin", 10)
	if err != nil || len(found) != 1 || found[0].GitHubLogin != "albin-gh" {
		t.Fatalf("after connecting GitHub, search = %+v, %v", found, err)
	}
	// Still no password hash, however many columns the query grows.
	if found[0].PasswordHash != "" {
		t.Error("account search returned a password hash")
	}
}
