package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/config"
)

func repairableRayServer(t *testing.T) *Server {
	t.Helper()
	s, _ := newTestServer(t)
	s.cfg.ActiveNetwork = "net"
	s.cfg.Worker = config.WorkerConfig{Enabled: true, AllowJobs: true}
	s.cfg.Memberships = []config.MembershipConfig{{
		ID: "net", Enabled: true,
		Policy:  config.WorkerConfig{Enabled: true, AllowJobs: true},
		RayHead: "100.64.0.3:6379", RayHeadNode: "peer",
		RayHeadUpdated: "2026-09-09T20:00:00Z", RayHeadGeneration: 1,
	}}
	return s
}

func TestRayRepairStatusStartsReady(t *testing.T) {
	s := repairableRayServer(t)
	recorder := httptest.NewRecorder()
	s.getRayRepair(recorder, httptest.NewRequest(http.MethodGet, "/api/ray/repair?network_id=net", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status returned %d: %s", recorder.Code, recorder.Body.String())
	}
	var status rayRepairStatus
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.NetworkID != "net" || status.Stage != "Ready" || status.Running {
		t.Fatalf("unexpected initial repair status: %+v", status)
	}
}

func TestRayRepairDoesNotStartTwice(t *testing.T) {
	s := repairableRayServer(t)
	s.rayRepairs = map[string]rayRepairStatus{
		"net": {NetworkID: "net", Running: true, Percent: 30, Stage: "Installing Ray"},
	}
	recorder := httptest.NewRecorder()
	s.startRayRepair(recorder, httptest.NewRequest(http.MethodPost, "/api/ray/repair?network_id=net", nil))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("second start returned %d: %s", recorder.Code, recorder.Body.String())
	}
	var status rayRepairStatus
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Running || status.Percent != 30 {
		t.Fatalf("running repair was replaced: %+v", status)
	}
}
