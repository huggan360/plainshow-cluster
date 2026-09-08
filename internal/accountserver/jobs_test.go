package accountserver

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/ray"
)

func TestNetworkJobHistoryScopeAndDeletion(t *testing.T) {
	store, owner, friend := twoAccounts(t)
	if _, err := store.CheckIn(owner.ID, NodeCheckIn{ID: "n1", Name: "PC", Networks: []NetworkRef{{ID: "net-lab", Name: "Lab"}}}); err != nil {
		t.Fatal(err)
	}
	report := JobReport{NodeID: "n1", Jobs: []ray.Job{{ID: "j1", Status: "RUNNING", Entrypoint: "python train.py", StartedAt: 1234567890000}}}
	if changed, err := store.RecordNetworkJobs(owner.ID, "net-lab", report); err != nil || !changed {
		t.Fatalf("record %v %v", changed, err)
	}
	if changed, err := store.RecordNetworkJobs(owner.ID, "net-lab", report); err != nil || changed {
		t.Fatalf("duplicate rewrote history %v %v", changed, err)
	}
	if _, err := store.NetworkJobHistory(friend.ID, "net-lab", 0); !errors.Is(err, ErrNetworkMember) {
		t.Fatalf("outsider read %v", err)
	}
	if _, err := store.RecordNetworkJobs(friend.ID, "net-lab", report); !errors.Is(err, ErrNetworkMember) {
		t.Fatalf("outsider wrote %v", err)
	}
	if err := store.GrantNetworkMember(owner.ID, "net-lab", testKey, friend.ID, "member"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordNetworkJobs(friend.ID, "net-lab", report); !errors.Is(err, ErrNetworkMember) {
		t.Fatalf("friend spoofed owned node %v", err)
	}
	history, err := store.NetworkJobHistory(friend.ID, "net-lab", 0)
	if err != nil || history.Total != 1 || history.Jobs[0] != report.Jobs[0] {
		t.Fatalf("shared history %+v %v", history, err)
	}
	report.Jobs[0].Status = "SUCCEEDED"
	report.Jobs[0].EndedAt = 1234567891000
	if changed, err := store.RecordNetworkJobs(owner.ID, "net-lab", report); err != nil || !changed {
		t.Fatalf("finish %v %v", changed, err)
	}
	report.Jobs[0].Status = "RUNNING"
	report.Jobs[0].EndedAt = 0
	if changed, err := store.RecordNetworkJobs(owner.ID, "net-lab", report); err != nil || changed {
		t.Fatalf("stale snapshot regressed terminal state %v %v", changed, err)
	}
	if _, err := store.DeleteNetwork(owner.ID, "net-lab", testKey); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM ray_job_history`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("history survived deletion: %d %v", count, err)
	}
	if _, err := store.RecordNetworkJobs(owner.ID, "net-lab", report); !errors.Is(err, ErrNetworkMember) {
		t.Fatalf("late report recreated history %v", err)
	}
}

func TestJobHistoryPaginationAndValidation(t *testing.T) {
	store, owner, _ := twoAccounts(t)
	_, err := store.CheckIn(owner.ID, NodeCheckIn{ID: "n1", Name: "PC", Networks: []NetworkRef{{ID: "net-lab", Name: "Lab"}}})
	if err != nil {
		t.Fatal(err)
	}
	report := JobReport{NodeID: "n1"}
	for i := 0; i < 60; i++ {
		report.Jobs = append(report.Jobs, ray.Job{ID: fmt.Sprint(i), Status: "SUCCEEDED", StartedAt: int64(i + 1)})
	}
	if _, err := store.RecordNetworkJobs(owner.ID, "net-lab", report); err != nil {
		t.Fatal(err)
	}
	first, _ := store.NetworkJobHistory(owner.ID, "net-lab", 0)
	next, _ := store.NetworkJobHistory(owner.ID, "net-lab", 50)
	if len(first.Jobs) != 50 || len(next.Jobs) != 10 || first.Total != 60 || first.Jobs[0].ID != "59" || next.Jobs[0].ID != "9" {
		t.Fatalf("pages %+v %+v", first, next)
	}
	report.Jobs[0].Status = "made up"
	if _, err := store.RecordNetworkJobs(owner.ID, "net-lab", report); err == nil {
		t.Fatal("invalid status accepted")
	}
}

func TestJobHistoryKeepsReusedIDsAndPromotesPending(t *testing.T) {
	store, owner, _ := twoAccounts(t)
	if _, err := store.CheckIn(owner.ID, NodeCheckIn{ID: "n1", Name: "PC", Networks: []NetworkRef{{ID: "net-lab", Name: "Lab"}}}); err != nil {
		t.Fatal(err)
	}
	for _, job := range []ray.Job{
		{ID: "same", Status: "SUCCEEDED", StartedAt: 1000, EndedAt: 2000},
		{ID: "same", Status: "RUNNING", StartedAt: 3000},
		{ID: "pending", Status: "PENDING"},
		{ID: "pending", Status: "RUNNING", StartedAt: 4000},
		{ID: "pending", Status: "PENDING"}, // delayed undated observation
	} {
		if _, err := store.RecordNetworkJobs(owner.ID, "net-lab", JobReport{NodeID: "n1", Jobs: []ray.Job{job}}); err != nil {
			t.Fatal(err)
		}
	}
	history, err := store.NetworkJobHistory(owner.ID, "net-lab", 0)
	if err != nil || history.Total != 3 || history.Jobs[0].StartedAt != 4000 || history.Jobs[2].Status != "SUCCEEDED" {
		t.Fatalf("history %+v %v", history, err)
	}
}

func TestJobChangesWakeAllMembersOnly(t *testing.T) {
	store, owner, friend := twoAccounts(t)
	if err := store.GrantNetworkMember(owner.ID, "net-lab", testKey, friend.ID, "member"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CheckIn(owner.ID, NodeCheckIn{ID: "n1", Name: "PC", Networks: []NetworkRef{{ID: "net-lab", Name: "Lab"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession("jobs-token", owner.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	server := NewServer(&Config{}, store, nil)
	mine := server.watchers.add(owner.ID)
	theirs := server.watchers.add(friend.ID)
	outsider := server.watchers.add("outsider")
	defer server.watchers.remove(mine)
	defer server.watchers.remove(theirs)
	defer server.watchers.remove(outsider)
	body := `{"node_id":"n1","jobs":[{"id":"j1","status":"PENDING","entrypoint":"python train.py"}]}`
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest(http.MethodPost, "/api/networks/net-lab/jobs", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer jobs-token")
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("report HTTP %d %s", w.Code, w.Body.String())
		}
	}
	if len(mine.events) != 1 || len(theirs.events) != 1 || len(outsider.events) != 0 {
		t.Fatal("missing, duplicate or cross-network event")
	}
	if topic := <-theirs.events; topic != TopicJobs {
		t.Fatalf("topic %s", topic)
	}
}
