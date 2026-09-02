package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"
)

func (s *Store) AuthEnabled() (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM account WHERE password_hash<>''`).Scan(&n)
	return n > 0, err
}
func (s *Store) AccountByUsername(username string) (Account, error) {
	var a Account
	err := s.db.QueryRow(`SELECT id,username,display_name,public_key,password_hash,created_at FROM account WHERE lower(username)=lower(?)`, username).Scan(&a.ID, &a.Username, &a.DisplayName, &a.PublicKey, &a.PasswordHash, &a.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// SetAccountPassword names an existing account and gives it a password.
//
// It reports an error when no such account exists. An UPDATE that matches
// nothing returns no error of its own, so without this check a caller with the
// wrong id is told the password was set when nothing happened.
func (s *Store) SetAccountPassword(id, username, displayName, hash string) error {
	result, err := s.db.Exec(
		`UPDATE account SET username=?,display_name=?,password_hash=? WHERE id=?`,
		username, displayName, hash, id)
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
func SessionHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func (s *Store) CreateSession(token, accountID string, expires time.Time) error {
	_, err := s.db.Exec(`INSERT INTO login_session(token_hash,account_id,expires_at,created_at) VALUES(?,?,?,?)`, SessionHash(token), accountID, expires.UTC().Format(time.RFC3339), Now())
	return err
}
func (s *Store) SessionAccount(token string) (Account, error) {
	var a Account
	err := s.db.QueryRow(`SELECT a.id,a.username,a.display_name,a.public_key,a.password_hash,a.created_at FROM login_session s JOIN account a ON a.id=s.account_id WHERE s.token_hash=? AND s.expires_at>?`, SessionHash(token), Now()).Scan(&a.ID, &a.Username, &a.DisplayName, &a.PublicKey, &a.PasswordHash, &a.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}
func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM login_session WHERE token_hash=?`, SessionHash(token))
	return err
}
