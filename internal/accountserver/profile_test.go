package accountserver

import (
	"errors"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/auth"
)

// TestChangingAPasswordProvesTheOldOne. Without that, anyone who reaches a
// signed-in browser — a shared machine, a forgotten session — can lock the
// owner out of their own account.
func TestChangingAPasswordProvesTheOldOne(t *testing.T) {
	store := openTestStore(t)
	hash, err := auth.HashPassword("original-password")
	if err != nil {
		t.Fatal(err)
	}
	account := Account{ID: "a1", Username: "hugo", DisplayName: "Hugo", PasswordHash: hash}
	if err := store.CreateAccount(account, false); err != nil {
		t.Fatal(err)
	}

	if err := store.ChangePassword("a1", "wrong", "a-new-password"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("a wrong current password was accepted: %v", err)
	}
	// Still the old one.
	current, _ := store.AccountByID("a1")
	if !auth.VerifyPassword(current.PasswordHash, "original-password") {
		t.Fatal("a failed change altered the password anyway")
	}

	if err := store.ChangePassword("a1", "original-password", "short"); err == nil {
		t.Error("a password below the minimum was accepted")
	}
	if err := store.ChangePassword("a1", "original-password", "a-new-password"); err != nil {
		t.Fatal(err)
	}
	changed, _ := store.AccountByID("a1")
	if !auth.VerifyPassword(changed.PasswordHash, "a-new-password") {
		t.Fatal("the new password does not verify")
	}
}

// TestTheUsernameIsNotRenamable: project membership, invitations and GitHub
// sync are all keyed by it, so only the display name may change.
func TestTheUsernameIsNotRenamable(t *testing.T) {
	store, hugo, _ := twoAccounts(t)

	if err := store.SetDisplayName(hugo.ID, "Hugo Hansson"); err != nil {
		t.Fatal(err)
	}
	updated, err := store.AccountByID(hugo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName != "Hugo Hansson" {
		t.Errorf("display name = %q", updated.DisplayName)
	}
	if updated.Username != "huggan360" {
		t.Errorf("the username changed to %q", updated.Username)
	}
	if err := store.SetDisplayName(hugo.ID, "   "); err == nil {
		t.Error("a blank display name was accepted")
	}
	if err := store.SetDisplayName("nobody", "Ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("renaming a missing account = %v", err)
	}
}
