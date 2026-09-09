package accountserver

// Projects the account knows about.
//
// Metadata only — a name, a repository, a branch, a size. Never a file, never a
// commit, never a byte of anybody's data: those go directly between machines,
// and that separation is the whole reason this service is safe to host.
//
// It exists so a second machine can show you a project you made somewhere else
// and offer to fetch it, instead of showing an empty workspace with no
// explanation of where your work went.

import (
	"database/sql"
	"errors"
	"strings"
)

// AccountProject is one project as the account knows it.
type AccountProject struct {
	ID          string `json:"id"`
	Owner       string `json:"owner_account_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Repository  string `json:"repository"`
	Branch      string `json:"branch"`
	SizeKB      int    `json:"size_kb"`
	Role        string `json:"role"`
	Created     string `json:"created_at"`
	Updated     string `json:"updated_at"`
}

// ProjectRegistration is what a node reports about a project it holds.
type ProjectRegistration struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Repository  string `json:"repository"`
	Branch      string `json:"branch"`
	SizeKB      int    `json:"size_kb"`
	// Members are the Plainshow accounts this project's own membership list
	// names, so being added on one machine reaches the person's other machines.
	Members []string `json:"members"`
}

// RegisterProject records or updates a project the account owns.
//
// The first account to report an id owns it. A later report from somebody else
// is ignored rather than rejected: two people can legitimately hold the same
// project, and the one who made it is the one whose row this is.
func (s *Store) RegisterProject(accountID string, input ProjectRegistration) (AccountProject, error) {
	input.ID, input.Name = strings.TrimSpace(input.ID), strings.TrimSpace(input.Name)
	if input.ID == "" || input.Name == "" {
		return AccountProject{}, errors.New("a project needs an id and a name")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return AccountProject{}, err
	}
	defer tx.Rollback()

	var owner string
	err = tx.QueryRow(`SELECT owner_account_id FROM project WHERE id=?`, input.ID).Scan(&owner)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		owner = accountID
		if _, err = tx.Exec(`INSERT INTO project
            (id,owner_account_id,name,description,repository,branch,size_kb,created_at,updated_at)
            VALUES(?,?,?,?,?,?,?,?,?)`, input.ID, accountID, input.Name, input.Description,
			input.Repository, input.Branch, input.SizeKB, now(), now()); err != nil {
			return AccountProject{}, err
		}
		if _, err = tx.Exec(`INSERT INTO project_member(project_id,account_id,role,joined_at)
            VALUES(?,?,'owner',?) ON CONFLICT DO NOTHING`, input.ID, accountID, now()); err != nil {
			return AccountProject{}, err
		}
	case err != nil:
		return AccountProject{}, err
	case owner == accountID:
		if _, err = tx.Exec(`UPDATE project SET name=?,description=?,repository=?,
            branch=?,size_kb=?,updated_at=? WHERE id=?`, input.Name, input.Description,
			input.Repository, input.Branch, input.SizeKB, now(), input.ID); err != nil {
			return AccountProject{}, err
		}
	}

	// Membership follows the owner's list. Somebody added on one machine
	// becomes able to see the project on all of theirs.
	if owner == accountID {
		for _, member := range input.Members {
			var id string
			if err := tx.QueryRow(`SELECT id FROM account WHERE username=? COLLATE NOCASE`,
				strings.TrimSpace(member)).Scan(&id); err != nil {
				continue
			}
			if _, err := tx.Exec(`INSERT INTO project_member(project_id,account_id,role,joined_at)
                VALUES(?,?,'member',?) ON CONFLICT DO NOTHING`, input.ID, id, now()); err != nil {
				return AccountProject{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return AccountProject{}, err
	}
	return s.projectFor(accountID, input.ID)
}

// ProjectsForAccount lists every project this account owns or belongs to.
func (s *Store) ProjectsForAccount(accountID string) ([]AccountProject, error) {
	rows, err := s.db.Query(`SELECT p.id,p.owner_account_id,p.name,p.description,
            p.repository,p.branch,p.size_kb,m.role,p.created_at,p.updated_at
        FROM project p JOIN project_member m ON m.project_id=p.id
        WHERE m.account_id=? ORDER BY p.updated_at DESC`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccountProject{}
	for rows.Next() {
		var item AccountProject
		if err := rows.Scan(&item.ID, &item.Owner, &item.Name, &item.Description,
			&item.Repository, &item.Branch, &item.SizeKB, &item.Role,
			&item.Created, &item.Updated); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) projectFor(accountID, id string) (AccountProject, error) {
	var item AccountProject
	err := s.db.QueryRow(`SELECT p.id,p.owner_account_id,p.name,p.description,
            p.repository,p.branch,p.size_kb,coalesce(m.role,''),p.created_at,p.updated_at
        FROM project p LEFT JOIN project_member m
            ON m.project_id=p.id AND m.account_id=?
        WHERE p.id=?`, accountID, id).Scan(&item.ID, &item.Owner, &item.Name,
		&item.Description, &item.Repository, &item.Branch, &item.SizeKB, &item.Role,
		&item.Created, &item.Updated)
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNotFound
	}
	return item, err
}

// ForgetProject removes an owner's project, or removes only the caller's
// membership when they do not own it. DELETE is idempotent so a node can finish
// removing local files after a retry without central metadata reappearing.
func (s *Store) ForgetProject(accountID, id string) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var owner string
	err = tx.QueryRow(`SELECT owner_account_id FROM project WHERE id=?`, id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return []string{accountID}, tx.Commit()
	}
	if err != nil {
		return nil, err
	}

	affected := []string{accountID}
	if owner == accountID {
		rows, queryErr := tx.Query(`SELECT account_id FROM project_member WHERE project_id=?`, id)
		if queryErr != nil {
			return nil, queryErr
		}
		affected = affected[:0]
		for rows.Next() {
			var member string
			if scanErr := rows.Scan(&member); scanErr != nil {
				rows.Close()
				return nil, scanErr
			}
			affected = append(affected, member)
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			rows.Close()
			return nil, rowsErr
		}
		if rowsErr := rows.Close(); rowsErr != nil {
			return nil, rowsErr
		}
		if len(affected) == 0 {
			affected = append(affected, accountID)
		}
		if _, err = tx.Exec(`DELETE FROM project WHERE id=?`, id); err != nil {
			return nil, err
		}
	} else if _, err = tx.Exec(`DELETE FROM project_member WHERE project_id=? AND account_id=?`, id, accountID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return affected, nil
}
