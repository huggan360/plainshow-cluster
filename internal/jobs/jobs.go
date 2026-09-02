// Package jobs runs work on this machine and streams what it produces.
//
// One job type covers every long-running thing the cluster does: a script, a
// shell command, later a notebook kernel or a training run. One lifecycle means
// one log pipe, one stop button and one permission check for all of them.
package jobs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/events"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

// maxTailLines is how much output is kept in memory for a reconnecting browser.
// The full log is always on disk; this is only what a newly opened tab is shown
// immediately.
const maxTailLines = 2000

// LogLine is one line of job output.
type LogLine struct {
	JobID  string `json:"job_id"`
	Seq    int    `json:"seq"`
	Stream string `json:"stream"`
	Text   string `json:"text"`
	At     string `json:"at"`
}

// running tracks a live process.
type running struct {
	cancel context.CancelFunc
	cmd    *exec.Cmd
	tail   []LogLine
	seq    int
	mu     sync.Mutex
}

// Supervisor starts, tracks and stops jobs on this machine.
type Supervisor struct {
	store  *store.Store
	hub    *events.Hub
	layout config.Layout
	cfg    *config.Config

	mu   sync.Mutex
	live map[string]*running
}

// NewSupervisor wires a supervisor to the node's state and event stream.
func NewSupervisor(st *store.Store, hub *events.Hub, l config.Layout, cfg *config.Config) *Supervisor {
	return &Supervisor{
		store: st, hub: hub, layout: l, cfg: cfg,
		live: make(map[string]*running),
	}
}

// Request describes work to start.
type Request struct {
	ProjectID string
	Project   string
	Kind      string
	Title     string
	Command   string
	Workdir   string
}

// ErrPolicy is returned when the machine's own settings forbid the work.
type ErrPolicy struct{ Reason string }

func (e ErrPolicy) Error() string { return e.Reason }

// admit intersects a request with this machine's local policy. The policy is
// authoritative and local: no remote caller can widen it. This is the check
// that makes it reasonable to run someone else's code on your desktop.
func (s *Supervisor) admit(req Request) error {
	if !s.cfg.Worker.Enabled {
		return ErrPolicy{"this machine is not accepting work (worker disabled)"}
	}
	if !s.cfg.Worker.AllowJobs {
		return ErrPolicy{"this machine does not allow jobs"}
	}
	if req.Kind == "terminal" && !s.cfg.Worker.AllowTerminal {
		return ErrPolicy{"this machine does not allow terminal access"}
	}
	if strings.TrimSpace(req.Command) == "" {
		return ErrPolicy{"no command given"}
	}
	return nil
}

// Start admits, records and launches a job, returning as soon as the process is
// spawned. Output arrives on the event stream.
func (s *Supervisor) Start(req Request) (store.Job, error) {
	if err := s.admit(req); err != nil {
		return store.Job{}, err
	}
	if req.Kind == "" {
		req.Kind = "script"
	}
	if req.Title == "" {
		req.Title = req.Command
	}

	job := store.Job{
		ID:        config.NewID(),
		ProjectID: req.ProjectID,
		Project:   req.Project,
		MachineID: s.cfg.Node.ID,
		Machine:   s.cfg.Node.Name,
		Kind:      req.Kind,
		Title:     req.Title,
		Command:   req.Command,
		Workdir:   req.Workdir,
		State:     store.JobQueued,
		ExitCode:  -1,
		Created:   store.Now(),
	}
	if err := s.store.CreateJob(job); err != nil {
		return store.Job{}, err
	}
	s.hub.Publish("job.created", job)

	go s.exec(job)
	return job, nil
}

// exec runs the process and streams its output. It always reaches a terminal
// state, whatever goes wrong.
func (s *Supervisor) exec(job store.Job) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workdir := job.Workdir
	if workdir == "" {
		workdir = s.layout.Root
	}
	if _, err := os.Stat(workdir); err != nil {
		s.fail(job, fmt.Sprintf("working directory is unavailable: %v", err))
		return
	}

	// The command is a shell line because that is what a user types into a Run
	// box. It runs with this machine's own privileges and under its own policy;
	// sandboxing and resource limits arrive with the worker agent.
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", job.Command)
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(),
		"PSCLUSTER_JOB="+job.ID,
		"PSCLUSTER_PROJECT="+job.Project,
		"PSCLUSTER_NODE="+s.cfg.Node.Name,
		"PSCLUSTER_ROOT="+s.layout.Root,
		"PYTHONUNBUFFERED=1",
	)
	// A process group lets a stop signal reach the whole tree, not just the
	// shell: killing "sh -c python train.py" alone would orphan python.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.fail(job, err.Error())
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		s.fail(job, err.Error())
		return
	}

	logPath := filepath.Join(s.layout.JobLogs(), job.ID+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		s.fail(job, fmt.Sprintf("cannot open log file: %v", err))
		return
	}
	defer logFile.Close()

	if err := cmd.Start(); err != nil {
		s.fail(job, err.Error())
		return
	}

	live := &running{cancel: cancel, cmd: cmd}
	s.mu.Lock()
	s.live[job.ID] = live
	s.mu.Unlock()

	_ = s.store.StartJob(job.ID)
	job.State = store.JobRunning
	job.Started = store.Now()
	s.hub.Publish("job.state", job)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.pump(live, job.ID, "stdout", stdout, logFile) }()
	go func() { defer wg.Done(); s.pump(live, job.ID, "stderr", stderr, logFile) }()
	wg.Wait()

	waitErr := cmd.Wait()

	s.mu.Lock()
	delete(s.live, job.ID)
	s.mu.Unlock()

	state, code, msg := classify(ctx, waitErr)
	_ = s.store.FinishJob(job.ID, state, code, msg)

	job.State = state
	job.ExitCode = code
	job.Error = msg
	job.Ended = store.Now()
	s.hub.Publish("job.state", job)
}

// classify turns a Wait error into the job's terminal state.
func classify(ctx context.Context, err error) (state string, code int, msg string) {
	if ctx.Err() != nil {
		return store.JobStopped, -1, "stopped"
	}
	if err == nil {
		return store.JobSucceeded, 0, ""
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return store.JobFailed, ee.ExitCode(), ""
	}
	return store.JobFailed, -1, err.Error()
}

// pump reads one output stream line by line, writing it to disk, to the
// in-memory tail and to every connected browser.
//
// stdout and stderr are read by separate goroutines so the two can be told
// apart in the interface. That means the interleaving between them is decided
// by the scheduler and is inherently best-effort — but everything downstream
// must still agree on one order, so the sequence number, the in-memory tail and
// the file write all happen inside a single critical section. Without that, a
// reconnecting browser reading the file would see a different order from one
// that watched it live.
func (s *Supervisor) pump(live *running, jobID, stream string, r io.Reader, logFile *os.File) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		text := sc.Text()
		prefix := ""
		if stream == "stderr" {
			prefix = "!"
		}

		live.mu.Lock()
		live.seq++
		line := LogLine{
			JobID: jobID, Seq: live.seq, Stream: stream, Text: text,
			At: time.Now().UTC().Format(time.RFC3339Nano),
		}
		live.tail = append(live.tail, line)
		if len(live.tail) > maxTailLines {
			live.tail = live.tail[len(live.tail)-maxTailLines:]
		}
		_, writeErr := fmt.Fprintf(logFile, "%s%s\n", prefix, text)
		live.mu.Unlock()

		if writeErr != nil {
			// Losing the on-disk copy must not stop the job or the live view.
			log.Printf("job %s: writing log: %v", jobID, writeErr)
		}
		s.hub.Publish("job.log", line)
	}
}

// fail records a job that could not start.
func (s *Supervisor) fail(job store.Job, msg string) {
	_ = s.store.FinishJob(job.ID, store.JobFailed, -1, msg)
	job.State = store.JobFailed
	job.Error = msg
	job.Ended = store.Now()
	s.hub.Publish("job.state", job)
}

// Stop terminates a running job's whole process group, escalating from TERM to
// KILL so a process ignoring the first signal still goes away.
func (s *Supervisor) Stop(id string) error {
	s.mu.Lock()
	live, ok := s.live[id]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("job %s is not running", id)
	}
	if live.cmd.Process != nil {
		pgid, err := syscall.Getpgid(live.cmd.Process.Pid)
		if err == nil {
			_ = syscall.Kill(-pgid, syscall.SIGTERM)
			go func() {
				time.Sleep(5 * time.Second)
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			}()
		}
	}
	live.cancel()
	return nil
}

// Tail returns recent output for a job: the in-memory buffer for a live job,
// the on-disk log for one that has finished.
func (s *Supervisor) Tail(id string, limit int) []LogLine {
	if limit <= 0 || limit > maxTailLines {
		limit = maxTailLines
	}
	s.mu.Lock()
	live, ok := s.live[id]
	s.mu.Unlock()

	if ok {
		live.mu.Lock()
		defer live.mu.Unlock()
		start := 0
		if len(live.tail) > limit {
			start = len(live.tail) - limit
		}
		out := make([]LogLine, len(live.tail[start:]))
		copy(out, live.tail[start:])
		return out
	}
	return s.readLog(id, limit)
}

// readLog replays a finished job's log from disk.
func (s *Supervisor) readLog(id string, limit int) []LogLine {
	out := []LogLine{}
	f, err := os.Open(filepath.Join(s.layout.JobLogs(), id+".log"))
	if err != nil {
		return out
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	seq := 0
	for sc.Scan() {
		text := sc.Text()
		stream := "stdout"
		if strings.HasPrefix(text, "!") {
			stream, text = "stderr", text[1:]
		}
		seq++
		out = append(out, LogLine{JobID: id, Seq: seq, Stream: stream, Text: text})
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// IsRunning reports whether a job is live on this machine.
func (s *Supervisor) IsRunning(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.live[id]
	return ok
}

// StopAll terminates every running job, used on shutdown so a restart never
// leaves orphaned processes behind.
func (s *Supervisor) StopAll() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.live))
	for id := range s.live {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		_ = s.Stop(id)
	}
}
