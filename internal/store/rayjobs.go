package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/huggan360/plainshow-cluster/internal/ray"
)

// QueueRayJobs saves only changed observations and never regresses a terminal
// state. Scope includes the account server and account ID, so switching account
// cannot upload another account's pending summaries.
func (s *Store) QueueRayJobs(scope, networkID string, jobs []ray.Job) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, job := range jobs {
		payload, err := json.Marshal(job)
		if err != nil {
			return err
		}
		var previous string
		if job.StartedAt > 0 {
			if _, err := tx.Exec(`DELETE FROM ray_job_outbox WHERE scope=? AND network_id=? AND id=? AND started_at=0`, scope, networkID, job.ID); err != nil {
				return err
			}
		}
		err = tx.QueryRow(`SELECT payload FROM ray_job_outbox WHERE scope=? AND network_id=? AND id=? AND started_at=?`, scope, networkID, job.ID, job.StartedAt).Scan(&previous)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if previous == string(payload) {
			continue
		}
		if previous != "" {
			var old ray.Job
			if err := json.Unmarshal([]byte(previous), &old); err != nil {
				return err
			}
			if !old.Running() || old.Status == "RUNNING" && job.Status == "PENDING" {
				continue
			}
		}
		if _, err := tx.Exec(`INSERT INTO ray_job_outbox(scope,network_id,id,started_at,payload) VALUES(?,?,?,?,?)
			ON CONFLICT(scope,network_id,id,started_at) DO UPDATE SET payload=excluded.payload`, scope, networkID, job.ID, job.StartedAt, string(payload)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) PendingRayJobs(scope, networkID string) ([]ray.Job, error) {
	rows, err := s.db.Query(`SELECT payload FROM ray_job_outbox WHERE scope=? AND network_id=? ORDER BY id LIMIT 100`, scope, networkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []ray.Job{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var job ray.Job
		if err := json.Unmarshal([]byte(raw), &job); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Store) AckRayJobs(scope, networkID string, jobs []ray.Job) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, job := range jobs {
		raw, err := json.Marshal(job)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM ray_job_outbox WHERE scope=? AND network_id=? AND id=? AND payload=?`, scope, networkID, job.ID, string(raw)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
