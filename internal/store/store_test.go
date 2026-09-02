package store

import (
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "cluster.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestProjectLifecycle(t *testing.T) {
	st := open(t)

	p := Project{ID: "p1", Name: "vision-model", Description: "test"}
	if err := st.CreateProject(&p); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.Created == "" || p.Updated == "" {
		t.Error("CreateProject should fill in timestamps on the row it wrote")
	}

	got, err := st.ProjectByName("vision-model")
	if err != nil {
		t.Fatalf("ProjectByName: %v", err)
	}
	if got.ID != "p1" {
		t.Errorf("ProjectByName returned %+v", got)
	}

	if _, err := st.ProjectByName("nope"); err != ErrNotFound {
		t.Errorf("missing project error = %v, want ErrNotFound", err)
	}

	if err := st.DeleteProject("vision-model"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := st.ProjectByName("vision-model"); err != ErrNotFound {
		t.Error("project survived deletion")
	}
}

func TestJobLifecycle(t *testing.T) {
	st := open(t)
	p := Project{ID: "p1", Name: "proj"}
	if err := st.CreateProject(&p); err != nil {
		t.Fatal(err)
	}

	job := Job{ID: "j1", ProjectID: "p1", Kind: "script", Command: "echo hi"}
	if err := st.CreateJob(job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	active, err := st.ActiveJobs()
	if err != nil || len(active) != 1 {
		t.Fatalf("ActiveJobs = %d, %v; want 1", len(active), err)
	}
	if active[0].Project != "proj" {
		t.Errorf("job should join its project name, got %q", active[0].Project)
	}

	if err := st.StartJob("j1"); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishJob("j1", JobSucceeded, 0, ""); err != nil {
		t.Fatal(err)
	}

	got, err := st.Job("j1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != JobSucceeded || !got.Terminal() || got.Ended == "" {
		t.Errorf("finished job = %+v", got)
	}
	if active, _ := st.ActiveJobs(); len(active) != 0 {
		t.Errorf("finished job still counted as active")
	}
}

// TestRecoverRunningJobs covers the restart case: a job cannot still be running
// if the daemon supervising it is gone.
func TestRecoverRunningJobs(t *testing.T) {
	st := open(t)
	for _, id := range []string{"a", "b"} {
		if err := st.CreateJob(Job{ID: id, Kind: "script", Command: "sleep 100"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.StartJob("a"); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishJob("b", JobSucceeded, 0, ""); err != nil {
		t.Fatal(err)
	}

	n, err := st.RecoverRunningJobs()
	if err != nil {
		t.Fatalf("RecoverRunningJobs: %v", err)
	}
	if n != 1 {
		t.Errorf("recovered %d jobs, want 1", n)
	}
	got, _ := st.Job("a")
	if got.State != JobStopped {
		t.Errorf("interrupted job state = %q, want %q", got.State, JobStopped)
	}
	if done, _ := st.Job("b"); done.State != JobSucceeded {
		t.Errorf("a finished job must not be rewritten, got %q", done.State)
	}
}

func TestUpsertMachineIsIdempotent(t *testing.T) {
	st := open(t)
	m := Machine{ID: "m1", Name: "pi", Roles: []string{"master", "worker"}, IsSelf: true}
	for i := 0; i < 3; i++ {
		if err := st.UpsertMachine(m); err != nil {
			t.Fatalf("UpsertMachine: %v", err)
		}
	}
	machines, err := st.Machines()
	if err != nil {
		t.Fatal(err)
	}
	if len(machines) != 1 {
		t.Fatalf("got %d machines, want 1", len(machines))
	}
	if len(machines[0].Roles) != 2 || machines[0].Roles[0] != "master" {
		t.Errorf("roles round-tripped wrongly: %v", machines[0].Roles)
	}
}
