// Package store owns the node's SQLite state.
//
// The database is small on purpose: machines, projects and job history. Project
// files live on the filesystem, and live telemetry is streamed rather than
// stored.
package store

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("not found")

// Store is a handle on the node's database.
type Store struct{ db *sql.DB }

// Open connects to the database at path, creating and migrating it if needed.
// WAL is enabled so a reader never blocks the writer, which matters because the
// web interface polls state while jobs are writing to it.
func Open(path string) (*Store, error) {
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// modernc's driver is safe for concurrent use, but SQLite serialises
	// writers anyway; a small pool keeps lock contention predictable.
	db.SetMaxOpenConns(4)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// Now renders the current time in the format every timestamp column uses.
func Now() string { return time.Now().UTC().Format(time.RFC3339) }

// ---------------------------------------------------------------- machines --

// Machine is a computer belonging to the cluster.
type Machine struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Roles    []string `json:"roles"`
	OS       string   `json:"os"`
	Arch     string   `json:"arch"`
	Address  string   `json:"address"`
	IsSelf   bool     `json:"is_self"`
	LastSeen string   `json:"last_seen"`
	Created  string   `json:"created_at"`
}

// UpsertMachine inserts or updates a machine row by id.
func (s *Store) UpsertMachine(m Machine) error {
	self := 0
	if m.IsSelf {
		self = 1
	}
	_, err := s.db.Exec(`
        INSERT INTO machine (id, name, roles, os, arch, address, is_self, last_seen, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET
            name=excluded.name, roles=excluded.roles, os=excluded.os,
            arch=excluded.arch, address=excluded.address,
            is_self=excluded.is_self, last_seen=excluded.last_seen`,
		m.ID, m.Name, strings.Join(m.Roles, ","), m.OS, m.Arch,
		m.Address, self, m.LastSeen, Now())
	return err
}

// Machines lists every known machine, this one first.
func (s *Store) Machines() ([]Machine, error) {
	rows, err := s.db.Query(`
        SELECT id, name, roles, os, arch, address, is_self, last_seen, created_at
        FROM machine ORDER BY is_self DESC, name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Machine{}
	for rows.Next() {
		var m Machine
		var roles string
		var self int
		if err := rows.Scan(&m.ID, &m.Name, &roles, &m.OS, &m.Arch,
			&m.Address, &self, &m.LastSeen, &m.Created); err != nil {
			return nil, err
		}
		m.IsSelf = self == 1
		if roles != "" {
			m.Roles = strings.Split(roles, ",")
		} else {
			m.Roles = []string{}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- projects --

// Project is a directory of code with a name.
type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Created     string `json:"created_at"`
	Updated     string `json:"updated_at"`
}

// CreateProject records a new project, filling in its timestamps so the caller
// can return the row it just wrote without reading it back.
func (s *Store) CreateProject(p *Project) error {
	now := Now()
	p.Created, p.Updated = now, now
	_, err := s.db.Exec(`
        INSERT INTO project (id, name, description, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Description, now, now)
	return err
}

// Projects lists all projects, most recently touched first.
func (s *Store) Projects() ([]Project, error) {
	rows, err := s.db.Query(`
        SELECT id, name, description, created_at, updated_at
        FROM project ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Created, &p.Updated); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProjectByName looks a project up by its directory name.
func (s *Store) ProjectByName(name string) (Project, error) {
	var p Project
	err := s.db.QueryRow(`
        SELECT id, name, description, created_at, updated_at
        FROM project WHERE name = ?`, name).
		Scan(&p.ID, &p.Name, &p.Description, &p.Created, &p.Updated)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// TouchProject marks a project as changed.
func (s *Store) TouchProject(name string) error {
	_, err := s.db.Exec(`UPDATE project SET updated_at = ? WHERE name = ?`, Now(), name)
	return err
}

// DeleteProject removes a project row. Files are removed separately, by the
// caller, so a failed delete never leaves the row gone and the data orphaned.
func (s *Store) DeleteProject(name string) error {
	_, err := s.db.Exec(`DELETE FROM project WHERE name = ?`, name)
	return err
}

// -------------------------------------------------------------------- jobs --

// Job states. A job is created queued, becomes running, and ends in exactly one
// terminal state.
const (
	JobQueued    = "queued"
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
	JobStopped   = "stopped"
)

// Job is a unit of work: a script, a command, later a notebook kernel or a
// training run. One lifecycle covers all of them.
type Job struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Project   string `json:"project"`
	MachineID string `json:"machine_id"`
	Machine   string `json:"machine"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Command   string `json:"command"`
	Workdir   string `json:"workdir"`
	State     string `json:"state"`
	ExitCode  int    `json:"exit_code"`
	Error     string `json:"error"`
	Created   string `json:"created_at"`
	Started   string `json:"started_at"`
	Ended     string `json:"ended_at"`
}

// Terminal reports whether the job has finished, however it finished.
func (j Job) Terminal() bool {
	return j.State == JobSucceeded || j.State == JobFailed || j.State == JobStopped
}

// CreateJob records a newly submitted job.
func (s *Store) CreateJob(j Job) error {
	_, err := s.db.Exec(`
        INSERT INTO job (id, project_id, machine_id, kind, title, command,
                         workdir, state, exit_code, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, -1, ?)`,
		j.ID, j.ProjectID, j.MachineID, j.Kind, j.Title, j.Command,
		j.Workdir, JobQueued, Now())
	return err
}

// StartJob marks a job as running.
func (s *Store) StartJob(id string) error {
	_, err := s.db.Exec(`UPDATE job SET state = ?, started_at = ? WHERE id = ?`,
		JobRunning, Now(), id)
	return err
}

// FinishJob records a job's terminal state.
func (s *Store) FinishJob(id, state string, code int, errMsg string) error {
	_, err := s.db.Exec(`
        UPDATE job SET state = ?, exit_code = ?, error = ?, ended_at = ?
        WHERE id = ?`, state, code, errMsg, Now(), id)
	return err
}

const jobSelect = `
    SELECT j.id, j.project_id, COALESCE(p.name, ''), j.machine_id,
           COALESCE(m.name, ''), j.kind, j.title, j.command, j.workdir,
           j.state, j.exit_code, j.error, j.created_at, j.started_at, j.ended_at
    FROM job j
    LEFT JOIN project p ON p.id = j.project_id
    LEFT JOIN machine m ON m.id = j.machine_id`

func scanJobs(rows *sql.Rows) ([]Job, error) {
	out := []Job{}
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.ProjectID, &j.Project, &j.MachineID,
			&j.Machine, &j.Kind, &j.Title, &j.Command, &j.Workdir, &j.State,
			&j.ExitCode, &j.Error, &j.Created, &j.Started, &j.Ended); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// Jobs lists recent jobs, newest first.
func (s *Store) Jobs(limit int) ([]Job, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(jobSelect+` ORDER BY j.created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// ActiveJobs lists jobs that have not reached a terminal state.
func (s *Store) ActiveJobs() ([]Job, error) {
	rows, err := s.db.Query(jobSelect+`
        WHERE j.state IN (?, ?) ORDER BY j.created_at DESC`, JobQueued, JobRunning)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// Job fetches a single job.
func (s *Store) Job(id string) (Job, error) {
	rows, err := s.db.Query(jobSelect+` WHERE j.id = ?`, id)
	if err != nil {
		return Job{}, err
	}
	defer rows.Close()
	jobs, err := scanJobs(rows)
	if err != nil {
		return Job{}, err
	}
	if len(jobs) == 0 {
		return Job{}, ErrNotFound
	}
	return jobs[0], nil
}

// RecoverRunningJobs marks jobs left running by an unclean shutdown as stopped.
// Called once at startup: a process supervised by a daemon that is no longer
// alive cannot still be running, and leaving it "running" in the UI is a lie.
func (s *Store) RecoverRunningJobs() (int, error) {
	res, err := s.db.Exec(`
        UPDATE job SET state = ?, error = ?, ended_at = ?
        WHERE state IN (?, ?)`,
		JobStopped, "interrupted by node restart", Now(), JobQueued, JobRunning)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
