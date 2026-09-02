package store

import (
	"database/sql"
	"errors"
)

type Dataset struct {
	ID        string `json:"id"`
	NetworkID string `json:"network_id"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	RootHash  string `json:"root_hash"`
	FileCount int    `json:"file_count"`
	SizeBytes int64  `json:"size_bytes"`
	Manifest  string `json:"-"`
	Created   string `json:"created_at"`
}

type DatasetPlacement struct {
	DatasetID string `json:"dataset_id"`
	NodeID    string `json:"node_id"`
	State     string `json:"state"`
	BytesDone int64  `json:"bytes_done"`
	Updated   string `json:"updated_at"`
}

func (s *Store) CreateDataset(dataset *Dataset) error {
	dataset.Created = Now()
	_, err := s.db.Exec(`INSERT INTO dataset
        (id,network_id,name,version,root_hash,file_count,size_bytes,manifest,created_at)
        VALUES (?,?,?,?,?,?,?,?,?)`, dataset.ID, dataset.NetworkID, dataset.Name, dataset.Version,
		dataset.RootHash, dataset.FileCount, dataset.SizeBytes, dataset.Manifest, dataset.Created)
	return err
}

func (s *Store) UpsertDataset(dataset Dataset) error {
	if dataset.Created == "" {
		dataset.Created = Now()
	}
	_, err := s.db.Exec(`INSERT INTO dataset
        (id,network_id,name,version,root_hash,file_count,size_bytes,manifest,created_at)
        VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET manifest=excluded.manifest`,
		dataset.ID, dataset.NetworkID, dataset.Name, dataset.Version, dataset.RootHash,
		dataset.FileCount, dataset.SizeBytes, dataset.Manifest, dataset.Created)
	return err
}

func scanDataset(row interface{ Scan(...any) error }) (Dataset, error) {
	var dataset Dataset
	err := row.Scan(&dataset.ID, &dataset.NetworkID, &dataset.Name, &dataset.Version,
		&dataset.RootHash, &dataset.FileCount, &dataset.SizeBytes, &dataset.Manifest, &dataset.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return dataset, ErrNotFound
	}
	return dataset, err
}

func (s *Store) Dataset(id string) (Dataset, error) {
	return scanDataset(s.db.QueryRow(`SELECT id,network_id,name,version,root_hash,file_count,size_bytes,manifest,created_at FROM dataset WHERE id=?`, id))
}

func (s *Store) Datasets(networkID string) ([]Dataset, error) {
	rows, err := s.db.Query(`SELECT id,network_id,name,version,root_hash,file_count,size_bytes,manifest,created_at FROM dataset WHERE network_id=? ORDER BY created_at DESC`, networkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Dataset{}
	for rows.Next() {
		dataset, err := scanDataset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, dataset)
	}
	return out, rows.Err()
}

func (s *Store) SetDatasetPlacement(placement DatasetPlacement) error {
	_, err := s.db.Exec(`INSERT INTO dataset_placement(dataset_id,node_id,state,bytes_done,updated_at)
        VALUES(?,?,?,?,?) ON CONFLICT(dataset_id,node_id) DO UPDATE SET state=excluded.state,
        bytes_done=excluded.bytes_done,updated_at=excluded.updated_at`, placement.DatasetID,
		placement.NodeID, placement.State, placement.BytesDone, Now())
	return err
}

func (s *Store) DatasetPlacements(datasetID string) ([]DatasetPlacement, error) {
	rows, err := s.db.Query(`SELECT dataset_id,node_id,state,bytes_done,updated_at FROM dataset_placement WHERE dataset_id=?`, datasetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DatasetPlacement{}
	for rows.Next() {
		var p DatasetPlacement
		if err := rows.Scan(&p.DatasetID, &p.NodeID, &p.State, &p.BytesDone, &p.Updated); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
