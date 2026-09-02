package jobs

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/events"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

// newSupervisor builds a supervisor over a throwaway node.
func newSupervisor(t *testing.T, tune func(*config.Config)) (*Supervisor, *store.Store, *events.Hub) {
	t.Helper()
	l, err := config.NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(l.Database())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := config.Defaults()
	if tune != nil {
		tune(cfg)
	}
	hub := events.NewHub()
	return NewSupervisor(st, hub, l, cfg), st, hub
}

// waitForState blocks until the job reaches a terminal state, or fails the test.
func waitForState(t *testing.T, st *store.Store, id string) store.Job {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		job, err := st.Job(id)
		if err == nil && job.Terminal() {
			return job
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s never finished", id)
	return store.Job{}
}

func TestJobSucceeds(t *testing.T) {
	sup, st, hub := newSupervisor(t, nil)
	sub := hub.Subscribe()
	defer sub.Close()

	job, err := sup.Start(Request{Command: "echo hello; echo oops >&2"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := waitForState(t, st, job.ID)
	if done.State != store.JobSucceeded || done.ExitCode != 0 {
		t.Fatalf("job = %s exit %d, want succeeded exit 0", done.State, done.ExitCode)
	}

	lines := sup.Tail(job.ID, 100)
	if len(lines) != 2 {
		t.Fatalf("got %d log lines, want 2: %+v", len(lines), lines)
	}
	var sawStdout, sawStderr bool
	for _, l := range lines {
		if l.Stream == "stdout" && l.Text == "hello" {
			sawStdout = true
		}
		if l.Stream == "stderr" && l.Text == "oops" {
			sawStderr = true
		}
	}
	if !sawStdout || !sawStderr {
		t.Errorf("streams not separated correctly: %+v", lines)
	}
}

// TestJobFailureKeepsExitCode: a non-zero exit is a result, not a crash, and
// the code must survive to the UI.
func TestJobFailureKeepsExitCode(t *testing.T) {
	sup, st, _ := newSupervisor(t, nil)
	job, err := sup.Start(Request{Command: "exit 42"})
	if err != nil {
		t.Fatal(err)
	}
	done := waitForState(t, st, job.ID)
	if done.State != store.JobFailed || done.ExitCode != 42 {
		t.Errorf("job = %s exit %d, want failed exit 42", done.State, done.ExitCode)
	}
}

// TestStopKillsProcessGroup checks that stopping reaches a child of the shell,
// not just the shell itself.
func TestStopKillsProcessGroup(t *testing.T) {
	sup, st, _ := newSupervisor(t, nil)
	job, err := sup.Start(Request{Command: "sleep 60"})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for !sup.IsRunning(job.ID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !sup.IsRunning(job.ID) {
		t.Fatal("job never started running")
	}
	if err := sup.Stop(job.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	done := waitForState(t, st, job.ID)
	if done.State != store.JobStopped {
		t.Errorf("stopped job state = %q, want %q", done.State, store.JobStopped)
	}
}

// TestPolicyIsEnforcedLocally is the security property that makes it reasonable
// to run someone else's code: the machine's own settings decide, and a caller
// cannot talk its way past them.
func TestPolicyIsEnforcedLocally(t *testing.T) {
	cases := []struct {
		name string
		tune func(*config.Config)
		req  Request
	}{
		{"worker disabled", func(c *config.Config) { c.Worker.Enabled = false },
			Request{Command: "echo hi"}},
		{"jobs not allowed", func(c *config.Config) { c.Worker.AllowJobs = false },
			Request{Command: "echo hi"}},
		{"terminal not allowed", func(c *config.Config) { c.Worker.AllowTerminal = false },
			Request{Kind: "terminal", Command: "bash"}},
		{"empty command", nil, Request{Command: "   "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sup, st, _ := newSupervisor(t, tc.tune)
			if _, err := sup.Start(tc.req); err == nil {
				t.Fatal("Start was allowed despite local policy")
			}
			jobs, _ := st.Jobs(10)
			if len(jobs) != 0 {
				t.Errorf("a refused job should not be recorded, got %d", len(jobs))
			}
		})
	}
}

// TestTailReadsFinishedJobFromDisk covers the reconnecting-browser case: the
// in-memory buffer is gone once a job ends, so the log must come off disk with
// its stream tags intact.
//
// It deliberately does not assert an order between stdout and stderr: those are
// separate pipes read by separate goroutines, so their interleaving is decided
// by the scheduler. Ordering *within* one stream is guaranteed, and that is
// what is checked here.
func TestTailReadsFinishedJobFromDisk(t *testing.T) {
	sup, st, _ := newSupervisor(t, nil)
	job, err := sup.Start(Request{Command: "echo one; echo two >&2; echo three"})
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, st, job.ID)

	lines := sup.Tail(job.ID, 100)
	if len(lines) != 3 {
		t.Fatalf("replayed %d lines, want 3: %+v", len(lines), lines)
	}

	streams := map[string][]string{}
	for _, l := range lines {
		streams[l.Stream] = append(streams[l.Stream], l.Text)
	}
	if got := streams["stderr"]; len(got) != 1 || got[0] != "two" {
		t.Errorf("stderr replayed as %v, want [two]", got)
	}
	out := streams["stdout"]
	if len(out) != 2 || out[0] != "one" || out[1] != "three" {
		t.Errorf("stdout replayed as %v, want [one three] in order", out)
	}
}

// TestLiveAndReplayedLogsAgree is the property the shared critical section in
// pump exists to guarantee: a browser watching live and one reconnecting
// afterwards must see the same lines in the same order.
func TestLiveAndReplayedLogsAgree(t *testing.T) {
	sup, st, hub := newSupervisor(t, nil)
	sub := hub.Subscribe()
	defer sub.Close()

	var (
		mu   sync.Mutex
		live []LogLine
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range sub.C {
			if ev.Topic != "job.log" {
				continue
			}
			if line, ok := ev.Data.(LogLine); ok {
				mu.Lock()
				live = append(live, line)
				mu.Unlock()
			}
		}
	}()

	job, err := sup.Start(Request{
		Command: "for i in 1 2 3 4 5 6 7 8; do echo out$i; echo err$i >&2; done",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, st, job.ID)

	// The job is finished, but its last log events may still be in flight to
	// this subscriber. Wait for the count to settle rather than sleeping a
	// fixed amount: under a loaded test run any fixed wait is eventually too
	// short, and this test then fails for a reason that has nothing to do with
	// what it checks.
	replayed := sup.Tail(job.ID, 100)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		seen := len(live)
		mu.Unlock()
		if seen >= len(replayed) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	sub.Close()
	<-done

	if len(replayed) != len(live) {
		t.Fatalf("replayed %d lines but %d were broadcast", len(replayed), len(live))
	}
	for i := range live {
		if live[i].Text != replayed[i].Text || live[i].Stream != replayed[i].Stream {
			t.Fatalf("line %d differs: live %+v, replayed %+v", i, live[i], replayed[i])
		}
	}
}

func TestWorkdirIsUsed(t *testing.T) {
	sup, st, _ := newSupervisor(t, nil)
	dir := t.TempDir()
	job, err := sup.Start(Request{Command: "pwd", Workdir: dir})
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, st, job.ID)

	lines := sup.Tail(job.ID, 10)
	if len(lines) == 0 {
		t.Fatal("no output")
	}
	// macOS and some Linux setups hand back a symlinked temp path, so compare
	// the resolved forms rather than the strings.
	got, _ := filepath.EvalSymlinks(lines[0].Text)
	want, _ := filepath.EvalSymlinks(dir)
	if got != want {
		t.Errorf("ran in %q, want %q", lines[0].Text, dir)
	}
}

func TestMissingWorkdirFailsCleanly(t *testing.T) {
	sup, st, _ := newSupervisor(t, nil)
	job, err := sup.Start(Request{Command: "echo hi", Workdir: "/does/not/exist"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done := waitForState(t, st, job.ID)
	if done.State != store.JobFailed || done.Error == "" {
		t.Errorf("job = %+v, want failed with an explanation", done)
	}
}
