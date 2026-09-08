package accountserver

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/huggan360/plainshow-cluster/internal/ray"
)

type JobReport struct {
	NodeID string    `json:"node_id"`
	Jobs   []ray.Job `json:"jobs"`
}

type JobHistory struct {
	Jobs  []ray.Job `json:"jobs"`
	Total int       `json:"total"`
}

// RecordNetworkJobs accepts observations only from an owned, enrolled device
// on a network the account can contribute to. Reports cannot create networks.
// Terminal states never regress when another device submits an older snapshot.
func (s *Store) RecordNetworkJobs(accountID, networkID string, report JobReport) (bool, error) {
	if len(report.Jobs) > 100 {
		return false, errors.New("report at most 100 jobs at a time")
	}
	for _, job := range report.Jobs {
		if job.ID == "" || len(job.ID) > 256 || len(job.Entrypoint) > 4096 || len(job.Message) > 4096 || job.StartedAt < 0 || job.EndedAt < 0 {
			return false, errors.New("invalid job summary")
		}
		switch job.Status {
		case "PENDING", "RUNNING", "SUCCEEDED", "FAILED", "STOPPED":
		default:
			return false, errors.New("invalid Ray job status")
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var allowed int
	err = tx.QueryRow(`SELECT 1 FROM network_member m JOIN node_network nn ON nn.network_id=m.network_id
		JOIN node n ON n.id=nn.node_id WHERE m.account_id=? AND m.network_id=?
		AND m.role!='viewer' AND n.id=? AND n.owner_account_id=?`, accountID, networkID, report.NodeID, accountID).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNetworkMember
	}
	if err != nil {
		return false, err
	}
	changed := false
	for _, job := range report.Jobs {
		if job.StartedAt == 0 {
			var dated int
			if err := tx.QueryRow(`SELECT count(*) FROM ray_job_history WHERE network_id=? AND id=? AND started_at>0`, networkID, job.ID).Scan(&dated); err != nil {
				return false, err
			}
			if dated > 0 {
				continue
			}
		}
		// A new Ray head may reuse a driver/submission ID. Keep distinct runs
		// by their start timestamp, while promoting an undated pending entry.
		if job.StartedAt > 0 {
			if _, err := tx.Exec(`DELETE FROM ray_job_history WHERE network_id=? AND id=? AND started_at=0 AND status='PENDING'`, networkID, job.ID); err != nil {
				return false, err
			}
		}
		var old ray.Job
		err := tx.QueryRow(`SELECT id,status,entrypoint,message,started_at,ended_at FROM ray_job_history WHERE network_id=? AND id=? AND started_at=?`, networkID, job.ID, job.StartedAt).
			Scan(&old.ID, &old.Status, &old.Entrypoint, &old.Message, &old.StartedAt, &old.EndedAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if err == nil && (old == job || !old.Running() || old.Status == "RUNNING" && job.Status == "PENDING") {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO ray_job_history(network_id,id,status,entrypoint,message,started_at,ended_at)
			VALUES(?,?,?,?,?,?,?) ON CONFLICT(network_id,id,started_at) DO UPDATE SET status=excluded.status,
			entrypoint=excluded.entrypoint,message=excluded.message,started_at=excluded.started_at,ended_at=excluded.ended_at`,
			networkID, job.ID, job.Status, job.Entrypoint, job.Message, job.StartedAt, job.EndedAt); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, tx.Commit()
}

func (s *Store) NetworkJobHistory(accountID, networkID string, offset int) (JobHistory, error) {
	out := JobHistory{Jobs: []ray.Job{}}
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var member int
	err = tx.QueryRow(`SELECT 1 FROM network_member WHERE account_id=? AND network_id=?`, accountID, networkID).Scan(&member)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNetworkMember
	}
	if err != nil {
		return out, err
	}
	if err := tx.QueryRow(`SELECT count(*) FROM ray_job_history WHERE network_id=?`, networkID).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.Query(`SELECT id,status,entrypoint,message,started_at,ended_at FROM ray_job_history WHERE network_id=? ORDER BY started_at DESC,id DESC LIMIT 50 OFFSET ?`, networkID, offset)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var job ray.Job
		if err := rows.Scan(&job.ID, &job.Status, &job.Entrypoint, &job.Message, &job.StartedAt, &job.EndedAt); err != nil {
			return out, err
		}
		out.Jobs = append(out.Jobs, job)
	}
	return out, rows.Err()
}

func (s *Server) reportNetworkJobs(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	var report JobReport
	if err := decode(r, &report); err != nil {
		fail(w, 400, err.Error())
		return
	}
	changed, err := s.store.RecordNetworkJobs(account.ID, r.PathValue("id"), report)
	if errors.Is(err, ErrNetworkMember) {
		fail(w, 403, "An enrolled device and network membership are required.")
		return
	}
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if changed {
		members, _ := s.store.NetworkMembers(r.PathValue("id"))
		for _, member := range members {
			s.watchers.notify(member.AccountID, TopicJobs)
		}
	}
	writeJSON(w, 200, map[string]bool{"changed": changed})
}

func (s *Server) networkJobs(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	offset, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("offset")))
	if offset < 0 {
		offset = 0
	}
	items, err := s.store.NetworkJobHistory(account.ID, r.PathValue("id"), offset)
	if errors.Is(err, ErrNetworkMember) {
		fail(w, 403, "You are not a member of that network.")
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, items)
}
