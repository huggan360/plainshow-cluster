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

// TestMovingAProjectBetweenNetworks: a project lives in exactly one network,
// so this is a move. The unique index is what stops a clashing name in the
// destination from producing two projects that look the same.
func TestMovingAProjectBetweenNetworks(t *testing.T) {
	st := open(t)
	first := Project{ID: "p1", NetworkID: "net-a", Name: "vision"}
	if err := st.CreateProject(&first); err != nil {
		t.Fatal(err)
	}
	if err := st.MoveProjectToNetwork(first.ID, "net-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ProjectByNameInNetwork("net-a", "vision"); err == nil {
		t.Error("the project is still visible in the network it left")
	}
	moved, err := st.ProjectByNameInNetwork("net-b", "vision")
	if err != nil || moved.ID != first.ID {
		t.Fatalf("project in net-b = %+v, %v", moved, err)
	}

	// A name already taken in the destination must fail rather than duplicate.
	clash := Project{ID: "p2", NetworkID: "net-a", Name: "vision"}
	if err := st.CreateProject(&clash); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ProjectByName("vision"); err != ErrAmbiguous {
		t.Fatalf("unscoped duplicate lookup error = %v, want ErrAmbiguous", err)
	}
	if byID, err := st.ProjectByID(clash.ID); err != nil || byID.NetworkID != "net-a" {
		t.Fatalf("stable id lookup = %+v, %v", byID, err)
	}
	if err := st.MoveProjectToNetwork(clash.ID, "net-b"); err == nil {
		t.Error("two projects with the same name landed in one network")
	}
}

// TestFindingProjectsByRepository backs the rule that one GitHub repository
// belongs to one project: two would give it two sets of collaborators and two
// answers to who can see it.
func TestFindingProjectsByRepository(t *testing.T) {
	st := open(t)
	one := Project{ID: "p1", NetworkID: "net-a", Name: "vision"}
	if err := st.CreateProject(&one); err != nil {
		t.Fatal(err)
	}
	if err := st.SetProjectRepositoryID(one.ID, "huggan360/vision"); err != nil {
		t.Fatal(err)
	}
	two := Project{ID: "p2", NetworkID: "net-b", Name: "vision-again"}
	if err := st.CreateProject(&two); err != nil {
		t.Fatal(err)
	}
	found, err := st.ProjectsWithRepository("HUGGAN360/VISION")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != one.ID {
		t.Fatalf("lookup = %+v, want the one project that has it", found)
	}
	// An empty repository is not a repository, and must never match.
	if empty, err := st.ProjectsWithRepository(""); err != nil || len(empty) != 0 {
		t.Fatalf("empty lookup = %+v, %v", empty, err)
	}
}

// TestNodeProjectsSurviveAWriteAndRead keeps the gossiped "has the files" list
// honest across the storage boundary.
func TestNodeProjectsSurviveAWriteAndRead(t *testing.T) {
	st := open(t)
	if err := st.UpsertNetworkNode(NetworkNode{
		NetworkID: "net-a", NodeID: "n1", Name: "Stationary",
		Projects: []string{"vision", "speech"}, LastSeen: Now(),
	}); err != nil {
		t.Fatal(err)
	}
	nodes, err := st.NetworkNodes("net-a")
	if err != nil || len(nodes) != 1 {
		t.Fatalf("nodes = %+v, %v", nodes, err)
	}
	if len(nodes[0].Projects) != 2 || nodes[0].Projects[0] != "vision" {
		t.Fatalf("projects = %+v", nodes[0].Projects)
	}
	// A node that reports nothing must read back as an empty list, not null,
	// or every caller has to guard against it.
	if err := st.UpsertNetworkNode(NetworkNode{
		NetworkID: "net-a", NodeID: "n2", Name: "Laptop", LastSeen: Now(),
	}); err != nil {
		t.Fatal(err)
	}
	nodes, _ = st.NetworkNodes("net-a")
	for _, node := range nodes {
		if node.Projects == nil {
			t.Errorf("%s reported nil projects rather than none", node.Name)
		}
	}
}
