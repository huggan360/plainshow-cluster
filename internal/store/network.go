package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/config"
)

const (
	NetworkOwner    = "owner"
	NetworkAdmin    = "admin"
	NetworkOperator = "operator"
	NetworkMember   = "member"
	NetworkViewer   = "viewer"
)

func ValidNetworkRole(role string) bool {
	switch role {
	case NetworkOwner, NetworkAdmin, NetworkOperator, NetworkMember, NetworkViewer:
		return true
	default:
		return false
	}
}

type Account struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	DisplayName  string `json:"display_name"`
	PublicKey    string `json:"public_key"`
	PasswordHash string `json:"-"`
	Created      string `json:"created_at"`
}

type Network struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	OwnerAccountID string `json:"owner_account_id"`
	Role           string `json:"role,omitempty"`
	Created        string `json:"created_at"`
	Updated        string `json:"updated_at"`
}

type NetworkPermissions struct {
	ManageNetwork  bool `json:"manage_network"`
	ManageMembers  bool `json:"manage_members"`
	CreateProjects bool `json:"create_projects"`
	RunJobs        bool `json:"run_jobs"`
	ManageNodes    bool `json:"manage_nodes"`
}

type NetworkMemberRow struct {
	NetworkID   string             `json:"network_id"`
	Account     Account            `json:"account"`
	Role        string             `json:"role"`
	Permissions NetworkPermissions `json:"permissions"`
	Created     string             `json:"created_at"`
}

func PermissionsForRole(role string) NetworkPermissions {
	switch role {
	case NetworkOwner:
		return NetworkPermissions{true, true, true, true, true}
	case NetworkAdmin:
		return NetworkPermissions{true, true, true, true, true}
	case NetworkOperator:
		return NetworkPermissions{false, false, true, true, true}
	case NetworkMember:
		return NetworkPermissions{false, false, true, true, false}
	default:
		return NetworkPermissions{}
	}
}

func (s *Store) UpsertAccount(account Account) error {
	_, err := s.db.Exec(`INSERT INTO account
        (id, username, display_name, public_key, password_hash, created_at)
        VALUES (?, ?, ?, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET username=excluded.username,
          display_name=excluded.display_name, public_key=excluded.public_key,
          password_hash=CASE WHEN excluded.password_hash='' THEN account.password_hash
                             ELSE excluded.password_hash END`,
		account.ID, account.Username, account.DisplayName, account.PublicKey,
		account.PasswordHash, Now())
	return err
}

func (s *Store) Account(id string) (Account, error) {
	var account Account
	err := s.db.QueryRow(`SELECT id, username, display_name, public_key,
        password_hash, created_at FROM account WHERE id=?`, id).Scan(
		&account.ID, &account.Username, &account.DisplayName, &account.PublicKey,
		&account.PasswordHash, &account.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return account, ErrNotFound
	}
	return account, err
}

func (s *Store) UpsertNetwork(network Network) error {
	now := Now()
	_, err := s.db.Exec(`INSERT INTO network
        (id, name, owner_account_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET name=excluded.name,
          owner_account_id=excluded.owner_account_id, updated_at=excluded.updated_at`,
		network.ID, network.Name, network.OwnerAccountID, now, now)
	return err
}

func (s *Store) AddNetworkMember(networkID, accountID, role string) error {
	if !ValidNetworkRole(role) {
		return errors.New("invalid network role")
	}
	p := PermissionsForRole(role)
	_, err := s.db.Exec(`INSERT INTO network_member
        (network_id, account_id, role, manage_network, manage_members,
         create_projects, run_jobs, manage_nodes, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(network_id, account_id) DO UPDATE SET role=excluded.role,
          manage_network=excluded.manage_network, manage_members=excluded.manage_members,
          create_projects=excluded.create_projects, run_jobs=excluded.run_jobs,
          manage_nodes=excluded.manage_nodes`,
		networkID, accountID, role, p.ManageNetwork, p.ManageMembers,
		p.CreateProjects, p.RunJobs, p.ManageNodes, Now())
	return err
}

func (s *Store) RemoveNetworkMember(networkID, accountID string) error {
	_, err := s.db.Exec(`DELETE FROM network_member WHERE network_id=? AND account_id=?`,
		networkID, accountID)
	return err
}

// DeleteNetwork removes network membership and compute state, not local
// projects, their history, or files. Legacy project network_id values also
// locate existing folders and are retained even after the network is gone.
func (s *Store) DeleteNetwork(networkID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`DELETE FROM training_run WHERE network_id=?`,
		`DELETE FROM dataset_placement WHERE dataset_id IN (SELECT id FROM dataset WHERE network_id=?)`,
		`DELETE FROM dataset WHERE network_id=?`,
		`DELETE FROM invitation WHERE network_id=?`,
		`DELETE FROM network_controller WHERE network_id=?`,
		`DELETE FROM network_node WHERE network_id=?`,
		`DELETE FROM network_member WHERE network_id=?`,
		`DELETE FROM network WHERE id=?`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement, networkID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// NetworkMember returns one account's durable role in a network.
func (s *Store) NetworkMember(networkID, accountID string) (NetworkMemberRow, error) {
	var member NetworkMemberRow
	err := s.db.QueryRow(`SELECT m.network_id,a.id,a.username,a.display_name,
        a.public_key,a.created_at,m.role,m.manage_network,m.manage_members,
        m.create_projects,m.run_jobs,m.manage_nodes,m.created_at
        FROM network_member m JOIN account a ON a.id=m.account_id
        WHERE m.network_id=? AND m.account_id=?`, networkID, accountID).Scan(
		&member.NetworkID, &member.Account.ID, &member.Account.Username,
		&member.Account.DisplayName, &member.Account.PublicKey, &member.Account.Created,
		&member.Role, &member.Permissions.ManageNetwork, &member.Permissions.ManageMembers,
		&member.Permissions.CreateProjects, &member.Permissions.RunJobs,
		&member.Permissions.ManageNodes, &member.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return member, ErrNotFound
	}
	return member, err
}

func (s *Store) Networks(accountID string) ([]Network, error) {
	rows, err := s.db.Query(`SELECT n.id, n.name, n.owner_account_id, m.role,
        n.created_at, n.updated_at FROM network n
        JOIN network_member m ON m.network_id=n.id
        WHERE m.account_id=? ORDER BY lower(n.name)`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Network{}
	for rows.Next() {
		var network Network
		if err := rows.Scan(&network.ID, &network.Name, &network.OwnerAccountID,
			&network.Role, &network.Created, &network.Updated); err != nil {
			return nil, err
		}
		out = append(out, network)
	}
	return out, rows.Err()
}

func (s *Store) NetworkByID(id string) (Network, error) {
	var network Network
	err := s.db.QueryRow(`SELECT id,name,owner_account_id,created_at,updated_at
        FROM network WHERE id=?`, id).Scan(&network.ID, &network.Name,
		&network.OwnerAccountID, &network.Created, &network.Updated)
	if errors.Is(err, sql.ErrNoRows) {
		return network, ErrNotFound
	}
	return network, err
}

func (s *Store) NetworkMembers(networkID string) ([]NetworkMemberRow, error) {
	rows, err := s.db.Query(`SELECT m.network_id,a.id,a.username,a.display_name,
        a.public_key,a.created_at,m.role,m.manage_network,m.manage_members,
        m.create_projects,m.run_jobs,m.manage_nodes,m.created_at
        FROM network_member m JOIN account a ON a.id=m.account_id
        WHERE m.network_id=? ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END,
        lower(a.username)`, networkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NetworkMemberRow{}
	for rows.Next() {
		var member NetworkMemberRow
		if err := rows.Scan(&member.NetworkID, &member.Account.ID,
			&member.Account.Username, &member.Account.DisplayName,
			&member.Account.PublicKey, &member.Account.Created, &member.Role,
			&member.Permissions.ManageNetwork, &member.Permissions.ManageMembers,
			&member.Permissions.CreateProjects, &member.Permissions.RunJobs,
			&member.Permissions.ManageNodes, &member.Created); err != nil {
			return nil, err
		}
		out = append(out, member)
	}
	return out, rows.Err()
}

type NetworkNode struct {
	Online      *bool          `json:"online,omitempty"`
	NetworkID   string         `json:"network_id"`
	NodeID      string         `json:"node_id"`
	Name        string         `json:"name"`
	Roles       []string       `json:"roles"`
	OS          string         `json:"os"`
	Arch        string         `json:"arch"`
	PublicKey   string         `json:"public_key"`
	Fingerprint string         `json:"fingerprint"`
	Address     string         `json:"address"`
	Policy      map[string]any `json:"policy"`
	Capacity    map[string]any `json:"capacity"`
	// Projects names what this machine has on disk for the network. Being
	// online says a machine can be reached; this says it can actually run
	// something, because a job needs the files and the data beside them.
	Projects []string `json:"projects"`
	IsSelf   bool     `json:"is_self"`
	LastSeen string   `json:"last_seen"`
	Created  string   `json:"created_at"`
}

// NetworkController is an optional always-reachable collaboration endpoint.
// It is not a device and never appears in job placement.
type NetworkController struct {
	NetworkID   string `json:"network_id"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	PublicKey   string `json:"public_key,omitempty"`
	Fingerprint string `json:"fingerprint"`
	Address     string `json:"address"`
	CollabToken string `json:"collab_token,omitempty"`
	LastSeen    string `json:"last_seen"`
	Created     string `json:"created_at"`
}

func (s *Store) UpsertNetworkController(item NetworkController) error {
	_, err := s.db.Exec(`INSERT INTO network_controller
        (network_id,id,name,public_key,fingerprint,address,collab_token,last_seen,created_at)
        VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(network_id,id) DO UPDATE SET
        name=excluded.name,public_key=excluded.public_key,fingerprint=excluded.fingerprint,
        address=excluded.address,collab_token=excluded.collab_token,last_seen=excluded.last_seen`,
		item.NetworkID, item.ID, item.Name, item.PublicKey, item.Fingerprint,
		item.Address, item.CollabToken, item.LastSeen, Now())
	return err
}

// SetNetworkController replaces the controller discovered from the global
// registry for one network. Passing nil clears a stale/offline assignment.
func (s *Store) SetNetworkController(networkID string, item *NetworkController) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM network_controller WHERE network_id=?`, networkID); err != nil {
		return err
	}
	if item != nil {
		if _, err := tx.Exec(`INSERT INTO network_controller
			(network_id,id,name,public_key,fingerprint,address,collab_token,last_seen,created_at)
			VALUES (?,?,?,?,?,?,?,?,?)`, networkID, item.ID, item.Name, "", "",
			item.Address, item.CollabToken, item.LastSeen, Now()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) NetworkControllers(networkID string) ([]NetworkController, error) {
	rows, err := s.db.Query(`SELECT network_id,id,name,public_key,fingerprint,address,
        collab_token,last_seen,created_at FROM network_controller WHERE network_id=? ORDER BY lower(name)`, networkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NetworkController{}
	for rows.Next() {
		var item NetworkController
		if err := rows.Scan(&item.NetworkID, &item.ID, &item.Name, &item.PublicKey,
			&item.Fingerprint, &item.Address, &item.CollabToken, &item.LastSeen, &item.Created); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) NetworkController(networkID, id string) (NetworkController, error) {
	var item NetworkController
	err := s.db.QueryRow(`SELECT network_id,id,name,public_key,fingerprint,address,
        collab_token,last_seen,created_at FROM network_controller WHERE network_id=? AND id=?`,
		networkID, id).Scan(&item.NetworkID, &item.ID, &item.Name, &item.PublicKey,
		&item.Fingerprint, &item.Address, &item.CollabToken, &item.LastSeen, &item.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNotFound
	}
	return item, err
}

func (s *Store) UpsertNetworkNode(node NetworkNode) error {
	policy, err := json.Marshal(node.Policy)
	if err != nil {
		return err
	}
	capacity, err := json.Marshal(node.Capacity)
	if err != nil {
		return err
	}
	if node.Projects == nil {
		node.Projects = []string{}
	}
	projects, err := json.Marshal(node.Projects)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO network_node
		(network_id,node_id,name,roles,os,arch,public_key,fingerprint,address,policy,capacity,projects,is_self,last_seen,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
        ON CONFLICT(network_id,node_id) DO UPDATE SET name=excluded.name,
		  roles=excluded.roles, os=excluded.os, arch=excluded.arch,
		  public_key=excluded.public_key, fingerprint=excluded.fingerprint, address=excluded.address,
		  policy=excluded.policy, capacity=excluded.capacity, projects=excluded.projects,
		  is_self=excluded.is_self, last_seen=excluded.last_seen`,
		node.NetworkID, node.NodeID, node.Name, strings.Join(node.Roles, ","),
		node.OS, node.Arch, node.PublicKey, node.Fingerprint, node.Address,
		string(policy), string(capacity), string(projects),
		node.IsSelf, node.LastSeen, Now())
	return err
}

// UpsertNetworkBootstrap records the first reachable edge supplied by the
// account directory without overwriting richer policy/capacity learned from
// the peer itself.
func (s *Store) UpsertNetworkBootstrap(node NetworkNode) error {
	_, err := s.db.Exec(`INSERT INTO network_node
		(network_id,node_id,name,roles,os,arch,public_key,fingerprint,address,policy,capacity,projects,is_self,last_seen,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,'{}','{}','[]',0,?,?)
		ON CONFLICT(network_id,node_id) DO UPDATE SET
		name=excluded.name,os=excluded.os,arch=excluded.arch,
		public_key=excluded.public_key,fingerprint=excluded.fingerprint,
		address=excluded.address,last_seen=excluded.last_seen
		WHERE excluded.last_seen > network_node.last_seen`,
		node.NetworkID, node.NodeID, node.Name, strings.Join(node.Roles, ","),
		node.OS, node.Arch, node.PublicKey, node.Fingerprint, node.Address,
		node.LastSeen, Now())
	return err
}

func (s *Store) NetworkNodes(networkID string) ([]NetworkNode, error) {
	rows, err := s.db.Query(`SELECT network_id,node_id,name,roles,os,arch,public_key,fingerprint,address,
        policy,capacity,projects,is_self,last_seen,created_at FROM network_node
        WHERE network_id=? ORDER BY is_self DESC, lower(name)`, networkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NetworkNode{}
	for rows.Next() {
		var node NetworkNode
		var roles, policy, capacity, projects string
		if err := rows.Scan(&node.NetworkID, &node.NodeID, &node.Name, &roles,
			&node.OS, &node.Arch, &node.PublicKey, &node.Fingerprint, &node.Address, &policy, &capacity,
			&projects, &node.IsSelf, &node.LastSeen, &node.Created); err != nil {
			return nil, err
		}
		node.Roles = normaliseStoredRoles(roles)
		node.Policy = map[string]any{}
		_ = json.Unmarshal([]byte(policy), &node.Policy)
		node.Capacity = map[string]any{}
		_ = json.Unmarshal([]byte(capacity), &node.Capacity)
		node.Projects = []string{}
		_ = json.Unmarshal([]byte(projects), &node.Projects)
		out = append(out, node)
	}
	return out, rows.Err()
}

// normaliseStoredRoles keeps retired machine roles from escaping the storage
// boundary. The rows themselves can have been written by an older binary, but
// every current caller should see an ordinary device.
func normaliseStoredRoles(raw string) []string {
	stored := []config.Role{}
	for _, role := range strings.Split(raw, ",") {
		if role = strings.TrimSpace(role); role != "" {
			stored = append(stored, config.Role(role))
		}
	}
	normalised := config.Normalise(stored)
	out := make([]string, 0, len(normalised))
	for _, role := range normalised {
		out = append(out, string(role))
	}
	return out
}

func (s *Store) NetworkNode(networkID, nodeID string) (NetworkNode, error) {
	nodes, err := s.NetworkNodes(networkID)
	if err != nil {
		return NetworkNode{}, err
	}
	for _, node := range nodes {
		if node.NodeID == nodeID {
			return node, nil
		}
	}
	return NetworkNode{}, ErrNotFound
}

// TouchNetworkNode records a fresh self check-in without rewriting the
// machine's identity or policy from a potentially stale snapshot.
func (s *Store) TouchNetworkNode(networkID, nodeID, seen string) error {
	result, err := s.db.Exec(`UPDATE network_node SET last_seen=?
        WHERE network_id=? AND node_id=?`, seen, networkID, nodeID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrNotFound
	}
	return nil
}

type Invitation struct {
	ID        string `json:"id"`
	NetworkID string `json:"network_id"`
	TokenHash string `json:"-"`
	Role      string `json:"role"`
	Expires   string `json:"expires_at"`
	MaxUses   int    `json:"max_uses"`
	Uses      int    `json:"uses"`
	CreatedBy string `json:"created_by"`
	Created   string `json:"created_at"`
}

func (s *Store) CreateInvitation(invite Invitation) error {
	if !ValidNetworkRole(invite.Role) || invite.ID == "" || invite.TokenHash == "" {
		return errors.New("invalid invitation")
	}
	if invite.MaxUses < 1 {
		invite.MaxUses = 1
	}
	_, err := s.db.Exec(`INSERT INTO invitation
        (id,network_id,token_hash,role,expires_at,max_uses,uses,created_by,created_at)
        VALUES (?,?,?,?,?,?,?,?,?)`, invite.ID, invite.NetworkID, invite.TokenHash,
		invite.Role, invite.Expires, invite.MaxUses, 0, invite.CreatedBy, Now())
	return err
}

// HasPendingInvitation reports whether a join code is outstanding for a
// network. A machine is expected, so the peer port has to be open for it.
func (s *Store) HasPendingInvitation(networkID string) (bool, error) {
	var n int
	err := s.db.QueryRow(`
        SELECT count(*) FROM invitation
        WHERE network_id = ? AND uses < max_uses AND expires_at > ?`,
		networkID, Now()).Scan(&n)
	return n > 0, err
}

// ConsumeInvitation atomically spends one use of a join token.
func (s *Store) ConsumeInvitation(networkID, tokenHash string) (Invitation, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Invitation{}, err
	}
	defer tx.Rollback()
	var invite Invitation
	err = tx.QueryRow(`SELECT id,network_id,token_hash,role,expires_at,max_uses,
        uses,created_by,created_at FROM invitation
        WHERE network_id=? AND token_hash=?`, networkID, tokenHash).Scan(
		&invite.ID, &invite.NetworkID, &invite.TokenHash, &invite.Role,
		&invite.Expires, &invite.MaxUses, &invite.Uses, &invite.CreatedBy, &invite.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return invite, ErrNotFound
	}
	if err != nil {
		return invite, err
	}
	expires, err := time.Parse(time.RFC3339, invite.Expires)
	if err != nil || time.Now().After(expires) || invite.Uses >= invite.MaxUses {
		return invite, errors.New("invitation expired or already used")
	}
	result, err := tx.Exec(`UPDATE invitation SET uses=uses+1
        WHERE id=? AND uses=? AND uses<max_uses`, invite.ID, invite.Uses)
	if err != nil {
		return invite, err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return invite, errors.New("invitation was used at the same time")
	}
	invite.Uses++
	return invite, tx.Commit()
}
