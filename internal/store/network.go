package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
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
	IsSelf      bool           `json:"is_self"`
	LastSeen    string         `json:"last_seen"`
	Created     string         `json:"created_at"`
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
	_, err = s.db.Exec(`INSERT INTO network_node
		(network_id,node_id,name,roles,os,arch,public_key,fingerprint,address,policy,capacity,is_self,last_seen,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
        ON CONFLICT(network_id,node_id) DO UPDATE SET name=excluded.name,
		  roles=excluded.roles, os=excluded.os, arch=excluded.arch,
		  public_key=excluded.public_key, fingerprint=excluded.fingerprint, address=excluded.address,
		  policy=excluded.policy, capacity=excluded.capacity,
		  is_self=excluded.is_self, last_seen=excluded.last_seen`,
		node.NetworkID, node.NodeID, node.Name, strings.Join(node.Roles, ","),
		node.OS, node.Arch, node.PublicKey, node.Fingerprint, node.Address, string(policy), string(capacity),
		node.IsSelf, node.LastSeen, Now())
	return err
}

func (s *Store) NetworkNodes(networkID string) ([]NetworkNode, error) {
	rows, err := s.db.Query(`SELECT network_id,node_id,name,roles,os,arch,public_key,fingerprint,address,
        policy,capacity,is_self,last_seen,created_at FROM network_node
        WHERE network_id=? ORDER BY is_self DESC, lower(name)`, networkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NetworkNode{}
	for rows.Next() {
		var node NetworkNode
		var roles, policy, capacity string
		if err := rows.Scan(&node.NetworkID, &node.NodeID, &node.Name, &roles,
			&node.OS, &node.Arch, &node.PublicKey, &node.Fingerprint, &node.Address, &policy, &capacity, &node.IsSelf,
			&node.LastSeen, &node.Created); err != nil {
			return nil, err
		}
		if roles != "" {
			node.Roles = strings.Split(roles, ",")
		} else {
			node.Roles = []string{}
		}
		node.Policy = map[string]any{}
		_ = json.Unmarshal([]byte(policy), &node.Policy)
		node.Capacity = map[string]any{}
		_ = json.Unmarshal([]byte(capacity), &node.Capacity)
		out = append(out, node)
	}
	return out, rows.Err()
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

func (s *Store) AssignProjectsToNetwork(networkID string) error {
	_, err := s.db.Exec(`UPDATE project SET network_id=? WHERE network_id=''`, networkID)
	return err
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
