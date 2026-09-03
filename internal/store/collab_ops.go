package store

import (
	"database/sql"
)

// SaveCollabEdit records one edit: the operation itself and the document it
// produced, in a single transaction.
//
// Both writes have to happen or neither: a document saved without its operation
// cannot reconcile a reconnecting client, and an operation without its document
// describes an edit to content nobody has. Doing them separately also costs two
// fsyncs on a path that runs once per character typed, and the whole thing is
// serialised behind one mutex, so a slow write here stalls everyone editing.
func (s *Store) SaveCollabEdit(doc CollabDocument, revision int64, payload string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
        INSERT INTO collab_operation (network_id, project_id, path, revision, payload)
        VALUES (?, ?, ?, ?, ?)
        ON CONFLICT(network_id, project_id, path, revision) DO UPDATE SET
            payload = excluded.payload`,
		doc.NetworkID, doc.ProjectID, doc.Path, revision, payload); err != nil {
		return err
	}
	if _, err := tx.Exec(`
        INSERT INTO collab_document
            (network_id, project_id, path, content, revision, history, updated_at)
        VALUES (?, ?, ?, ?, ?, '', ?)
        ON CONFLICT(network_id, project_id, path) DO UPDATE SET
            content = excluded.content, revision = excluded.revision,
            history = '', updated_at = excluded.updated_at`,
		doc.NetworkID, doc.ProjectID, doc.Path, doc.Content, doc.Revision, Now()); err != nil {
		return err
	}
	return tx.Commit()
}

// AppendCollabOperation records one edit.
//
// This is the write on the keystroke path, so it stays a single small insert.
// Trimming happens separately and rarely; doing it here would put a delete scan
// in front of every character typed.
func (s *Store) AppendCollabOperation(networkID, projectID, path string,
	revision int64, payload string) error {
	_, err := s.db.Exec(`
        INSERT INTO collab_operation (network_id, project_id, path, revision, payload)
        VALUES (?, ?, ?, ?, ?)
        ON CONFLICT(network_id, project_id, path, revision) DO UPDATE SET
            payload = excluded.payload`,
		networkID, projectID, path, revision, payload)
	return err
}

// CollabOperations returns the retained edits for a document, oldest first.
func (s *Store) CollabOperations(networkID, projectID, path string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 4096
	}
	// Newest-first with a limit, then reversed: the alternative needs a count
	// first, which is a second query on the reconnect path.
	rows, err := s.db.Query(`
        SELECT payload FROM collab_operation
        WHERE network_id = ? AND project_id = ? AND path = ?
        ORDER BY revision DESC LIMIT ?`,
		networkID, projectID, path, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	reversed := []string{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		reversed = append(reversed, payload)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]string, len(reversed))
	for i, payload := range reversed {
		out[len(reversed)-1-i] = payload
	}
	return out, nil
}

// TrimCollabOperations drops edits older than the retention window.
func (s *Store) TrimCollabOperations(networkID, projectID, path string, keepFrom int64) error {
	_, err := s.db.Exec(`
        DELETE FROM collab_operation
        WHERE network_id = ? AND project_id = ? AND path = ? AND revision < ?`,
		networkID, projectID, path, keepFrom)
	return err
}

// DeleteCollabDocument forgets a document and its edits, used when a project
// or a file goes away.
func (s *Store) DeleteCollabDocument(networkID, projectID, path string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range []string{
		`DELETE FROM collab_operation WHERE network_id=? AND project_id=? AND path=?`,
		`DELETE FROM collab_document  WHERE network_id=? AND project_id=? AND path=?`,
	} {
		if _, err := tx.Exec(stmt, networkID, projectID, path); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteCollabTree forgets a file or every document beneath a directory. SQL's
// length function keeps wildcard characters in legitimate paths literal.
func (s *Store) DeleteCollabTree(networkID, projectID, path string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, table := range []string{"collab_operation", "collab_document"} {
		if _, err := tx.Exec(`DELETE FROM `+table+`
            WHERE network_id=? AND project_id=?
              AND (path=? OR substr(path,1,length(?)+1)=? || '/')`,
			networkID, projectID, path, path, path); err != nil {
			return err
		}
	}
	return tx.Commit()
}

var _ = sql.ErrNoRows
