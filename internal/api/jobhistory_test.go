package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/ray"
)

func TestJobHistoryMergesWithoutRegressingOrInventingRunningJobs(t *testing.T) {
	live := []ray.Job{{ID: "same", Status: "RUNNING"}, {ID: "new", Status: "PENDING"}}
	history := []ray.Job{{ID: "same", Status: "SUCCEEDED"}, {ID: "old", Status: "RUNNING"}}
	merged := mergeJobHistory(live, history, "n", "Lab")
	if len(merged) != 3 || merged[0].Status != "SUCCEEDED" || merged[0].Archived || !merged[2].Archived || merged[2].Status != "RUNNING" {
		t.Fatalf("merged %+v", merged)
	}
	for _, job := range merged {
		if job.NetworkID != "n" || job.NetworkName != "Lab" {
			t.Fatal("lost network")
		}
	}
}

func TestJobHistoryLoadsSelectedNetworkWithoutRay(t *testing.T) {
	srv, _ := newTestServer(t)
	central := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("history request lost authentication")
		}
		if r.URL.Path != "/api/networks/chosen/jobs" || r.URL.Query().Get("offset") != "50" {
			t.Errorf("wrong network/page %s", r.URL)
		}
		writeJSON(w, 200, map[string]any{"jobs": []ray.Job{{ID: "saved", Status: "RUNNING", StartedAt: 1700000000000}}, "total": 70})
	}))
	defer central.Close()
	srv.cfg.Account.Server = central.URL
	srv.cfg.Memberships = []config.MembershipConfig{{ID: "chosen", Name: "Lab"}, {ID: "other", Name: "Other"}}
	srv.cfg.ActiveNetwork = "chosen"
	if err := config.SaveAccountToken(srv.layout, "test-token"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	srv.getRayJobs(w, httptest.NewRequest("GET", "/api/ray/jobs?history_offset=50", nil))
	var out struct {
		Jobs      []listedRayJob `json:"jobs"`
		Running   bool           `json:"running"`
		NetworkID string         `json:"network_id"`
		Total     int            `json:"history_total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(out.Jobs) != 1 || !out.Jobs[0].Archived || out.Running || out.NetworkID != "chosen" || out.Total != 70 {
		t.Fatalf("response %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	srv.getRayJobs(w, httptest.NewRequest("GET", "/api/ray/jobs?network_id=outsider", nil))
	if w.Code != 403 {
		t.Fatal("unjoined network accepted")
	}
	srv.cfg.ActiveNetwork = ""
	w = httptest.NewRecorder()
	srv.getRayJobs(w, httptest.NewRequest("GET", "/api/ray/jobs", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Choose a network") {
		t.Fatal("blank selection picked another network")
	}
}

func TestJobSummaryTruncationPreservesUTF8(t *testing.T) {
	text := trimJobText(strings.Repeat("界", 3000))
	if len(text) > 4096 || !utf8.ValidString(text) {
		t.Fatalf("invalid summary %d bytes", len(text))
	}
}

func TestJobHistoryDoesNotConfuseReusedIDs(t *testing.T) {
	merged := mergeJobHistory([]ray.Job{{ID: "same", Status: "RUNNING", StartedAt: 3000}},
		[]ray.Job{{ID: "same", Status: "SUCCEEDED", StartedAt: 1000}}, "n", "Lab")
	if len(merged) != 2 || merged[0].Status != "RUNNING" || !merged[1].Archived {
		t.Fatalf("different runs conflated: %+v", merged)
	}
}
