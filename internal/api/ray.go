package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/accountclient"
	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/ray"
	"github.com/huggan360/plainshow-cluster/internal/store"
	"github.com/huggan360/plainshow-cluster/internal/sysinfo"
	"github.com/huggan360/plainshow-cluster/internal/tailnet"
)

// rayAnnouncement is the small piece of network state every peer converges
// on. An empty Head is a tombstone: without it, a machine returning after being
// offline could resurrect an obsolete head.
type rayAnnouncement struct {
	Head       string `json:"head"`
	NodeID     string `json:"node_id"`
	Updated    string `json:"updated"`
	Generation uint64 `json:"generation,omitempty"`
}

// localRayState records what the one Ray process on this machine is serving.
// Only the device's compute-assigned network may own that process at a time.
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
				NodeID: membership.RayHeadNode, Updated: membership.RayHeadUpdated,
				Generation: membership.RayHeadGeneration}
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
			NodeID: membership.RayHeadNode, Updated: membership.RayHeadUpdated,
			Generation: membership.RayHeadGeneration}
		if !rayAnnouncementNewer(next, current) {
			return false, nil
		}
		old := *membership
		membership.RayHead = next.Head
		membership.RayHeadNode = next.NodeID
		membership.RayHeadUpdated = next.Updated
		membership.RayHeadGeneration = next.Generation
		if err := config.Save(s.layout, s.cfg); err != nil {
			*membership = old
			return false, err
		}
		return true, nil
	}
	return false, errors.New("this device does not belong to that network")
}

func (s *Server) announceRayHead(networkID, head string) error {
	current := s.rayAnnouncement(networkID)
	_, err := s.storeRayAnnouncement(networkID, rayAnnouncement{
		Head: head, NodeID: s.cfg.Node.ID,
		Updated: time.Now().UTC().Format(time.RFC3339Nano), Generation: current.Generation + 1,
	})
	return err
}

// publishRayChanged is the browser-facing half of Ray gossip. The network id
// and global running state let every open client render the same switch as soon
// as the peer socket carries an announcement across the network.
func (s *Server) publishRayChanged(networkID string) {
	announcement := s.rayAnnouncement(networkID)
	s.hub.Publish("ray.changed", map[string]any{
		"network_id":  networkID,
		"head":        announcement.Head,
		"running":     announcement.Head != "",
		"local_error": s.rayReconcileError(networkID),
	})
}

func (s *Server) rayReconcileError(networkID string) string {
	s.rayHealthMu.RLock()
	defer s.rayHealthMu.RUnlock()
	return s.rayErrors[networkID]
}

// setRayReconcileError keeps automatic worker failures visible without writing
// the same retry error to the journal every fifteen seconds.
func (s *Server) setRayReconcileError(networkID, message string) {
	message = strings.TrimSpace(message)
	if len(message) > 2000 {
		message = message[len(message)-2000:]
	}
	s.rayHealthMu.Lock()
	if s.rayErrors == nil {
		s.rayErrors = make(map[string]string)
	}
	old := s.rayErrors[networkID]
	if message == "" {
		delete(s.rayErrors, networkID)
	} else {
		s.rayErrors[networkID] = message
	}
	s.rayHealthMu.Unlock()
	if old == message {
		return
	}
	if message != "" {
		log.Printf("ray: automatic join for network %s failed: %s", networkID, message)
	}
	s.publishRayChanged(networkID)
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
	// Generations are Lamport-style network revisions. Unlike wall clocks they
	// cannot make two machines retain different heads merely because one PC's
	// clock is ahead. Generation zero is the legacy timestamp-only format.
	if next.Generation > 0 || current.Generation > 0 {
		if next.Generation != current.Generation {
			return next.Generation > current.Generation
		}
		return next.NodeID+"\x00"+next.Head > current.NodeID+"\x00"+current.Head
	}
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
	networkID := s.rayNetworkID(r)
	announcement := s.rayAnnouncement(networkID)
	status := ray.Probe(r.Context(), ray.DashboardURL(hostOf(announcement.Head), ray.DefaultDashboard))
	policy, eligible := s.rayPolicy(networkID)
	local, _ := s.readLocalRayState()
	localError := s.rayReconcileError(networkID)
	repair := s.rayRepair(networkID)
	localAddress := tailnet.Probe(r.Context()).Self.Address
	response := map[string]any{
		"network_id":    networkID,
		"installed":     status.Installed,
		"running":       status.Running,
		"head":          announcement.Head,
		"head_node":     announcement.NodeID,
		"is_head":       announcement.Head != "" && announcement.NodeID == s.cfg.Node.ID,
		"nodes":         status.Nodes,
		"total_cpu":     status.TotalCPU,
		"total_gpu":     status.TotalGPU,
		"detail":        status.Detail,
		"eligible":      eligible,
		"policy":        policy,
		"local_running": local.NetworkID == networkID && status.HasAliveNode(localAddress),
		"local_error":   localError,
		"repair_needed": localError != "",
		"repair":        repair,
	}
	switch {
	case !status.Installed:
		response["advice"] = "Install Ray on this machine by re-running the Plainshow installer; it adds the managed runtime automatically."
	case !eligible:
		response["advice"] = "This machine is not accepting Ray work for this network. Enable the worker and job policy in Settings."
	case localError != "":
		response["advice"] = "This machine could not join Ray automatically: " + localError
	case announcement.Head == "":
		response["advice"] = "No Ray cluster is running for this network yet. Start it on any machine; the others attach automatically."
	case !status.Running:
		response["advice"] = "The network's Ray head is not answering. Its owner can restart it, or you can replace it from this machine."
	}
	writeJSON(w, http.StatusOK, response)
}

// rayNetworkID scopes Ray operations to the network named by the caller. The
// selected-network fallback is used for new runs. Logs and stop requests retain
// the original job's explicit network even after the user's selection changes.
func (s *Server) rayNetworkID(r *http.Request) string {
	if id := strings.TrimSpace(r.URL.Query().Get("network_id")); id != "" {
		return id
	}
	return s.cfg.ActiveNetwork
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
		Head      bool   `json:"head"`
		NetworkID string `json:"network_id"`
	}
	_ = decode(r, &body)

	s.rayActionMu.Lock()
	defer s.rayActionMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()

	networkID := strings.TrimSpace(body.NetworkID)
	if networkID == "" {
		networkID = s.rayNetworkID(r)
	}
	if networkID == "" {
		fail(w, http.StatusConflict, "Choose a network before starting Ray.")
		return
	}
	policy, eligible := s.rayPolicy(networkID)
	if !eligible {
		fail(w, http.StatusForbidden, "This machine is not accepting Ray work for this network. Enable its worker and job policy first.")
		return
	}
	address := tailnet.Probe(ctx).Self.Address
	if address == "" {
		fail(w, http.StatusConflict, "This machine has no private network address yet. Sign in and join the network first.")
		return
	}
	// Ray can run one local raylet without advertising the same CPU and GPU
	// twice. Record which network this device contributes to internally, while
	// the account and UI remain free to use every network concurrently.
	if s.cfg.ActiveNetwork != networkID {
		if !s.cfg.SetActiveNetwork(networkID) {
			fail(w, http.StatusNotFound, "This device does not belong to that network.")
			return
		}
		if err := config.Save(s.layout, s.cfg); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.hub.Publish("network.active", s.cfg.ActiveMembership())
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
		if err := ray.WaitForNode(ctx, ray.DashboardURL(address, ray.DefaultDashboard), address, 45*time.Second); err != nil {
			_ = stopLocalRay(ctx)
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
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
		s.publishRayChanged(networkID)
		writeJSON(w, http.StatusOK, map[string]any{"head": head, "role": "head"})
		return
	}

	if err := ray.StartWorker(ctx, address, announcement.Head, policy); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := ray.WaitForNode(ctx, ray.DashboardURL(hostOf(announcement.Head), ray.DefaultDashboard), address, 45*time.Second); err != nil {
		_ = stopLocalRay(ctx)
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := s.writeLocalRayState(localRayState{NetworkID: networkID, Head: announcement.Head, Role: "worker", Policy: policy}); err != nil {
		_ = stopLocalRay(ctx)
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.publishRayChanged(networkID)
	writeJSON(w, http.StatusOK, map[string]any{"head": announcement.Head, "role": "worker"})
}

// stopRay switches Ray off for the network. Any participating machine can
// publish the tombstone; peer sockets carry it to every node, whose reconciler
// then stops its local worker.
func (s *Server) stopRay(w http.ResponseWriter, r *http.Request) {
	s.rayActionMu.Lock()
	defer s.rayActionMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	networkID := s.rayNetworkID(r)
	if networkID == "" || !hasMembership(s.cfg, networkID) {
		fail(w, http.StatusForbidden, "This device does not belong to that network.")
		return
	}
	local, _ := s.readLocalRayState()
	if local.NetworkID == networkID {
		if err := ray.Stop(ctx); err != nil && !errors.Is(err, ray.ErrNotInstalled) {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		_ = s.clearLocalRayState()
	}
	announcement := s.rayAnnouncement(networkID)
	if announcement.Head != "" {
		if err := s.announceRayHead(networkID, ""); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.publishRayChanged(networkID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// StartRayReconciler restores Ray after a daemon or machine restart, attaches
// workers after gossip announces a head, and moves the one local Ray process
// when the device's compute assignment changes.
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
		s.setRayReconcileError(networkID, "")
		if state.NetworkID != "" {
			_ = stopLocalRay(ctx)
			_ = s.clearLocalRayState()
		}
		if !eligible && announcement.NodeID == s.cfg.Node.ID {
			_ = s.announceRayHead(networkID, "")
		}
		return
	}

	address := tailnet.Probe(ctx).Self.Address
	if address == "" {
		s.setRayReconcileError(networkID, "private network address is unavailable")
		return
	}
	if !ray.Installed(ctx) {
		s.setRayReconcileError(networkID, "managed Ray runtime is not installed")
		return
	}
	role := "worker"
	if announcement.NodeID == s.cfg.Node.ID {
		role = "head"
	}
	desired := localRayState{NetworkID: networkID, Head: announcement.Head, Role: role, Policy: policy}
	dashboard := ray.DashboardURL(hostOf(announcement.Head), ray.DefaultDashboard)
	if state == desired {
		// Do not tear down a healthy worker for one missed dashboard request.
		// Confirm the absence briefly; a dead raylet still recovers promptly.
		if ray.NodeAlive(ctx, dashboard, address) ||
			ray.WaitForNode(ctx, dashboard, address, 6*time.Second) == nil {
			s.setRayReconcileError(networkID, "")
			return
		}
	}
	// A failed `ray start` can leave processes without a state file. Always
	// clear the local runtime before retrying a missing raylet.
	if err := stopLocalRay(ctx); err != nil {
		s.setRayReconcileError(networkID, err.Error())
		return
	}
	_ = s.clearLocalRayState()
	var err error
	if role == "head" {
		err = ray.StartHead(ctx, address, ray.DefaultPort, ray.DefaultDashboard, policy)
	} else {
		err = ray.StartWorker(ctx, address, announcement.Head, policy)
	}
	if err == nil {
		err = ray.WaitForNode(ctx, dashboard, address, 45*time.Second)
	}
	if err == nil {
		s.setRayReconcileError(networkID, "")
		_ = s.writeLocalRayState(desired)
		s.publishRayChanged(networkID)
	} else {
		_ = stopLocalRay(ctx)
		_ = s.clearLocalRayState()
		s.setRayReconcileError(networkID, err.Error())
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
	// Availability is the quick switch in the corner; the rest is the durable
	// policy in Settings. Both have to say yes, and this one is checked here so
	// going offline actually removes the machine from Ray rather than only
	// changing a word on the screen.
	eligible := s.Available() && found && device.Enabled && device.AllowJobs &&
		network.Enabled && network.AllowJobs
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

// getRayJobs combines live Ray observations with this network's saved history.
// Execution remains Ray's; an unavailable head is not proof a job has failed.
func (s *Server) getRayJobs(w http.ResponseWriter, r *http.Request) {
	networkID := s.rayNetworkID(r)
	name := ""
	for _, membership := range s.cfg.Memberships {
		if membership.ID == networkID {
			name = membership.Name
			break
		}
	}
	if networkID == "" {
		writeJSON(w, 200, map[string]any{"jobs": []listedRayJob{}, "detail": "Choose a network on Networks to see its jobs."})
		return
	}
	if name == "" {
		fail(w, 403, "This device does not belong to that network.")
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("history_offset"))
	if offset < 0 {
		offset = 0
	}
	var live, history []ray.Job
	detail, historyError := "", ""
	running, total := false, 0
	historyReady := false
	// Read history concurrently with Ray: an unreachable head should not make
	// the account history wait for two sequential network timeouts.
	var historyDone = make(chan struct{})
	go func() {
		defer close(historyDone)
		if !s.usesCentralAccounts() {
			return
		}
		client, err := accountclient.New(s.cfg.Account.Server)
		token, tokenErr := config.LoadAccountToken(s.layout)
		if err != nil || tokenErr != nil || token == "" {
			historyError = "Sign in to load saved job history."
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		result, err := client.JobHistory(ctx, token, networkID, offset)
		if err != nil {
			historyError = "Saved job history is unavailable: " + err.Error()
			return
		}
		history, total = result.Jobs, result.Total
		historyReady = true
	}()
	if head := s.rayHead(networkID); head != "" {
		var err error
		live, err = ray.Jobs(r.Context(), ray.DashboardURL(hostOf(head), ray.DefaultDashboard))
		if err != nil {
			detail = "Cannot reach Ray. Saved entries show the last known status."
		} else {
			running = true
		}
	} else {
		detail = "Ray is offline. Saved entries show the last known status."
	}
	<-historyDone
	// Live queued/running jobs stay visible on every history page. Completed
	// jobs follow the account's page, rather than repeating all Ray results on
	// every page and making "Older history" meaningless.
	if historyReady && total > 0 {
		pageIDs := map[string]bool{}
		for _, job := range history {
			pageIDs[rayHistoryKey(job)] = true
		}
		filtered := make([]ray.Job, 0, len(live))
		for _, job := range live {
			if job.Running() || pageIDs[rayHistoryKey(job)] {
				filtered = append(filtered, job)
			}
		}
		live = filtered
	}
	items := mergeJobHistory(live, history, networkID, name)
	sort.Slice(items, func(i, j int) bool {
		if items[i].StartedAt == items[j].StartedAt {
			return items[i].ID > items[j].ID
		}
		return items[i].StartedAt > items[j].StartedAt
	})
	writeJSON(w, 200, map[string]any{"jobs": items, "network_id": networkID, "network_name": name, "running": running,
		"detail": detail, "history_error": historyError, "history_total": total, "history_offset": offset, "history_more": offset+50 < total})
}

func (s *Server) submitRayJob(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project   string `json:"project"`
		ProjectID string `json:"project_id"`
		NetworkID string `json:"network_id"`
		Command   string `json:"command"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	reference := strings.TrimSpace(body.ProjectID)
	if reference == "" {
		reference = strings.TrimSpace(body.Project)
	}
	var project store.Project
	var err error
	project, _, err = s.project(reference)
	if err != nil {
		if errors.Is(err, store.ErrAmbiguous) {
			fail(w, http.StatusConflict, "More than one project has that name. Use its project id.")
			return
		}
		fail(w, http.StatusNotFound, "No such project.")
		return
	}
	networkID, err := s.executionNetwork(strings.TrimSpace(body.NetworkID))
	if err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	head := s.rayHead(networkID)
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
	s.publishRayChanged(networkID)
	writeJSON(w, http.StatusAccepted, map[string]string{
		"id": jobID, "status": "submitted", "detail": output,
		"network_id": networkID,
	})
}

func (s *Server) rayJobLogs(w http.ResponseWriter, r *http.Request) {
	head := s.rayHead(s.rayNetworkID(r))
	logs, err := ray.JobLogs(r.Context(), ray.DashboardURL(hostOf(head), ray.DefaultDashboard),
		r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"logs": logs})
}

func (s *Server) stopRayJob(w http.ResponseWriter, r *http.Request) {
	head := s.rayHead(s.rayNetworkID(r))
	if err := ray.StopJob(r.Context(), ray.DashboardURL(hostOf(head), ray.DefaultDashboard),
		r.PathValue("id")); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	s.publishRayChanged(s.rayNetworkID(r))
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
}
