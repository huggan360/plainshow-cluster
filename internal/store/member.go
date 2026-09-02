package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Capabilities are what one person may do on one project.
//
// The shape mirrors the Plainshow console's — view, code, push, manage are the
// same words meaning the same things — with the two hosting capabilities
// replaced by the two that matter here. A cluster does not deploy or operate
// web processes; it runs jobs and trains models.
const (
	CapView   = "view"   // open the project, read files, logs and job history
	CapCode   = "code"   // change files in the editor
	CapPush   = "push"   // fetch, pull, commit and push the repository
	CapRun    = "run"    // start jobs and notebook kernels
	CapTrain  = "train"  // start multi-worker distributed training runs
	CapManage = "manage" // add people and change what they may do
)

// Capabilities lists every capability in presentation order.
var Capabilities = []string{CapView, CapCode, CapPush, CapRun, CapTrain, CapManage}

// ValidCapability reports whether name is one this build understands.
func ValidCapability(name string) bool {
	for _, c := range Capabilities {
		if c == name {
			return true
		}
	}
	return false
}

// Member is one person's access to one project.
type Member struct {
	ProjectID    string          `json:"project_id"`
	Username     string          `json:"username"`
	GitHubLogin  string          `json:"github_login"`
	Capabilities map[string]bool `json:"capabilities"`
	// GitHubRole is what GitHub last reported for this person. It is a memo,
	// not a setting: it lets a later sync tell "unchanged on GitHub" apart from
	// "somebody changed it on GitHub", so a local change that GitHub's coarser
	// roles cannot express is not quietly undone on the next sync.
	GitHubRole string `json:"github_role"`
	Owner      bool   `json:"owner"`
	Created    string `json:"created_at"`
}

// Can reports whether the member holds a capability. An owner holds all of them.
func (m Member) Can(capability string) bool {
	if m.Owner {
		return true
	}
	return m.Capabilities[capability]
}

// AccessLabel names a member's access in one word, for lists and headings.
func (m Member) AccessLabel() string {
	switch {
	case m.Owner:
		return "owner"
	case m.Can(CapManage):
		return "admin"
	case m.Can(CapTrain):
		return "maintainer"
	case m.Can(CapPush):
		return "contributor"
	case m.Can(CapCode):
		return "editor"
	default:
		return "viewer"
	}
}

// CapabilitySet builds a full set from the capabilities that should be on.
func CapabilitySet(on ...string) map[string]bool {
	set := map[string]bool{}
	for _, c := range Capabilities {
		set[c] = false
	}
	for _, c := range on {
		if ValidCapability(c) {
			set[c] = true
		}
	}
	return set
}

// OwnerCapabilities is every capability, held by whoever created the project.
func OwnerCapabilities() map[string]bool { return CapabilitySet(Capabilities...) }

// DefaultCapabilities is what a new collaborator gets: everything except the
// ability to change who else has access.
func DefaultCapabilities() map[string]bool {
	return CapabilitySet(CapView, CapCode, CapPush, CapRun, CapTrain)
}

// UpsertMember creates or updates one person's access to a project.
func (s *Store) UpsertMember(m Member) error {
	if m.Capabilities == nil {
		m.Capabilities = CapabilitySet()
	}
	b := func(name string) int {
		if m.Capabilities[name] {
			return 1
		}
		return 0
	}
	owner := 0
	if m.Owner {
		owner = 1
	}
	_, err := s.db.Exec(`
        INSERT INTO member (project_id, username, github_login, view, code, push,
                            run, train, manage, github_role, owner, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(project_id, username) DO UPDATE SET
            github_login=excluded.github_login, view=excluded.view,
            code=excluded.code, push=excluded.push, run=excluded.run,
            train=excluded.train, manage=excluded.manage,
            github_role=excluded.github_role, owner=excluded.owner`,
		m.ProjectID, m.Username, m.GitHubLogin,
		b(CapView), b(CapCode), b(CapPush), b(CapRun), b(CapTrain), b(CapManage),
		m.GitHubRole, owner, Now())
	return err
}

const memberSelect = `
    SELECT project_id, username, github_login, view, code, push, run, train,
           manage, github_role, owner, created_at
    FROM member`

func scanMembers(rows *sql.Rows) ([]Member, error) {
	out := []Member{}
	for rows.Next() {
		var m Member
		var view, code, push, run, train, manage, owner int
		if err := rows.Scan(&m.ProjectID, &m.Username, &m.GitHubLogin,
			&view, &code, &push, &run, &train, &manage,
			&m.GitHubRole, &owner, &m.Created); err != nil {
			return nil, err
		}
		m.Capabilities = map[string]bool{
			CapView: view == 1, CapCode: code == 1, CapPush: push == 1,
			CapRun: run == 1, CapTrain: train == 1, CapManage: manage == 1,
		}
		m.Owner = owner == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

// Members lists everyone with access to a project, the owner first.
func (s *Store) Members(projectID string) ([]Member, error) {
	rows, err := s.db.Query(memberSelect+`
        WHERE project_id = ? ORDER BY owner DESC, username ASC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMembers(rows)
}

// Member fetches one person's access.
func (s *Store) Member(projectID, username string) (Member, error) {
	rows, err := s.db.Query(memberSelect+` WHERE project_id = ? AND username = ?`,
		projectID, username)
	if err != nil {
		return Member{}, err
	}
	defer rows.Close()
	found, err := scanMembers(rows)
	if err != nil {
		return Member{}, err
	}
	if len(found) == 0 {
		return Member{}, ErrNotFound
	}
	return found[0], nil
}

// RemoveMember withdraws someone's access. The owner cannot be removed: a
// project with nobody who may manage it can never be repaired.
func (s *Store) RemoveMember(projectID, username string) error {
	existing, err := s.Member(projectID, username)
	if err != nil {
		return err
	}
	if existing.Owner {
		return errors.New("the project owner cannot be removed")
	}
	_, err = s.db.Exec(`DELETE FROM member WHERE project_id = ? AND username = ?`,
		projectID, username)
	return err
}

// MemberByGitHubLogin finds a project member by their GitHub account.
func (s *Store) MemberByGitHubLogin(projectID, login string) (Member, error) {
	rows, err := s.db.Query(memberSelect+`
        WHERE project_id = ? AND lower(github_login) = lower(?)`, projectID, login)
	if err != nil {
		return Member{}, err
	}
	defer rows.Close()
	found, err := scanMembers(rows)
	if err != nil {
		return Member{}, err
	}
	if len(found) == 0 {
		return Member{}, ErrNotFound
	}
	return found[0], nil
}

// CapabilitiesFromNames validates and builds a capability set.
func CapabilitiesFromNames(names []string) (map[string]bool, error) {
	unknown := []string{}
	for _, n := range names {
		if !ValidCapability(n) {
			unknown = append(unknown, n)
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("unknown capabilities: %s (valid: %s)",
			strings.Join(unknown, ", "), strings.Join(Capabilities, ", "))
	}
	return CapabilitySet(names...), nil
}
