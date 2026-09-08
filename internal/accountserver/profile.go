package accountserver

// Changing your own account.
//
// Separate from the administrator's account endpoints on purpose. An admin
// disabling somebody and a person renaming themselves are different powers, and
// sharing a handler between them is how one quietly becomes the other.

import (
	"errors"
	"net/http"
	"strings"

	"github.com/huggan360/plainshow-cluster/internal/auth"
)

// ErrWrongPassword means the current password did not match.
var ErrWrongPassword = errors.New("current password is incorrect")

// SetDisplayName changes what an account is called.
//
// The username is not changeable here. It is what project membership, GitHub
// sync and invitations are keyed by, so renaming it would silently detach
// somebody from their own work.
func (s *Store) SetDisplayName(accountID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return errors.New("a display name must be between 1 and 64 characters")
	}
	result, err := s.db.Exec(`UPDATE account SET display_name=? WHERE id=?`, name, accountID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

// ChangePassword replaces a password, proving the old one first.
//
// Every other session stays valid. Ending them would be defensible, but a
// person changing their password on a laptop should not find their training run
// disowned on a machine in another room.
func (s *Store) ChangePassword(accountID, current, next string) error {
	if len(next) < 10 {
		return errors.New("a password must be at least 10 characters")
	}
	var hash string
	if err := s.db.QueryRow(`SELECT password_hash FROM account WHERE id=?`,
		accountID).Scan(&hash); err != nil {
		return ErrNotFound
	}
	if !auth.VerifyPassword(hash, current) {
		return ErrWrongPassword
	}
	replacement, err := auth.HashPassword(next)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE account SET password_hash=? WHERE id=?`, replacement, accountID)
	return err
}

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	var body struct {
		DisplayName string `json:"display_name"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.SetDisplayName(account.ID, body.DisplayName); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := s.store.AccountByID(account.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	var body struct {
		Current string `json:"current_password"`
		Next    string `json:"new_password"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	err := s.store.ChangePassword(account.ID, body.Current, body.Next)
	switch {
	case errors.Is(err, ErrWrongPassword):
		fail(w, http.StatusForbidden, "That is not your current password.")
	case err != nil:
		fail(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "changed"})
	}
}
