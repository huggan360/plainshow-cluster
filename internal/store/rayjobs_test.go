package store

import (
	"path/filepath"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/ray"
)

func TestRayJobOutboxSurvivesRestartAndNetworkDeletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAccount(Account{ID: "a", Username: "owner"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertNetwork(Network{ID: "n", Name: "Lab", OwnerAccountID: "a"}); err != nil {
		t.Fatal(err)
	}
	job := ray.Job{ID: "j", Status: "RUNNING", StartedAt: 1000}
	if err := st.QueueRayJobs("server/account", "n", []ray.Job{job}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	pending, err := st.PendingRayJobs("server/account", "n")
	if err != nil || len(pending) != 1 || pending[0] != job {
		t.Fatalf("pending %+v %v", pending, err)
	}
	if other, _ := st.PendingRayJobs("server/other", "n"); len(other) != 0 {
		t.Fatal("account switch leaked pending job")
	}
	finished := job
	finished.Status = "SUCCEEDED"
	finished.EndedAt = 2000
	if err := st.QueueRayJobs("server/account", "n", []ray.Job{finished}); err != nil {
		t.Fatal(err)
	}
	if err := st.AckRayJobs("server/account", "n", pending); err != nil {
		t.Fatal(err)
	}
	pending, _ = st.PendingRayJobs("server/account", "n")
	if len(pending) != 1 || pending[0] != finished {
		t.Fatal("acknowledging an old report deleted a newer result")
	}
	if err := st.QueueRayJobs("server/account", "n", []ray.Job{job}); err != nil {
		t.Fatal(err)
	}
	pending, _ = st.PendingRayJobs("server/account", "n")
	if len(pending) != 1 || pending[0] != finished {
		t.Fatal("older snapshot regressed terminal result")
	}
	if err := st.DeleteNetwork("n"); err != nil {
		t.Fatal(err)
	}
	pending, _ = st.PendingRayJobs("server/account", "n")
	if len(pending) != 0 {
		t.Fatal("network deletion retained queued history")
	}
	if err := st.QueueRayJobs("server/account", "n", []ray.Job{job}); err == nil {
		t.Fatal("late observation recreated deleted network queue")
	}
}
