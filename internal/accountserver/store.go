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
	// ErrRegistrationClosed means no additional account may be created.
	ErrRegistrationClosed = errors.New("registration is closed")
	// ErrNodeOwner means another account already registered the same node id.
	ErrNodeOwner = errors.New("node belongs to another account")
	// ErrNetworkKey prevents an account from claiming a network by guessing its ID.
	ErrNetworkKey = errors.New("network management key is invalid")
	// ErrNetworkMember means a valid network key was presented by an account
	// that has not been invited by a network administrator.
	ErrNetworkMember = errors.New("account is not a member of the network")
	// ErrNetworkDeleted prevents stale devices from resurrecting a network.
	ErrNetworkDeleted = errors.New("network was deleted")
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
	// GitHubLogin is what this person is called on GitHub, published by a node
	// when they connect their account there. Without it a project invitation
	// can grant access here and nowhere else: repository collaborators are
	// GitHub logins, and an account name is not one.
	GitHubLogin string `json:"github_login"`
	Admin       bool   `json:"admin"`
	Disabled    bool   `json:"disabled"`
	Created     string `json:"created_at"`
	LastLogin   string `json:"last_login_at"`
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

// NodeCheckIn is aggregate metadata plus the private endpoint and public
// identity needed for peers in the same network to make their first direct
// connection. It has no project names, commands, logs, datasets or artifacts.
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
	Address      string       `json:"address"`
	PublicKey    string       `json:"public_key"`
	Fingerprint  string       `json:"fingerprint"`
	// ActiveNetwork is the network this device is working in right now. It is
	// also the acknowledgement of a move somebody asked for from elsewhere.
	ActiveNetwork string `json:"active_network"`
	// GitHubLogin is the owner's GitHub name as this node knows it. Reported
	// here because a node is where the two identities meet: it holds the token.
	GitHubLogin string `json:"github_login"`
	// CPUCores, RAMTotalMB and GPUs say what this machine actually is. A count
	// of graphics cards cannot answer "which machine should run this", which is
	// the question the device list exists to answer.
	CPUCores   int    `json:"cpu_cores"`
	RAMTotalMB int    `json:"ram_total_mb"`
	GPUs       string `json:"gpus"`
}

// NodeInstructions is what the account service asks a device to do next.
//
// It rides back on the heartbeat rather than travelling over a connection of
// its own. A device that is asleep or offline is not a delivery failure — it
// gets its instruction when it comes back, which is the only time it could
// have acted on it anyway.
type NodeInstructions struct {
	DesiredNetwork string `json:"desired_network"`
	SignOut        bool   `json:"sign_out"`
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

// NetworkBootstrapNode is enough for one peer-to-peer connection. Once that
// edge exists the mesh exchanges richer policy, capacity and project state
// directly without sending it through the account service.
type NetworkBootstrapNode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	Address     string `json:"address"`
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
	LastSeen    string `json:"last_seen"`
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
	columns := []struct{ table, name, definition string }{
		{"account", "github_login", "TEXT NOT NULL DEFAULT ''"},
		// The GitHub token, so connecting it on one machine connects it on
		// all of them. It is a credential this service now holds on
		// somebody's behalf, which is a real escalation of what a lost
		// database costs — see SetGitHubToken.
		{"account", "github_token", "TEXT NOT NULL DEFAULT ''"},
		{"network", "owner_account_id", "TEXT NOT NULL DEFAULT ''"},
		{"network", "management_key", "TEXT NOT NULL DEFAULT ''"},
		// A device reports which network it is working in, and is told which
		// one it should be working in. Two columns rather than one: the
		// difference between them is what "moving" looks like while it happens.
		{"node", "active_network", "TEXT NOT NULL DEFAULT ''"},
		{"node", "desired_network", "TEXT NOT NULL DEFAULT ''"},
		// Set when somebody signs a device out from another machine. The device
		// clears it on its next check-in, which is also the acknowledgement.
		{"node", "sign_out_at", "TEXT NOT NULL DEFAULT ''"},
		// What the machine actually is. A count of graphics cards cannot
		// answer "which machine should run this", which is the question the
		// device list exists to answer.
		{"node", "cpu_cores", "INTEGER NOT NULL DEFAULT 0"},
		{"node", "ram_total_mb", "INTEGER NOT NULL DEFAULT 0"},
		{"node", "gpus", "TEXT NOT NULL DEFAULT '[]'"},
		{"node", "address", "TEXT NOT NULL DEFAULT ''"},
		{"node", "public_key", "TEXT NOT NULL DEFAULT ''"},
		{"node", "fingerprint", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, column := range columns {
		has, err := s.hasColumn(column.table, column.name)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		statement := "ALTER TABLE " + column.table + " ADD COLUMN " + column.name + " " + column.definition
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("%s: %w", statement, err)
		}
	}
	return nil
}

// hasColumn checks the database rather than a version counter, so migrations
// are safe to re-run on a fresh database and on an old one.
func (s *Store) hasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primary int
		var name, kind string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primary); err != nil {
			return false, err
		}
		if name == column {
			return true, rows.Err()
		}
	}
	return false, rows.Err()
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// TokenHash hashes session secrets before durable storage.
func TokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// CreateAccount atomically makes the first account an administrator. Later
// accounts obey RegistrationOpen.
func (s *Store) CreateAccount(account Account, registrationOpen bool) error {
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

// AccountByID returns one identity, for showing somebody their own profile.
func (s *Store) AccountByID(id string) (Account, error) {
	var account Account
	err := s.db.QueryRow(`SELECT id,username,display_name,password_hash,is_admin,
        disabled,github_login,created_at,last_login_at FROM account WHERE id=?`, id).Scan(
		&account.ID, &account.Username, &account.DisplayName, &account.PasswordHash,
		&account.Admin, &account.Disabled, &account.GitHubLogin,
		&account.Created, &account.LastLogin)
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
func (s *Store) CheckIn(accountID string, input NodeCheckIn) (NodeInstructions, error) {
	var instructions NodeInstructions
	tx, err := s.db.Begin()
	if err != nil {
		return instructions, err
	}
	defer tx.Rollback()
	var owner string
	err = tx.QueryRow(`SELECT owner_account_id FROM node WHERE id=?`, input.ID).Scan(&owner)
	if err == nil && owner != accountID {
		return instructions, ErrNodeOwner
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return instructions, err
	}
	seen := now()
	_, err = tx.Exec(`INSERT INTO node
        (id,owner_account_id,name,version,os,arch,gpu_count,project_count,running_jobs,
         active_network,address,public_key,fingerprint,cpu_cores,ram_total_mb,gpus,
         last_seen,created_at)
        VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
        ON CONFLICT(id) DO UPDATE SET name=excluded.name,version=excluded.version,
          os=excluded.os,arch=excluded.arch,gpu_count=excluded.gpu_count,
          project_count=excluded.project_count,running_jobs=excluded.running_jobs,
          active_network=excluded.active_network,address=excluded.address,
          public_key=excluded.public_key,fingerprint=excluded.fingerprint,
          cpu_cores=excluded.cpu_cores,ram_total_mb=excluded.ram_total_mb,
          gpus=excluded.gpus,
          last_seen=excluded.last_seen`, input.ID, accountID, input.Name, input.Version,
		input.OS, input.Arch, input.GPUCount, input.ProjectCount, input.RunningJobs,
		input.ActiveNetwork, input.Address, input.PublicKey, input.Fingerprint,
		input.CPUCores, input.RAMTotalMB, defaultJSON(input.GPUs), seen, seen)
	if err != nil {
		return instructions, err
	}

	var desired, signOutAt string
	if err := tx.QueryRow(`SELECT desired_network,sign_out_at FROM node WHERE id=?`,
		input.ID).Scan(&desired, &signOutAt); err != nil {
		return instructions, err
	}
	// A device that reports the network it was asked to move to has finished
	// moving, so the request is spent. Leaving it set would make the interface
	// show a pending move that already happened.
	if desired != "" && desired == input.ActiveNetwork {
		if _, err := tx.Exec(`UPDATE node SET desired_network='' WHERE id=?`, input.ID); err != nil {
			return instructions, err
		}
		desired = ""
	}
	instructions.DesiredNetwork = desired
	if signOutAt != "" {
		// Handed over once. A device that signs out stops checking in, so
		// waiting for an acknowledgement that can never arrive would re-sign it
		// out every time it was signed back in.
		instructions.SignOut = true
		if _, err := tx.Exec(`UPDATE node SET sign_out_at='' WHERE id=?`, input.ID); err != nil {
			return instructions, err
		}
	}
	if _, err := tx.Exec(`DELETE FROM node_network WHERE node_id=?`, input.ID); err != nil {
		return instructions, err
	}
	for _, network := range input.Networks {
		if network.ID == "" || network.Name == "" {
			continue
		}
		result, err := tx.Exec(`UPDATE network SET name=?,last_seen=? WHERE id=? AND EXISTS (
            SELECT 1 FROM network_member WHERE network_id=? AND account_id=?)`,
			network.Name, seen, network.ID, network.ID, accountID)
		if err != nil {
			return instructions, err
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO node_network(node_id,network_id) VALUES(?,?)`,
			input.ID, network.ID); err != nil {
			return instructions, err
		}
	}
	return instructions, tx.Commit()
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
	var deleted int
	if err := tx.QueryRow(`SELECT count(*) FROM network_tombstone WHERE network_id=?`, input.ID).Scan(&deleted); err != nil {
		return EnterpriseNetwork{}, err
	}
	if deleted != 0 {
		return EnterpriseNetwork{}, ErrNetworkDeleted
	}
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

// DeleteNetwork permanently removes a network owned by actor and records a
// tombstone for every member so stale device configurations cannot recreate it.
func (s *Store) DeleteNetwork(actorID, networkID, managementKey string) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var owner, key string
	if err := tx.QueryRow(`SELECT owner_account_id,management_key FROM network WHERE id=?`, networkID).Scan(&owner, &key); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if owner != actorID {
		return nil, errors.New("only the network owner can delete the network")
	}
	if subtle.ConstantTimeCompare([]byte(key), []byte(managementKey)) != 1 {
		return nil, ErrNetworkKey
	}
	rows, err := tx.Query(`SELECT account_id FROM network_member WHERE network_id=?`, networkID)
	if err != nil {
		return nil, err
	}
	accounts := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		accounts = append(accounts, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO network_tombstone(network_id,deleted_at) VALUES(?,?)
		ON CONFLICT(network_id) DO UPDATE SET deleted_at=excluded.deleted_at`, networkID, now()); err != nil {
		return nil, err
	}
	for _, accountID := range accounts {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO network_tombstone_member(network_id,account_id) VALUES(?,?)`, networkID, accountID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(`UPDATE node SET active_network='',desired_network=''
		WHERE active_network=? OR desired_network=?`, networkID, networkID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM network WHERE id=?`, networkID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return accounts, nil
}

func (s *Store) DeletedNetworksForAccount(accountID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT network_id FROM network_tombstone_member WHERE account_id=?`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
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
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	Role          string                 `json:"role"`
	ManagementKey string                 `json:"management_key"`
	Owner         bool                   `json:"owner"`
	Members       int                    `json:"members"`
	Nodes         int                    `json:"nodes"`
	LastSeen      string                 `json:"last_seen"`
	Joined        string                 `json:"joined_at"`
	Devices       []NetworkBootstrapNode `json:"devices"`
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
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range out {
		devices, err := s.bootstrapNodes(out[index].ID)
		if err != nil {
			return nil, err
		}
		out[index].Devices = devices
	}
	return out, nil
}

func (s *Store) bootstrapNodes(networkID string) ([]NetworkBootstrapNode, error) {
	rows, err := s.db.Query(`SELECT n.id,n.name,n.os,n.arch,n.address,n.public_key,n.fingerprint,n.last_seen
		FROM node n JOIN node_network nn ON nn.node_id=n.id
		WHERE nn.network_id=? AND n.address<>'' AND n.fingerprint<>''
		ORDER BY n.last_seen DESC`, networkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NetworkBootstrapNode{}
	for rows.Next() {
		var node NetworkBootstrapNode
		if err := rows.Scan(&node.ID, &node.Name, &node.OS, &node.Arch, &node.Address,
			&node.PublicKey, &node.Fingerprint, &node.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, node)
	}
	return out, rows.Err()
}
