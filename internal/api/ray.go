package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/ray"
	"github.com/huggan360/plainshow-cluster/internal/sysinfo"
	"github.com/huggan360/plainshow-cluster/internal/tailnet"
)

// rayAnnouncement is the small piece of network state every peer converges
// on. An empty Head is a tombstone: without it, a machine returning after being
// offline could resurrect an obsolete head.
type rayAnnouncement struct {
	Head    string `json:"head"`
	NodeID  string `json:"node_id"`
	Updated string `json:"updated"`
}

// localRayState records what the one Ray process on this machine is serving.
// Only the selected Plainshow network may own that process at a time.
type localRayState struct {
	NetworkID string             `json:"network_id"`
	Head      string             `json:"head"`
	Role      string             `json:"role"`
	Policy    ray.ResourcePolicy `json:"policy"`
}

func (s *Server) rayAnnouncement(networkID string) rayAnnouncement {
	s.rayMu.RLock()
	defer s.rayMu.RUnlock()
	for _, membership := range s.cfg.Memberships {
		if membership.ID == networkID {
			return rayAnnouncement{Head: membership.RayHead,
				NodeID: membership.RayHeadNode, Updated: membership.RayHeadUpdated}
		}
	}
	return rayAnnouncement{}
}

func (s *Server) rayHead(networkID string) string {
	return s.rayAnnouncement(networkID).Head
}

// storeRayAnnouncement records a newer announcement. Equal timestamps use a
// deterministic tie-break so simultaneous starts converge even if two clocks
// happen to produce the same value.
func (s *Server) storeRayAnnouncement(networkID string, next rayAnnouncement) (bool, error) {
	if !validRayAnnouncement(next) {
		return false, errors.New("invalid Ray head announcement")
	}
	s.rayMu.Lock()
	defer s.rayMu.Unlock()
	for i := range s.cfg.Memberships {
		membership := &s.cfg.Memberships[i]
		if membership.ID != networkID {
			continue
		}
		current := rayAnnouncement{Head: membership.RayHead,
			NodeID: membership.RayHeadNode, Updated: membership.RayHeadUpdated}
		if !rayAnnouncementNewer(next, current) {
			return false, nil
		}
		old := *membership
		membership.RayHead = next.Head
		membership.RayHeadNode = next.NodeID
		membership.RayHeadUpdated = next.Updated
		if err := config.Save(s.layout, s.cfg); err != nil {
			*membership = old
			return false, err
		}
		return true, nil
	}
	return false, errors.New("this device does not belong to that network")
}

func (s *Server) announceRayHead(networkID, head string) error {
	_, err := s.storeRayAnnouncement(networkID, rayAnnouncement{
		Head: head, NodeID: s.cfg.Node.ID,
		Updated: time.Now().UTC().Format(time.RFC3339Nano),
	})
	return err
}

func validRayAnnouncement(announcement rayAnnouncement) bool {
	if announcement.NodeID == "" || announcement.Updated == "" {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, announcement.Updated); err != nil {
		return false
	}
	if announcement.Head == "" {
		return true
	}
	host, portText, err := net.SplitHostPort(announcement.Head)
	if err != nil || strings.TrimSpace(host) == "" {
		return false
	}
	port, err := strconv.Atoi(portText)
	return err == nil && port > 0 && port <= 65535
}

func rayAnnouncementNewer(next, current rayAnnouncement) bool {
	nextTime, err := time.Parse(time.RFC3339Nano, next.Updated)
	if err != nil {
		return false
	}
	currentTime, err := time.Parse(time.RFC3339Nano, current.Updated)
	if err != nil || nextTime.After(currentTime) {
		return true
	}
	if nextTime.Before(currentTime) {
		return false
	}
	return next.NodeID+"\x00"+next.Head > current.NodeID+"\x00"+current.Head
}

// getRay reports the state of this network's Ray cluster.
func (s *Server) getRay(w http.ResponseWriter, r *http.Request) {
	announcement := s.rayAnnouncement(s.cfg.ActiveNetwork)
	status := ray.Probe(r.Context(), ray.DashboardURL(hostOf(announcement.Head), ray.DefaultDashboard))
	policy, eligible := s.rayPolicy(s.cfg.ActiveNetwork)
	response := map[string]any{
		"installed": status.Installed,
		"running":   status.Running,
		"head":      announcement.Head,
		"head_node": announcement.NodeID,
		"is_head":   announcement.Head != "" && announcement.NodeID == s.cfg.Node.ID,
		"nodes":     status.Nodes,
		"total_cpu": status.TotalCPU,
		"total_gpu": status.TotalGPU,
		"detail":    status.Detail,
		"eligible":  eligible,
		"policy":    policy,
	}
	switch {
	case !status.Installed:
		response["advice"] = "Install Ray on this machine by re-running the Plainshow installer; it adds the managed runtime automatically."
	case !eligible:
		response["advice"] = "This machine is not accepting Ray work in the selected network. Enable the worker and job policy in Settings."
	case announcement.Head == "":
		response["advice"] = "No Ray cluster is running for this network yet. Start it on any machine; the others attach automatically."
	case !status.Running:
		response["advice"] = "The network's Ray head is not answering. Its owner can restart it, or you can replace it from this machine."
	}
	writeJSON(w, http.StatusOK, response)
}

func hostOf(address string) string {
	if address == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(address)
	if err == nil {
		return host
	}
	return address
}

// startRay makes this machine the network head when requested (or when none
// exists), otherwise attaches it to the head already announced by the peers.
func (s *Server) startRay(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Head bool `json:"head"`
	}
	_ = decode(r, &body)

	s.rayActionMu.Lock()
	defer s.rayActionMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()

	networkID := s.cfg.ActiveNetwork
	if networkID == "" {
		fail(w, http.StatusConflict, "Select a network before starting Ray.")
		return
	}
	policy, eligible := s.rayPolicy(networkID)
	if !eligible {
		fail(w, http.StatusForbidden, "This machine is not accepting Ray work in the selected network. Enable its worker and job policy first.")
		return
	}
	address := tailnet.Probe(ctx).Self.Address
	if address == "" {
		fail(w, http.StatusConflict, "This machine has no private network address yet. Sign in and join the network first.")
		return
	}

	announcement := s.rayAnnouncement(networkID)
	if err := stopLocalRay(ctx); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = s.clearLocalRayState()
	if body.Head || announcement.Head == "" || announcement.NodeID == s.cfg.Node.ID {
		if err := ray.StartHead(ctx, address, ray.DefaultPort, ray.DefaultDashboard, policy); err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		head := net.JoinHostPort(address, strconv.Itoa(ray.DefaultPort))
		if err := s.announceRayHead(networkID, head); err != nil {
			_ = stopLocalRay(ctx)
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := s.writeLocalRayState(localRayState{NetworkID: networkID, Head: head, Role: "head", Policy: policy}); err != nil {
			_ = stopLocalRay(ctx)
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.hub.Publish("ray.changed", map[string]any{"head": head})
		writeJSON(w, http.StatusOK, map[string]any{"head": head, "role": "head"})
		return
	}

	if err := ray.StartWorker(ctx, address, announcement.Head, policy); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := s.writeLocalRayState(localRayState{NetworkID: networkID, Head: announcement.Head, Role: "worker", Policy: policy}); err != nil {
		_ = stopLocalRay(ctx)
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.hub.Publish("ray.changed", map[string]any{"head": announcement.Head})
	writeJSON(w, http.StatusOK, map[string]any{"head": announcement.Head, "role": "worker"})
}

// stopRay takes this machine out of Ray. Stopping the head publishes a
// tombstone so peers do not keep trying to attach to it.
func (s *Server) stopRay(w http.ResponseWriter, r *http.Request) {
	s.rayActionMu.Lock()
	defer s.rayActionMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	if err := ray.Stop(ctx); err != nil && !errors.Is(err, ray.ErrNotInstalled) {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = s.clearLocalRayState()
	announcement := s.rayAnnouncement(s.cfg.ActiveNetwork)
	if announcement.NodeID == s.cfg.Node.ID {
		if err := s.announceRayHead(s.cfg.ActiveNetwork, ""); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		announcement = s.rayAnnouncement(s.cfg.ActiveNetwork)
	}
	s.hub.Publish("ray.changed", map[string]any{"head": announcement.Head})
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// StartRayReconciler restores Ray after a daemon or machine restart, attaches
// workers after gossip announces a head, and moves the one local Ray process
// when the user changes the active Plainshow network.
func (s *Server) StartRayReconciler(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 15 * time.Second
	}
	go func() {
		s.reconcileRay(ctx)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reconcileRay(ctx)
			}
		}
	}()
}

func (s *Server) reconcileRay(parent context.Context) {
	s.rayActionMu.Lock()
	defer s.rayActionMu.Unlock()
	ctx, cancel := context.WithTimeout(parent, 4*time.Minute)
	defer cancel()

	networkID := s.cfg.ActiveNetwork
	announcement := s.rayAnnouncement(networkID)
	state, _ := s.readLocalRayState()
	policy, eligible := s.rayPolicy(networkID)
	if networkID == "" || announcement.Head == "" || !eligible {
		if state.NetworkID != "" || ray.RunningLocal(ctx) {
			_ = stopLocalRay(ctx)
			_ = s.clearLocalRayState()
		}
		if !eligible && announcement.NodeID == s.cfg.Node.ID {
			_ = s.announceRayHead(networkID, "")
		}
		return
	}

	address := tailnet.Probe(ctx).Self.Address
	if address == "" || !ray.Installed(ctx) {
		return
	}
	role := "worker"
	if announcement.NodeID == s.cfg.Node.ID {
		role = "head"
	}
	desired := localRayState{NetworkID: networkID, Head: announcement.Head, Role: role, Policy: policy}
	if state == desired && ray.RunningLocal(ctx) {
		return
	}
	if state.NetworkID != "" || ray.RunningLocal(ctx) {
		_ = stopLocalRay(ctx)
		_ = s.clearLocalRayState()
	}
	var err error
	if role == "head" {
		err = ray.StartHead(ctx, address, ray.DefaultPort, ray.DefaultDashboard, policy)
	} else {
		err = ray.StartWorker(ctx, address, announcement.Head, policy)
	}
	if err == nil {
		_ = s.writeLocalRayState(desired)
		s.hub.Publish("ray.changed", map[string]any{"head": announcement.Head})
	}
}

func (s *Server) rayPolicy(networkID string) (ray.ResourcePolicy, bool) {
	device := s.cfg.Worker
	network := config.WorkerConfig{}
	found := false
	for _, membership := range s.cfg.Memberships {
		if membership.ID == networkID {
			network, found = membership.Policy, membership.Enabled
			break
		}
	}
	eligible := found && device.Enabled && device.AllowJobs && network.Enabled && network.AllowJobs
	policy := ray.ResourcePolicy{
		MaxCPU:    minNonzero(device.MaxCPU, network.MaxCPU),
		MaxRAMMB:  minNonzero(device.MaxRAMMB, network.MaxRAMMB),
		AllowGPU:  device.AllowGPU && network.AllowGPU,
		GPUsKnown: true,
	}
	if policy.AllowGPU {
		addGPUInventory(&policy, sysinfo.Probe(s.layout.Root).GPUs)
	}
	return policy, eligible
}

func addGPUInventory(policy *ray.ResourcePolicy, gpus []sysinfo.GPU) {
	for _, gpu := range gpus {
		if !gpu.Trainable {
			continue
		}
		policy.GPUCount++
		switch gpu.Vendor {
		case "nvidia":
			policy.NVIDIAGPUCount++
		case "amd":
			policy.AMDGPUCount++
		case "intel":
			policy.IntelGPUCount++
		}
	}
}

func minNonzero(a, b int) int {
	if a == 0 || (b > 0 && b < a) {
		return b
	}
	return a
}

func stopLocalRay(ctx context.Context) error {
	err := ray.Stop(ctx)
	if errors.Is(err, ray.ErrNotInstalled) {
		return nil
	}
	return err
}

func (s *Server) readLocalRayState() (localRayState, error) {
	raw, err := os.ReadFile(s.layout.RayState())
	if errors.Is(err, os.ErrNotExist) {
		return localRayState{}, nil
	}
	if err != nil {
		return localRayState{}, err
	}
	var state localRayState
	if err := json.Unmarshal(raw, &state); err != nil {
		return localRayState{}, err
	}
	return state, nil
}

func (s *Server) writeLocalRayState(state localRayState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	temporary := s.layout.RayState() + ".tmp"
	if err := os.WriteFile(temporary, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, s.layout.RayState()); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func (s *Server) clearLocalRayState() error {
	err := os.Remove(s.layout.RayState())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// getRayJobs lists what Ray is running. These are Ray's jobs, as Ray reports
// them; Plainshow keeps no parallel job model for work Ray owns.
func (s *Server) getRayJobs(w http.ResponseWriter, r *http.Request) {
	head := s.rayHead(s.cfg.ActiveNetwork)
	if head == "" {
		writeJSON(w, http.StatusOK, map[string]any{"jobs": []any{}, "running": false})
		return
	}
	jobs, err := ray.Jobs(r.Context(), ray.DashboardURL(hostOf(head), ray.DefaultDashboard))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"jobs": []any{}, "running": false, "detail": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "running": true})
}

func (s *Server) submitRayJob(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project string `json:"project"`
		Command string `json:"command"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	project, _, err := s.project(strings.TrimSpace(body.Project))
	if err != nil {
		fail(w, http.StatusNotFound, "No such project in the active network.")
		return
	}
	head := s.rayHead(s.cfg.ActiveNetwork)
	if head == "" {
		fail(w, http.StatusConflict, "Start Ray for this network before running a project.")
		return
	}
	jobID := "plainshow_" + strings.ReplaceAll(config.NewID(), "-", "")
	output, err := ray.Submit(r.Context(), ray.DashboardURL(hostOf(head), ray.DefaultDashboard),
		s.projectDir(project), strings.TrimSpace(body.Command), jobID)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	s.hub.Publish("ray.changed", map[string]any{"head": head})
	writeJSON(w, http.StatusAccepted, map[string]string{
		"id": jobID, "status": "submitted", "detail": output,
	})
}

func (s *Server) rayJobLogs(w http.ResponseWriter, r *http.Request) {
	head := s.rayHead(s.cfg.ActiveNetwork)
	logs, err := ray.JobLogs(r.Context(), ray.DashboardURL(hostOf(head), ray.DefaultDashboard),
		r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"logs": logs})
}

func (s *Server) stopRayJob(w http.ResponseWriter, r *http.Request) {
	head := s.rayHead(s.cfg.ActiveNetwork)
	if err := ray.StopJob(r.Context(), ray.DashboardURL(hostOf(head), ray.DefaultDashboard),
		r.PathValue("id")); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	s.hub.Publish("ray.changed", map[string]any{"head": head})
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
}
