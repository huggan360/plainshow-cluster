package store

import "encoding/json"

type TrainingRank struct {
	Rank   int    `json:"rank"`
	NodeID string `json:"node_id"`
	Node   string `json:"node"`
	JobID  string `json:"job_id"`
}
type TrainingRun struct {
	ID        string         `json:"id"`
	NetworkID string         `json:"network_id"`
	ProjectID string         `json:"project_id"`
	Framework string         `json:"framework"`
	State     string         `json:"state"`
	Ranks     []TrainingRank `json:"ranks"`
	Created   string         `json:"created_at"`
	Updated   string         `json:"updated_at"`
}

func (s *Store) CreateTrainingRun(run *TrainingRun) error {
	now := Now()
	run.Created = now
	run.Updated = now
	raw, _ := json.Marshal(run.Ranks)
	_, err := s.db.Exec(`INSERT INTO training_run(id,network_id,project_id,framework,state,ranks,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, run.ID, run.NetworkID, run.ProjectID, run.Framework, run.State, string(raw), now, now)
	return err
}
func (s *Store) UpdateTrainingRun(run TrainingRun) error {
	run.Updated = Now()
	raw, _ := json.Marshal(run.Ranks)
	_, err := s.db.Exec(`UPDATE training_run SET state=?,ranks=?,updated_at=? WHERE id=?`, run.State, string(raw), run.Updated, run.ID)
	return err
}
func (s *Store) TrainingRuns(networkID string) ([]TrainingRun, error) {
	rows, err := s.db.Query(`SELECT id,network_id,project_id,framework,state,ranks,created_at,updated_at FROM training_run WHERE network_id=? ORDER BY created_at DESC`, networkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrainingRun{}
	for rows.Next() {
		var run TrainingRun
		var raw string
		if err := rows.Scan(&run.ID, &run.NetworkID, &run.ProjectID, &run.Framework, &run.State, &raw, &run.Created, &run.Updated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(raw), &run.Ranks)
		out = append(out, run)
	}
	return out, rows.Err()
}
