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

// DeleteSessionsForAccount ends every signed-in browser on this machine.
//
// Signing a device out from somewhere else has to reach the browsers that are
// already open on it, not only the credential. An empty account id would match
// every row, so it is refused rather than quietly logging everyone out.
func (s *Store) DeleteSessionsForAccount(accountID string) error {
	if accountID == "" {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM login_session WHERE account_id=?`, accountID)
	return err
}

// AdoptGlobalAccount replaces the old device-shaped owner with the identity
// returned by the central Account Server. Network permissions move with it;
// device rows and cryptographic identities remain untouched.
func (s *Store) AdoptGlobalAccount(legacyID string, account Account) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if legacyID != "" && legacyID != account.ID {
		var usernameOwner string
		err := tx.QueryRow(`SELECT id FROM account WHERE lower(username)=lower(?)`,
			account.Username).Scan(&usernameOwner)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && usernameOwner == legacyID {
			// A locally claimed node often uses the same username as the global
			// identity that is replacing it. Free that unique value before the
			// global row is inserted; the placeholder cannot be a valid username
			// because both local and central validation reject slashes. Everything
			// happens inside this transaction, so it is never externally visible.
			placeholder := "/adopting/" + legacyID + "/" + account.ID
			if _, err := tx.Exec(`UPDATE account SET username=? WHERE id=?`,
				placeholder, legacyID); err != nil {
				return err
			}
		}
	}
	_, err = tx.Exec(`INSERT INTO account
        (id,username,display_name,public_key,password_hash,created_at)
        VALUES(?,?,?,?,?,?)
        ON CONFLICT(id) DO UPDATE SET username=excluded.username,
          display_name=excluded.display_name`, account.ID, account.Username,
		account.DisplayName, account.PublicKey, "", Now())
	if err != nil {
		return err
	}
	if legacyID != "" && legacyID != account.ID {
		_, err = tx.Exec(`INSERT INTO network_member
            (network_id,account_id,role,manage_network,manage_members,
             create_projects,run_jobs,manage_nodes,created_at)
            SELECT network_id,?,role,manage_network,manage_members,
             create_projects,run_jobs,manage_nodes,created_at
            FROM network_member WHERE account_id=?
            ON CONFLICT(network_id,account_id) DO UPDATE SET
             role=excluded.role,manage_network=excluded.manage_network,
             manage_members=excluded.manage_members,create_projects=excluded.create_projects,
             run_jobs=excluded.run_jobs,manage_nodes=excluded.manage_nodes`, account.ID, legacyID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE network SET owner_account_id=? WHERE owner_account_id=?`,
			account.ID, legacyID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE login_session SET account_id=? WHERE account_id=?`,
			account.ID, legacyID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM network_member WHERE account_id=?`, legacyID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM account WHERE id=?`, legacyID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
