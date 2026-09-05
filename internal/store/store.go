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

// ErrAmbiguous means a human-readable name exists in more than one network.
// Callers should retry with the stable project id (or an explicit network).
var ErrAmbiguous = errors.New("ambiguous")

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
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return s, nil
}

// migrate applies changes that a declarative schema cannot express.
//
// SQLite has no "ADD COLUMN IF NOT EXISTS", and re-running a plain ALTER fails
// on every start after the first. Each step here checks the database rather
// than tracking a version number, so it is safe to run on a fresh database, on
// an old one, and repeatedly.
func (s *Store) migrate() error {
	columns := []struct{ table, column, definition string }{
		// A project may mirror a GitHub repository. Cached here so the
		// interface can show it without shelling out to git; the live remote
		// still wins when the two disagree.
		{"project", "repository", "TEXT NOT NULL DEFAULT ''"},
		{"project", "network_id", "TEXT NOT NULL DEFAULT ''"},
		{"network_node", "os", "TEXT NOT NULL DEFAULT ''"},
		{"network_node", "arch", "TEXT NOT NULL DEFAULT ''"},
		{"network_node", "capacity", "TEXT NOT NULL DEFAULT '{}'"},
		{"network_node", "fingerprint", "TEXT NOT NULL DEFAULT ''"},
		// Which projects this machine actually has on disk. A machine cannot
		// run work on files it does not hold, so this is the difference
		// between "online" and "ready".
		{"network_node", "projects", "TEXT NOT NULL DEFAULT '[]'"},
	}
	for _, c := range columns {
		has, err := s.hasColumn(c.table, c.column)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", c.table, c.column, c.definition)
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	if err := s.migrateProjectScope(); err != nil {
		return err
	}
	_, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS project_network_idx
        ON project (network_id, updated_at DESC)`)
	return err
}

func (s *Store) migrateProjectScope() error {
	const migration = "project-network-scope-v1"
	var exists int
	if err := s.db.QueryRow(`SELECT count(*) FROM schema_migration WHERE name=?`, migration).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE TABLE project_scoped (
            id TEXT PRIMARY KEY, network_id TEXT NOT NULL DEFAULT '', name TEXT NOT NULL,
            description TEXT NOT NULL DEFAULT '', repository TEXT NOT NULL DEFAULT '',
            created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
            UNIQUE(network_id, name))`,
		`INSERT INTO project_scoped
            (id,network_id,name,description,repository,created_at,updated_at)
            SELECT id,network_id,name,description,repository,created_at,updated_at FROM project`,
		`DROP TABLE project`,
		`ALTER TABLE project_scoped RENAME TO project`,
		`INSERT INTO schema_migration(name,applied_at) VALUES ('project-network-scope-v1', datetime('now'))`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// hasColumn reports whether a table already carries a column.
func (s *Store) hasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid        int
			name, typ  string
			notNull    int
			dflt       sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
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
	NetworkID   string `json:"network_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Repository  string `json:"repository"`
	Created     string `json:"created_at"`
	Updated     string `json:"updated_at"`
	Branch      string `json:"branch,omitempty"`
}

// CreateProject records a new project, filling in its timestamps so the caller
// can return the row it just wrote without reading it back.
func (s *Store) CreateProject(p *Project) error {
	now := Now()
	p.Created, p.Updated = now, now
	_, err := s.db.Exec(`
        INSERT INTO project (id, network_id, name, description, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?)`,
		p.ID, p.NetworkID, p.Name, p.Description, now, now)
	return err
}

// MoveProjectToNetwork changes which network a project belongs to.
//
// A project lives in exactly one network — that is what makes "who can see this"
// answerable — so moving it is a move, not a copy. The unique index on
// (network_id, name) means a clashing name in the destination fails here rather
// than producing two projects that look the same.
func (s *Store) MoveProjectToNetwork(id, networkID string) error {
	result, err := s.db.Exec(`UPDATE project SET network_id=?, updated_at=? WHERE id=?`,
		networkID, Now(), id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

// ProjectsWithRepository finds every project pointing at one GitHub repository,
// across all networks. A repository belongs to one project in one network, and
// this is what catches the second one being made.
func (s *Store) ProjectsWithRepository(repository string) ([]Project, error) {
	rows, err := s.db.Query(`SELECT id,network_id,name,description,repository,created_at,updated_at
        FROM project WHERE lower(repository)=lower(?) AND repository<>''`, repository)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		var project Project
		if err := rows.Scan(&project.ID, &project.NetworkID, &project.Name,
			&project.Description, &project.Repository, &project.Created,
			&project.Updated); err != nil {
			return nil, err
		}
		out = append(out, project)
	}
	return out, rows.Err()
}

func (s *Store) UpdateProjectDescription(id, description string) error {
	result, err := s.db.Exec(`UPDATE project SET description=?, updated_at=? WHERE id=?`,
		description, Now(), id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

// Projects lists all projects, most recently touched first.
func (s *Store) Projects() ([]Project, error) {
	rows, err := s.db.Query(`
		SELECT id, network_id, name, description, repository, created_at, updated_at
        FROM project ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.NetworkID, &p.Name, &p.Description, &p.Repository,
			&p.Created, &p.Updated); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProjectsInNetwork lists projects belonging to one independent network.
func (s *Store) ProjectsInNetwork(networkID string) ([]Project, error) {
	rows, err := s.db.Query(`
        SELECT id, network_id, name, description, repository, created_at, updated_at
        FROM project WHERE network_id=? ORDER BY updated_at DESC`, networkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.NetworkID, &p.Name, &p.Description,
			&p.Repository, &p.Created, &p.Updated); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProjectByName looks a project up by its directory name.
func (s *Store) ProjectByName(name string) (Project, error) {
	rows, err := s.db.Query(`
		SELECT id, network_id, name, description, repository, created_at, updated_at
		FROM project WHERE name = ? ORDER BY updated_at DESC LIMIT 2`, name)
	if err != nil {
		return Project{}, err
	}
	defer rows.Close()
	var p Project
	if !rows.Next() {
		return p, ErrNotFound
	}
	if err := rows.Scan(&p.ID, &p.NetworkID, &p.Name, &p.Description,
		&p.Repository, &p.Created, &p.Updated); err != nil {
		return Project{}, err
	}
	if rows.Next() {
		return Project{}, ErrAmbiguous
	}
	return p, rows.Err()
}

// ProjectByID resolves the stable project identity used by network-independent
// API routes. Names are for people and may be reused in another network; ids
// are what make opening a project unambiguous without a global network picker.
func (s *Store) ProjectByID(id string) (Project, error) {
	var p Project
	err := s.db.QueryRow(`
		SELECT id, network_id, name, description, repository, created_at, updated_at
		FROM project WHERE id = ?`, id).
		Scan(&p.ID, &p.NetworkID, &p.Name, &p.Description, &p.Repository, &p.Created, &p.Updated)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

func (s *Store) ProjectByNameInNetwork(networkID, name string) (Project, error) {
	var p Project
	err := s.db.QueryRow(`
        SELECT id, network_id, name, description, repository, created_at, updated_at
        FROM project WHERE network_id=? AND name=?`, networkID, name).
		Scan(&p.ID, &p.NetworkID, &p.Name, &p.Description, &p.Repository, &p.Created, &p.Updated)
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

func (s *Store) TouchProjectID(id string) error {
	_, err := s.db.Exec(`UPDATE project SET updated_at = ? WHERE id = ?`, Now(), id)
	return err
}

// SetProjectRepository records which GitHub repository a project mirrors.
func (s *Store) SetProjectRepository(name, repository string) error {
	_, err := s.db.Exec(`UPDATE project SET repository = ?, updated_at = ? WHERE name = ?`,
		repository, Now(), name)
	return err
}

func (s *Store) SetProjectRepositoryID(id, repository string) error {
	_, err := s.db.Exec(`UPDATE project SET repository = ?, updated_at = ? WHERE id = ?`,
		repository, Now(), id)
	return err
}

// DeleteProject removes a project row. Files are removed separately, by the
// caller, so a failed delete never leaves the row gone and the data orphaned.
func (s *Store) DeleteProject(name string) error {
	p, err := s.ProjectByName(name)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM member WHERE project_id = ?`, p.ID); err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM project WHERE name = ?`, name)
	return err
}

func (s *Store) DeleteProjectID(id string) error {
	if _, err := s.db.Exec(`DELETE FROM member WHERE project_id = ?`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM project WHERE id = ?`, id)
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

// CreateRemoteJob records a job whose process is supervised by another node.
// Its ID is allocated by that worker so subsequent status and log requests use
// one stable identifier on both sides of the mesh.
func (s *Store) CreateRemoteJob(j Job) error {
	_, err := s.db.Exec(`INSERT INTO job
        (id,project_id,machine_id,kind,title,command,workdir,state,exit_code,created_at,started_at,ended_at,error)
        VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, j.ID, j.ProjectID, j.MachineID,
		j.Kind, j.Title, j.Command, j.Workdir, j.State, j.ExitCode, j.Created,
		j.Started, j.Ended, j.Error)
	return err
}

// SyncRemoteJob refreshes the lifecycle fields mirrored from its worker.
func (s *Store) SyncRemoteJob(j Job) error {
	_, err := s.db.Exec(`UPDATE job SET state=?,exit_code=?,error=?,started_at=?,ended_at=? WHERE id=?`,
		j.State, j.ExitCode, j.Error, j.Started, j.Ended, j.ID)
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
