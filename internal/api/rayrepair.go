package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/ray"
)

type rayRepairStatus struct {
	NetworkID string `json:"network_id"`
	Running   bool   `json:"running"`
	Done      bool   `json:"done"`
	OK        bool   `json:"ok"`
	Percent   int    `json:"percent"`
	Stage     string `json:"stage"`
	Detail    string `json:"detail"`
	Error     string `json:"error,omitempty"`
	Updated   string `json:"updated_at"`
}

func (s *Server) rayRepair(networkID string) rayRepairStatus {
	s.rayRepairMu.RLock()
	defer s.rayRepairMu.RUnlock()
	return s.rayRepairs[networkID]
}

func (s *Server) updateRayRepair(status rayRepairStatus) {
	status.Updated = time.Now().UTC().Format(time.RFC3339Nano)
	s.rayRepairMu.Lock()
	if s.rayRepairs == nil {
		s.rayRepairs = make(map[string]rayRepairStatus)
	}
	s.rayRepairs[status.NetworkID] = status
	s.rayRepairMu.Unlock()
	s.hub.Publish("ray.repair", status)
}

func (s *Server) getRayRepair(w http.ResponseWriter, r *http.Request) {
	networkID := s.rayNetworkID(r)
	if networkID == "" || !hasMembership(s.cfg, networkID) {
		fail(w, http.StatusNotFound, "This device does not belong to that network.")
		return
	}
	status := s.rayRepair(networkID)
	if status.NetworkID == "" {
		status = rayRepairStatus{NetworkID: networkID, Percent: 0, Stage: "Ready",
			Detail: "Ready to check and repair this machine's managed Ray runtime."}
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) startRayRepair(w http.ResponseWriter, r *http.Request) {
	networkID := s.rayNetworkID(r)
	if networkID == "" || !hasMembership(s.cfg, networkID) {
		fail(w, http.StatusNotFound, "This device does not belong to that network.")
		return
	}
	if s.rayAnnouncement(networkID).Head == "" {
		fail(w, http.StatusConflict, "Turn Ray on for this network before repairing its connection.")
		return
	}
	if _, eligible := s.rayPolicy(networkID); !eligible {
		fail(w, http.StatusForbidden, "Enable this machine's work and network job policy before repairing Ray.")
		return
	}
	if current := s.rayRepair(networkID); current.Running {
		writeJSON(w, http.StatusAccepted, current)
		return
	}
	status := rayRepairStatus{NetworkID: networkID, Running: true, Percent: 5,
		Stage: "Preparing repair", Detail: "Stopping any incomplete local Ray process."}
	s.updateRayRepair(status)
	writeJSON(w, http.StatusAccepted, status)
	go s.runRayRepair(networkID)
}

func (s *Server) runRayRepair(networkID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	status := s.rayRepair(networkID)
	update := func(percent int, stage, detail string) {
		status.Percent, status.Stage, status.Detail = percent, stage, detail
		s.updateRayRepair(status)
	}
	failRepair := func(err error) {
		status.Running, status.Done, status.OK = false, true, false
		status.Stage, status.Error = "Repair failed", err.Error()
		status.Detail = "Nothing outside Plainshow's managed Ray environment was changed."
		s.updateRayRepair(status)
	}

	s.rayActionMu.Lock()
	if err := stopLocalRay(ctx); err != nil {
		s.rayActionMu.Unlock()
		failRepair(fmt.Errorf("could not stop the incomplete Ray process: %w", err))
		return
	}
	_ = s.clearLocalRayState()
	if err := ray.RepairManaged(ctx, update); err != nil {
		s.rayActionMu.Unlock()
		failRepair(err)
		return
	}
	s.rayActionMu.Unlock()

	update(78, "Joining network", "Starting this machine against the network's current Ray head.")
	s.reconcileRay(ctx)
	if errText := s.rayReconcileError(networkID); errText != "" {
		failRepair(fmt.Errorf("automatic Ray join failed: %s", errText))
		return
	}
	local, _ := s.readLocalRayState()
	if local.NetworkID != networkID || !ray.RunningLocal(ctx) {
		failRepair(fmt.Errorf("Ray did not report a running local node after repair"))
		return
	}
	status.Running, status.Done, status.OK = false, true, true
	status.Percent, status.Stage = 100, "Ray repaired"
	status.Detail, status.Error = "This machine joined the Ray cluster automatically.", ""
	s.updateRayRepair(status)
	s.publishRayChanged(networkID)
}
