package accountserver

// Invitations name a person, not a machine.
//
// A join code proves you were handed a secret; it says nothing about who you
// are, and it has to be carried out of band to somebody who is standing at the
// right computer. An invitation is addressed to an account, waits until that
// person looks, and is accepted from whichever machine they happen to be on.
// The membership row it creates is the same one a code would have produced, so
// everything downstream — adoption, roles, sync — is unchanged.

import (
	"database/sql"
	"errors"
	"strings"
)

var (
	// ErrInviteNotAllowed means the inviting account cannot admit people here.
	ErrInviteNotAllowed = errors.New("account cannot invite people to this network")
	// ErrAlreadyMember means the invited account is already in the network.
	ErrAlreadyMember = errors.New("that account is already a member")
	// ErrInviteSettled means the invitation has already been answered.
	ErrInviteSettled = errors.New("that invitation has already been answered")
)

// Invitation is one pending or settled offer of network membership.
type Invitation struct {
	ID            string `json:"id"`
	NetworkID     string `json:"network_id"`
	NetworkName   string `json:"network_name"`
	AccountID     string `json:"account_id"`
	Username      string `json:"username"`
	DisplayName   string `json:"display_name"`
	InvitedBy     string `json:"invited_by"`
	InvitedByName string `json:"invited_by_name"`
	Role          string `json:"role"`
	Status        string `json:"status"`
	Created       string `json:"created_at"`
	Responded     string `json:"responded_at"`
}

// canInvite reports whether an account may admit people to a network.
func (s *Store) canInvite(networkID, accountID string) bool {
	role, err := s.networkRole(networkID, accountID)
	return err == nil && (role == "owner" || role == "admin")
}

// SearchAccounts finds people to invite.
//
// It answers only for a signed-in caller and returns nothing for an empty
// query, so it is a way to complete a name somebody already knows rather than a
// way to page through the whole directory.
func (s *Store) SearchAccounts(query string, limit int) ([]Account, error) {
	query = strings.TrimSpace(query)
	if len(query) < 2 {
		return []Account{}, nil
	}
	if limit <= 0 || limit > 25 {
		limit = 10
	}
	pattern := "%" + strings.ToLower(query) + "%"
	rows, err := s.db.Query(`SELECT id,username,display_name,github_login FROM account
        WHERE disabled=0 AND (lower(username) LIKE ? OR lower(display_name) LIKE ?)
        ORDER BY CASE WHEN lower(username)=? THEN 0 ELSE 1 END, lower(username)
        LIMIT ?`, pattern, pattern, strings.ToLower(query), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		var account Account
		if err := rows.Scan(&account.ID, &account.Username, &account.DisplayName,
			&account.GitHubLogin); err != nil {
			return nil, err
		}
		out = append(out, account)
	}
	return out, rows.Err()
}

// SetGitHubLogin records what an account is called on GitHub.
//
// A node publishes this when its owner connects GitHub, because that is the
// only place the two names are known together. It is what lets somebody be
// invited to a project by the name their collaborators know them by and still
// end up a collaborator on the repository.
func (s *Store) SetGitHubLogin(accountID, login string) error {
	_, err := s.db.Exec(`UPDATE account SET github_login=? WHERE id=?`,
		strings.TrimSpace(login), accountID)
	return err
}

// CreateInvitation offers membership to one account.
func (s *Store) CreateInvitation(actorID, networkID, username, role string) (Invitation, error) {
	if !validNetworkRole(role) || role == "owner" {
		return Invitation{}, errors.New("a network invitation can offer admin, operator, member or viewer")
	}
	if !s.canInvite(networkID, actorID) {
		return Invitation{}, ErrInviteNotAllowed
	}
	invitee, err := s.AccountByUsername(strings.TrimSpace(username))
	if err != nil {
		return Invitation{}, err
	}
	if invitee.Disabled {
		return Invitation{}, ErrNotFound
	}
	if _, err := s.networkRole(networkID, invitee.ID); err == nil {
		return Invitation{}, ErrAlreadyMember
	}

	// Re-inviting somebody who declined is a normal thing to do, so an existing
	// row is replaced rather than treated as a conflict.
	created := now()
	id, err := randomSecret()
	if err != nil {
		return Invitation{}, err
	}
	if _, err := s.db.Exec(`INSERT INTO network_invitation
            (id,network_id,account_id,invited_by,role,status,created_at,responded_at)
        VALUES(?,?,?,?,?,'pending',?,'')
        ON CONFLICT(network_id,account_id) DO UPDATE SET
            id=excluded.id, invited_by=excluded.invited_by, role=excluded.role,
            status='pending', created_at=excluded.created_at, responded_at=''`,
		id, networkID, invitee.ID, actorID, role, created); err != nil {
		return Invitation{}, err
	}
	return s.invitation(networkID, invitee.ID)
}

// InvitationsForAccount lists what is waiting for a person to answer.
func (s *Store) InvitationsForAccount(accountID string) ([]Invitation, error) {
	return s.invitationsWhere(`i.account_id=? AND i.status='pending'`, accountID)
}

// InvitationsForNetwork lists what a network has outstanding.
func (s *Store) InvitationsForNetwork(actorID, networkID string) ([]Invitation, error) {
	if _, err := s.networkRole(networkID, actorID); err != nil {
		return nil, ErrInviteNotAllowed
	}
	return s.invitationsWhere(`i.network_id=? AND i.status='pending'`, networkID)
}

// RespondToInvitation accepts or declines. Accepting is what creates the
// membership row, which is the same row a join code would have written.
func (s *Store) RespondToInvitation(accountID, invitationID string, accept bool) (Invitation, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Invitation{}, err
	}
	defer tx.Rollback()

	var networkID, role, status string
	err = tx.QueryRow(`SELECT network_id,role,status FROM network_invitation
        WHERE id=? AND account_id=?`, invitationID, accountID).Scan(&networkID, &role, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return Invitation{}, ErrNotFound
	}
	if err != nil {
		return Invitation{}, err
	}
	if status != "pending" {
		return Invitation{}, ErrInviteSettled
	}

	settled := "declined"
	if accept {
		settled = "accepted"
		if _, err := tx.Exec(`INSERT INTO network_member(network_id,account_id,role,joined_at)
            VALUES(?,?,?,?) ON CONFLICT(network_id,account_id) DO NOTHING`,
			networkID, accountID, role, now()); err != nil {
			return Invitation{}, err
		}
	}
	if _, err := tx.Exec(`UPDATE network_invitation SET status=?,responded_at=? WHERE id=?`,
		settled, now(), invitationID); err != nil {
		return Invitation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Invitation{}, err
	}
	return s.invitation(networkID, accountID)
}

// RevokeInvitation withdraws an offer that has not been answered. It returns
// the account the offer was addressed to, which is the one that has to be told
// the invitation is gone.
func (s *Store) RevokeInvitation(actorID, invitationID string) (string, error) {
	var networkID, invitee string
	err := s.db.QueryRow(`SELECT network_id,account_id FROM network_invitation WHERE id=?`,
		invitationID).Scan(&networkID, &invitee)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if !s.canInvite(networkID, actorID) {
		return "", ErrInviteNotAllowed
	}
	_, err = s.db.Exec(`DELETE FROM network_invitation WHERE id=?`, invitationID)
	return invitee, err
}

const invitationColumns = `i.id,i.network_id,n.name,i.account_id,a.username,a.display_name,
        i.invited_by,coalesce(b.display_name,''),i.role,i.status,i.created_at,i.responded_at
    FROM network_invitation i
    JOIN network n ON n.id=i.network_id
    JOIN account a ON a.id=i.account_id
    LEFT JOIN account b ON b.id=i.invited_by`

func (s *Store) invitationsWhere(condition string, argument string) ([]Invitation, error) {
	rows, err := s.db.Query(`SELECT `+invitationColumns+` WHERE `+condition+
		` ORDER BY i.created_at DESC`, argument)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invitation{}
	for rows.Next() {
		invitation, err := scanInvitation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, invitation)
	}
	return out, rows.Err()
}

func (s *Store) invitation(networkID, accountID string) (Invitation, error) {
	row := s.db.QueryRow(`SELECT `+invitationColumns+
		` WHERE i.network_id=? AND i.account_id=?`, networkID, accountID)
	invitation, err := scanInvitation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Invitation{}, ErrNotFound
	}
	return invitation, err
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

func scanInvitation(row scanner) (Invitation, error) {
	var item Invitation
	err := row.Scan(&item.ID, &item.NetworkID, &item.NetworkName, &item.AccountID,
		&item.Username, &item.DisplayName, &item.InvitedBy, &item.InvitedByName,
		&item.Role, &item.Status, &item.Created, &item.Responded)
	return item, err
}
