package store

import (
	"database/sql"
	"errors"
)

type CollabDocument struct {
	NetworkID string `json:"network_id"`
	ProjectID string `json:"project_id"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	Revision  int64  `json:"revision"`
	History   string `json:"-"`
	Updated   string `json:"updated_at"`
}

func (s *Store) CollabDocument(networkID, projectID, path string) (CollabDocument, error) {
	var doc CollabDocument
	err := s.db.QueryRow(`SELECT network_id,project_id,path,content,revision,history,updated_at
        FROM collab_document WHERE network_id=? AND project_id=? AND path=?`,
		networkID, projectID, path).Scan(&doc.NetworkID, &doc.ProjectID, &doc.Path,
		&doc.Content, &doc.Revision, &doc.History, &doc.Updated)
	if errors.Is(err, sql.ErrNoRows) {
		return doc, ErrNotFound
	}
	return doc, err
}

func (s *Store) SaveCollabDocument(doc CollabDocument) error {
	_, err := s.db.Exec(`INSERT INTO collab_document
        (network_id,project_id,path,content,revision,history,updated_at) VALUES (?,?,?,?,?,?,?)
        ON CONFLICT(network_id,project_id,path) DO UPDATE SET content=excluded.content,
          revision=excluded.revision,history=excluded.history,updated_at=excluded.updated_at`,
		doc.NetworkID, doc.ProjectID, doc.Path, doc.Content, doc.Revision, doc.History, Now())
	return err
}
