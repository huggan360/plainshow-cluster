package accountserver

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

var (
	// ErrNotFound is returned when an account or session does not exist.
	ErrNotFound = errors.New("not found")
	// ErrRegistrationClosed means only the bootstrap account may be created.
	ErrRegistrationClosed = errors.New("registration is closed")
	// ErrBootstrapToken means the first administrator token was wrong.
	ErrBootstrapToken = errors.New("bootstrap token is invalid")
	// ErrNodeOwner means another account already registered the same node id.
	ErrNodeOwner = errors.New("node belongs to another account")
	// ErrNetworkKey prevents an account from claiming a network by guessing its ID.
	ErrNetworkKey = errors.New("network management key is invalid")
	// ErrNetworkMember means a valid network key was presented by an account
	// that has not been invited by a network administrator.
	ErrNetworkMember = errors.New("account is not a member of the network")
	// ErrControllerOwner prevents another account from taking over a controller.
	ErrControllerOwner = errors.New("controller belongs to another account")
	// ErrControllerAccess means the account cannot use or configure a controller.
	ErrControllerAccess = errors.New("account cannot access this controller")
)

// Account is a global Plainshow identity.
type Account struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	DisplayName  string `json:"display_name"`
	PasswordHash string `json:"-"`
	Admin        bool   `json:"admin"`
	Disabled     bool   `json:"disabled"`
	Created      string `json:"created_at"`
	LastLogin    string `json:"last_login_at"`
}

// NetworkRef is the non-sensitive network identity a node reports for global
// counts and administration.
type NetworkRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// NetworkRegistration proves membership using the high-entropy key carried by
// every joined device. The key is created with the peer network and backed up
// by the enterprise master for recovery and administration.
type NetworkRegistration struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ManagementKey string `json:"management_key"`
	Role          string `json:"role"`
}

// EnterpriseNetwork is the Pi's canonical account/key registry entry.
type EnterpriseNetwork struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	OwnerAccountID string `json:"owner_account_id"`
	ManagementKey  string `json:"management_key,omitempty"`
	Members        int    `json:"members"`
	Nodes          int    `json:"nodes"`
	LastSeen       string `json:"last_seen"`
	Created        string `json:"created_at"`
}

// EnterpriseMember is the non-secret account identity and role shared with
// devices that have proved membership in the same network.
type EnterpriseMember struct {
	AccountID   string `json:"account_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Joined      string `json:"joined_at"`
}

// NodeCheckIn is aggregate metadata only. It intentionally has no project
// names, commands, logs, addresses, keys, datasets or artifacts.
type NodeCheckIn struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Version      string       `json:"version"`
	OS           string       `json:"os"`
	Arch         string       `json:"arch"`
	GPUCount     int          `json:"gpu_count"`
	ProjectCount int          `json:"project_count"`
	RunningJobs  int          `json:"running_jobs"`
	Networks     []NetworkRef `json:"networks"`
}

// Node is one globally registered device.
type Node struct {
	ID             string `json:"id"`
	OwnerAccountID string `json:"owner_account_id"`
	Name           string `json:"name"`
	Version        string `json:"version"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	GPUCount       int    `json:"gpu_count"`
	ProjectCount   int    `json:"project_count"`
	RunningJobs    int    `json:"running_jobs"`
	LastSeen       string `json:"last_seen"`
	Created        string `json:"created_at"`
}

// Stats is the intentionally small global overview.
type Stats struct {
	Accounts    int `json:"accounts"`
	Disabled    int `json:"disabled_accounts"`
	Nodes       int `json:"nodes"`
	OnlineNodes int `json:"online_nodes"`
	Networks    int `json:"networks"`
	GPUs        int `json:"gpus"`
	Projects    int `json:"projects"`
	RunningJobs int `json:"running_jobs"`
	Controllers int `json:"controllers"`
}

// Store owns the account server's independent SQLite database.
type Store struct{ db *sql.DB }

// Open creates or opens the central account database.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// This service is tiny; serial writes make bootstrap and ownership checks
	// deterministic without adding a distributed transaction problem.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply account schema: %w", err)
	}
	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate account schema: %w", err)
	}
	return store, nil
}

func (s *Store) migrate() error {
	columns := []struct{ name, definition string }{
		{"owner_account_id", "TEXT NOT NULL DEFAULT ''"},
		{"management_key", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, column := range columns {
		rows, err := s.db.Query(`PRAGMA table_info(network)`)
		if err != nil {
			return err
		}
		found := false
		for rows.Next() {
			var cid, notNull, primary int
			var name, kind string
			var defaultValue sql.NullString
			if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primary); err != nil {
				rows.Close()
				return err
			}
			found = found || name == column.name
		}
		rows.Close()
		if !found {
			if _, err := s.db.Exec("ALTER TABLE network ADD COLUMN " + column.name + " " + column.definition); err != nil {
				return err
			}
		}
	}
	return nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// TokenHash hashes bootstrap and session secrets before durable storage.
func TokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// InitialiseBootstrap records the first-admin token hash only while there are
// no accounts. Re-running init cannot replace a live system's credential.
func (s *Store) InitialiseBootstrap(hash string) error {
	var accounts int
	if err := s.db.QueryRow(`SELECT count(*) FROM account`).Scan(&accounts); err != nil {
		return err
	}
	if accounts != 0 {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO setting(name,value) VALUES('bootstrap_hash',?)
        ON CONFLICT(name) DO NOTHING`, hash)
	return err
}

// CreateAccount atomically makes the first account an administrator after
// checking the one-time bootstrap token. Later accounts obey RegistrationOpen.
func (s *Store) CreateAccount(account Account, bootstrapHash string, registrationOpen bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM account`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		var want string
		if err := tx.QueryRow(`SELECT value FROM setting WHERE name='bootstrap_hash'`).Scan(&want); err != nil ||
			subtle.ConstantTimeCompare([]byte(want), []byte(bootstrapHash)) != 1 {
			return ErrBootstrapToken
		}
		account.Admin = true
	} else if !registrationOpen {
		return ErrRegistrationClosed
	}
	account.Created = now()
	_, err = tx.Exec(`INSERT INTO account
        (id,username,display_name,password_hash,is_admin,disabled,created_at,last_login_at)
        VALUES(?,?,?,?,?,?,?,?)`, account.ID, account.Username, account.DisplayName,
		account.PasswordHash, account.Admin, false, account.Created, "")
	if err != nil {
		return err
	}
	if count == 0 {
		if _, err := tx.Exec(`DELETE FROM setting WHERE name='bootstrap_hash'`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AccountByUsername returns an enabled or disabled account for credential
// checking and administration.
func (s *Store) AccountByUsername(username string) (Account, error) {
	var account Account
	err := s.db.QueryRow(`SELECT id,username,display_name,password_hash,is_admin,
        disabled,created_at,last_login_at FROM account WHERE username=?`, username).Scan(
		&account.ID, &account.Username, &account.DisplayName, &account.PasswordHash,
		&account.Admin, &account.Disabled, &account.Created, &account.LastLogin)
	if errors.Is(err, sql.ErrNoRows) {
		return account, ErrNotFound
	}
	return account, err
}

// Accounts lists identities without password material.
func (s *Store) Accounts() ([]Account, error) {
	rows, err := s.db.Query(`SELECT id,username,display_name,is_admin,disabled,
        created_at,last_login_at FROM account ORDER BY lower(username)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		var account Account
		if err := rows.Scan(&account.ID, &account.Username, &account.DisplayName,
			&account.Admin, &account.Disabled, &account.Created, &account.LastLogin); err != nil {
			return nil, err
		}
		out = append(out, account)
	}
	return out, rows.Err()
}

// SetAccountDisabled enables or disables an account.
func (s *Store) SetAccountDisabled(id string, disabled bool) error {
	result, err := s.db.Exec(`UPDATE account SET disabled=? WHERE id=?`, disabled, id)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrNotFound
	}
	if disabled {
		_, _ = s.db.Exec(`DELETE FROM login_session WHERE account_id=?`, id)
	}
	return nil
}

// CreateSession stores only a digest of the bearer token.
func (s *Store) CreateSession(token, accountID string, expires time.Time) error {
	_, err := s.db.Exec(`INSERT INTO login_session(token_hash,account_id,expires_at,created_at)
		VALUES(?,?,?,?)`, TokenHash(token), accountID, expires.UTC().Format(time.RFC3339), now())
	if err == nil {
		_, _ = s.db.Exec(`UPDATE account SET last_login_at=? WHERE id=?`, now(), accountID)
	}
	return err
}

// SessionAccount resolves a live session and rejects disabled identities.
func (s *Store) SessionAccount(token string) (Account, error) {
	var account Account
	err := s.db.QueryRow(`SELECT a.id,a.username,a.display_name,a.is_admin,
        a.disabled,a.created_at,a.last_login_at FROM login_session s
        JOIN account a ON a.id=s.account_id
		WHERE s.token_hash=? AND s.expires_at>? AND a.disabled=0`, TokenHash(token), now()).Scan(
		&account.ID, &account.Username, &account.DisplayName, &account.Admin,
		&account.Disabled, &account.Created, &account.LastLogin)
	if errors.Is(err, sql.ErrNoRows) {
		return account, ErrNotFound
	}
	return account, err
}

// DeleteSession revokes one bearer token.
func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM login_session WHERE token_hash=?`, TokenHash(token))
	return err
}

// CheckIn records aggregate node counts while preventing one account from
// taking over a node id already registered by another.
func (s *Store) CheckIn(accountID string, input NodeCheckIn) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner string
	err = tx.QueryRow(`SELECT owner_account_id FROM node WHERE id=?`, input.ID).Scan(&owner)
	if err == nil && owner != accountID {
		return ErrNodeOwner
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	seen := now()
	_, err = tx.Exec(`INSERT INTO node
        (id,owner_account_id,name,version,os,arch,gpu_count,project_count,running_jobs,last_seen,created_at)
        VALUES(?,?,?,?,?,?,?,?,?,?,?)
        ON CONFLICT(id) DO UPDATE SET name=excluded.name,version=excluded.version,
          os=excluded.os,arch=excluded.arch,gpu_count=excluded.gpu_count,
          project_count=excluded.project_count,running_jobs=excluded.running_jobs,
          last_seen=excluded.last_seen`, input.ID, accountID, input.Name, input.Version,
		input.OS, input.Arch, input.GPUCount, input.ProjectCount, input.RunningJobs, seen, seen)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM node_network WHERE node_id=?`, input.ID); err != nil {
		return err
	}
	for _, network := range input.Networks {
		if network.ID == "" || network.Name == "" {
			continue
		}
		result, err := tx.Exec(`UPDATE network SET name=?,last_seen=? WHERE id=? AND EXISTS (
            SELECT 1 FROM network_member WHERE network_id=? AND account_id=?)`,
			network.Name, seen, network.ID, network.ID, accountID)
		if err != nil {
			return err
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO node_network(node_id,network_id) VALUES(?,?)`,
			input.ID, network.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func validNetworkRole(role string) bool {
	switch role {
	case "owner", "admin", "operator", "member", "viewer":
		return true
	default:
		return false
	}
}

func randomSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// RegisterNetwork creates or proves access to an enterprise network registry
// entry and records the global account's network role.
func (s *Store) RegisterNetwork(accountID string, input NetworkRegistration) (EnterpriseNetwork, error) {
	input.ID, input.Name = strings.TrimSpace(input.ID), strings.TrimSpace(input.Name)
	if input.ID == "" || input.Name == "" || len(input.ManagementKey) < 32 || !validNetworkRole(input.Role) {
		return EnterpriseNetwork{}, errors.New("network registration is incomplete")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return EnterpriseNetwork{}, err
	}
	defer tx.Rollback()
	var network EnterpriseNetwork
	err = tx.QueryRow(`SELECT id,name,owner_account_id,management_key,last_seen,created_at
        FROM network WHERE id=?`, input.ID).Scan(&network.ID, &network.Name,
		&network.OwnerAccountID, &network.ManagementKey,
		&network.LastSeen, &network.Created)
	seen := now()
	if errors.Is(err, sql.ErrNoRows) {
		input.Role = "owner"
		network = EnterpriseNetwork{ID: input.ID, Name: input.Name,
			OwnerAccountID: accountID, ManagementKey: input.ManagementKey,
			LastSeen: seen, Created: seen}
		_, err = tx.Exec(`INSERT INTO network
			(id,name,owner_account_id,management_key,last_seen,created_at)
			VALUES(?,?,?,?,?,?)`, network.ID, network.Name, network.OwnerAccountID,
			network.ManagementKey, seen, seen)
		if err != nil {
			return network, err
		}
	} else if err != nil {
		return network, err
	} else {
		if network.ManagementKey == "" {
			input.Role = "owner"
			network.OwnerAccountID, network.ManagementKey = accountID, input.ManagementKey
			if _, err := tx.Exec(`UPDATE network SET owner_account_id=?,management_key=? WHERE id=?`,
				accountID, input.ManagementKey, input.ID); err != nil {
				return network, err
			}
		}
		if subtle.ConstantTimeCompare([]byte(network.ManagementKey), []byte(input.ManagementKey)) != 1 {
			return network, ErrNetworkKey
		}
		network.Name, network.LastSeen = input.Name, seen
		if _, err := tx.Exec(`UPDATE network SET name=?,last_seen=? WHERE id=?`, input.Name, seen, input.ID); err != nil {
			return network, err
		}
	}
	var existingRole string
	memberErr := tx.QueryRow(`SELECT role FROM network_member WHERE network_id=? AND account_id=?`,
		input.ID, accountID).Scan(&existingRole)
	if errors.Is(memberErr, sql.ErrNoRows) {
		if network.OwnerAccountID != accountID {
			return network, ErrNetworkMember
		}
		existingRole = "owner"
		if _, err := tx.Exec(`INSERT INTO network_member(network_id,account_id,role,joined_at)
            VALUES(?,?,?,?)`, input.ID, accountID, existingRole, seen); err != nil {
			return network, err
		}
	} else if memberErr != nil {
		return network, memberErr
	}
	if err := tx.Commit(); err != nil {
		return network, err
	}
	return network, nil
}

// GrantNetworkMember is called by the inviting peer after it has authenticated
// the joining global account and consumed the peer invitation.
func (s *Store) GrantNetworkMember(actorID, networkID, managementKey, accountID, role string) error {
	if !validNetworkRole(role) || role == "owner" {
		return errors.New("invalid invited network role")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var key, actorRole string
	if err := tx.QueryRow(`SELECT management_key FROM network WHERE id=?`, networkID).Scan(&key); err != nil {
		return ErrNotFound
	}
	if subtle.ConstantTimeCompare([]byte(key), []byte(managementKey)) != 1 {
		return ErrNetworkKey
	}
	if err := tx.QueryRow(`SELECT role FROM network_member WHERE network_id=? AND account_id=?`,
		networkID, actorID).Scan(&actorRole); err != nil || (actorRole != "owner" && actorRole != "admin") {
		return errors.New("network administrator access is required")
	}
	var exists int
	if err := tx.QueryRow(`SELECT count(*) FROM account WHERE id=? AND disabled=0`, accountID).Scan(&exists); err != nil || exists != 1 {
		return errors.New("joining account does not exist or is disabled")
	}
	// Never demote or replace a role already assigned by an administrator.
	var current string
	if err := tx.QueryRow(`SELECT role FROM network_member WHERE network_id=? AND account_id=?`,
		networkID, accountID).Scan(&current); err == nil {
		return tx.Commit()
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO network_member(network_id,account_id,role,joined_at)
        VALUES(?,?,?,?)`, networkID, accountID, role, now()); err != nil {
		return err
	}
	return tx.Commit()
}

// NetworkMembers lists identities inside one network. Callers authenticate
// and prove network membership before this is returned by the HTTP layer.
func (s *Store) NetworkMembers(networkID string) ([]EnterpriseMember, error) {
	rows, err := s.db.Query(`SELECT m.account_id,a.username,a.display_name,m.role,m.joined_at
		FROM network_member m JOIN account a ON a.id=m.account_id
		WHERE m.network_id=? ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END,
		lower(a.username)`, networkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []EnterpriseMember{}
	for rows.Next() {
		var member EnterpriseMember
		if err := rows.Scan(&member.AccountID, &member.Username, &member.DisplayName,
			&member.Role, &member.Joined); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

// SetNetworkMemberRole changes a non-owner member. The enterprise registry is
// authoritative, so devices cannot create a local privilege that disappears
// on the next account check-in.
func (s *Store) SetNetworkMemberRole(actorID, networkID, managementKey, accountID, role string) error {
	if !validNetworkRole(role) || role == "owner" {
		return errors.New("invalid network role")
	}
	return s.changeNetworkMember(actorID, networkID, managementKey, accountID, role, false)
}

// RemoveNetworkMember removes a non-owner account from a network.
func (s *Store) RemoveNetworkMember(actorID, networkID, managementKey, accountID string) error {
	return s.changeNetworkMember(actorID, networkID, managementKey, accountID, "", true)
}

func (s *Store) changeNetworkMember(actorID, networkID, managementKey, accountID, role string, remove bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var key, actorRole, targetRole string
	if err := tx.QueryRow(`SELECT management_key FROM network WHERE id=?`, networkID).Scan(&key); err != nil {
		return ErrNotFound
	}
	if subtle.ConstantTimeCompare([]byte(key), []byte(managementKey)) != 1 {
		return ErrNetworkKey
	}
	if err := tx.QueryRow(`SELECT role FROM network_member WHERE network_id=? AND account_id=?`,
		networkID, actorID).Scan(&actorRole); err != nil || (actorRole != "owner" && actorRole != "admin") {
		return errors.New("network administrator access is required")
	}
	if err := tx.QueryRow(`SELECT role FROM network_member WHERE network_id=? AND account_id=?`,
		networkID, accountID).Scan(&targetRole); err != nil {
		return ErrNotFound
	}
	if targetRole == "owner" {
		return errors.New("the network owner cannot be changed or removed")
	}
	if remove {
		_, err = tx.Exec(`DELETE FROM network_member WHERE network_id=? AND account_id=?`, networkID, accountID)
	} else {
		_, err = tx.Exec(`UPDATE network_member SET role=? WHERE network_id=? AND account_id=?`,
			role, networkID, accountID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Networks returns the global registry with secrets included for administrators.
func (s *Store) Networks() ([]EnterpriseNetwork, error) {
	rows, err := s.db.Query(`SELECT n.id,n.name,n.owner_account_id,n.management_key,
		(SELECT count(*) FROM network_member m WHERE m.network_id=n.id),
		(SELECT count(*) FROM node_network nn WHERE nn.network_id=n.id),n.last_seen,n.created_at
        FROM network n ORDER BY lower(n.name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EnterpriseNetwork{}
	for rows.Next() {
		var network EnterpriseNetwork
		if err := rows.Scan(&network.ID, &network.Name, &network.OwnerAccountID,
			&network.ManagementKey, &network.Members,
			&network.Nodes, &network.LastSeen, &network.Created); err != nil {
			return nil, err
		}
		out = append(out, network)
	}
	return out, rows.Err()
}

func (s *Store) RotateNetworkKey(networkID string) (string, error) {
	secret, err := randomSecret()
	if err != nil {
		return "", err
	}
	result, err := s.db.Exec(`UPDATE network SET management_key=? WHERE id=?`, secret, networkID)
	if err != nil {
		return "", err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return "", ErrNotFound
	}
	return secret, nil
}

// Nodes lists globally registered devices.
func (s *Store) Nodes() ([]Node, error) {
	rows, err := s.db.Query(`SELECT id,owner_account_id,name,version,os,arch,
        gpu_count,project_count,running_jobs,last_seen,created_at
        FROM node ORDER BY last_seen DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Node{}
	for rows.Next() {
		var node Node
		if err := rows.Scan(&node.ID, &node.OwnerAccountID, &node.Name, &node.Version,
			&node.OS, &node.Arch, &node.GPUCount, &node.ProjectCount,
			&node.RunningJobs, &node.LastSeen, &node.Created); err != nil {
			return nil, err
		}
		out = append(out, node)
	}
	return out, rows.Err()
}

// Stats computes the lightweight global overview directly from indexed rows.
func (s *Store) Stats() (Stats, error) {
	var out Stats
	onlineSince := time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339)
	queries := []struct {
		query string
		value *int
	}{
		{`SELECT count(*) FROM account`, &out.Accounts},
		{`SELECT count(*) FROM account WHERE disabled=1`, &out.Disabled},
		{`SELECT count(*) FROM node`, &out.Nodes},
		{`SELECT count(*) FROM node WHERE last_seen>=?`, &out.OnlineNodes},
		{`SELECT count(*) FROM network`, &out.Networks},
		{`SELECT coalesce(sum(gpu_count),0) FROM node`, &out.GPUs},
		{`SELECT coalesce(sum(project_count),0) FROM node`, &out.Projects},
		{`SELECT coalesce(sum(running_jobs),0) FROM node`, &out.RunningJobs},
		{`SELECT count(*) FROM controller_server`, &out.Controllers},
	}
	for _, item := range queries {
		var err error
		if item.query == `SELECT count(*) FROM node WHERE last_seen>=?` {
			err = s.db.QueryRow(item.query, onlineSince).Scan(item.value)
		} else {
			err = s.db.QueryRow(item.query).Scan(item.value)
		}
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// AccountNetwork is one network an account belongs to, as the account service
// knows it. The management key is included deliberately: it is what lets a
// device the account owns take part in the network, and an account that is
// already a member has it anyway on every other machine it has signed in on.
type AccountNetwork struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Role          string `json:"role"`
	ManagementKey string `json:"management_key"`
	Owner         bool   `json:"owner"`
	Members       int    `json:"members"`
	Nodes         int    `json:"nodes"`
	LastSeen      string `json:"last_seen"`
	Joined        string `json:"joined_at"`
}

// NetworksForAccount lists every network this account is a member of.
//
// This is the read that makes an account mean something across machines. Until
// it existed the flow was push-only — a device reported the networks it already
// had — so signing in on a second machine produced an empty workspace, with no
// code path that could have said otherwise.
func (s *Store) NetworksForAccount(accountID string) ([]AccountNetwork, error) {
	rows, err := s.db.Query(`SELECT n.id, n.name, m.role, n.management_key,
            n.owner_account_id, n.last_seen, m.joined_at,
            (SELECT count(*) FROM network_member x WHERE x.network_id=n.id),
            (SELECT count(*) FROM node_network y WHERE y.network_id=n.id)
        FROM network n JOIN network_member m ON m.network_id=n.id
        WHERE m.account_id=? ORDER BY lower(n.name)`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccountNetwork{}
	for rows.Next() {
		var item AccountNetwork
		var owner string
		if err := rows.Scan(&item.ID, &item.Name, &item.Role, &item.ManagementKey,
			&owner, &item.LastSeen, &item.Joined, &item.Members, &item.Nodes); err != nil {
			return nil, err
		}
		item.Owner = owner == accountID
		out = append(out, item)
	}
	return out, rows.Err()
}
