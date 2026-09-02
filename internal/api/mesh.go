package api

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/accountclient"
	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/jobs"
	"github.com/huggan360/plainshow-cluster/internal/mesh"
	"github.com/huggan360/plainshow-cluster/internal/store"
	"github.com/huggan360/plainshow-cluster/internal/sysinfo"
	"github.com/huggan360/plainshow-cluster/internal/tailnet"
)

// ListenAndServeMesh starts the encrypted peer port.
//
// It opens nothing while this machine is alone. A single-machine install has no
// peers to talk to, so a port on every interface would be surface with no
// purpose — and most installs stay that way. The listener starts when the node
// first shares a network with somebody, which is also when it is first needed.
func (s *Server) ListenAndServeMesh(ctx context.Context, certificate tls.Certificate) error {
	// Wait rather than give up. Creating a join code on a running node has to
	// open the port, and checking only at startup would leave the first invite
	// on a fresh install unable to be redeemed.
	if err := s.waitUntilMeshNeeded(ctx); err != nil {
		return nil
	}
	port := s.cfg.Network.PeerPort
	if port == 0 {
		port = 10000
	}
	bind := s.cfg.Network.PeerBind
	if bind == "" {
		bind = "0.0.0.0"
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("mesh port %d is not available: %w", port, err)
	}
	s.cfg.Network.PeerPort = port
	server := &http.Server{Handler: s.MeshHandler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	err = server.Serve(tls.NewListener(listener, mesh.TLSConfig(certificate)))
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// meshNeeded reports whether any network this node belongs to has another
// machine in it, or is expecting one.
//
// It fails open: if the question cannot be answered the listener starts, since
// a node that silently will not accept peers is worse than an open port.
// waitUntilMeshNeeded blocks until this node has a peer or expects one.
//
// It returns an error only when the node is shutting down. The poll is cheap
// (two indexed counts) and stops for good the moment it succeeds, so this costs
// nothing once a cluster has more than one machine.
func (s *Server) waitUntilMeshNeeded(ctx context.Context) error {
	if s.meshNeeded() {
		return nil
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if s.meshNeeded() {
				return nil
			}
		}
	}
}

func (s *Server) meshNeeded() bool {
	for _, membership := range s.cfg.Memberships {
		nodes, err := s.store.NetworkNodes(membership.ID)
		if err != nil {
			return true
		}
		if len(nodes) > 1 {
			return true
		}
		pending, err := s.store.HasPendingInvitation(membership.ID)
		if err != nil || pending {
			return true
		}
	}
	return false
}

type joinRequest struct {
	Token        string         `json:"token"`
	NodeID       string         `json:"node_id"`
	Name         string         `json:"name"`
	PublicKey    string         `json:"public_key"`
	Fingerprint  string         `json:"fingerprint"`
	Endpoint     string         `json:"endpoint"`
	Account      store.Account  `json:"account"`
	AccountToken string         `json:"account_token"`
	Info         sysinfo.Info   `json:"info"`
	Policy       map[string]any `json:"policy"`
}

type joinResponse struct {
	Network store.Network       `json:"network"`
	Role    string              `json:"role"`
	Nodes   []store.NetworkNode `json:"nodes"`
}

type peerExchange struct {
	Nodes       []store.NetworkNode       `json:"nodes"`
	Controllers []store.NetworkController `json:"controllers,omitempty"`
}

// MeshHandler is the deliberately narrow API exposed on the encrypted peer
// port. The browser interface and local administration API are never exposed
// there.
func (s *Server) MeshHandler() http.Handler {
	root := http.NewServeMux()
	root.HandleFunc("POST /mesh/v1/join/{network}", s.acceptJoin)
	root.HandleFunc("POST /mesh/v1/controllers/join/{network}", s.acceptControllerJoin)
	authed := http.NewServeMux()
	authed.HandleFunc("POST /mesh/v1/peers/check-in", s.acceptPeerCheckIn)
	authed.HandleFunc("POST /mesh/v1/jobs", s.acceptRemoteJob)
	authed.HandleFunc("POST /mesh/v1/datasets/sync", s.acceptDatasetSync)
	authed.HandleFunc("POST /mesh/v1/reach", s.acceptReachCheck)
	authed.HandleFunc("GET /mesh/v1/jobs/{id}", s.remoteJob)
	authed.HandleFunc("GET /mesh/v1/jobs/{id}/logs", s.remoteJobLogs)
	authed.HandleFunc("POST /mesh/v1/jobs/{id}/stop", s.remoteJobStop)
	authed.HandleFunc("POST /mesh/v1/jobs/{id}/input", s.remoteJobInput)
	authed.HandleFunc("GET /mesh/v1/ping", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"node_id": s.cfg.Node.ID, "time": store.Now()})
	})
	root.Handle("/mesh/v1/", mesh.Authenticate(authed, s.meshPublicKey))
	return root
}

func (s *Server) meshPublicKey(networkID, deviceID string) (ed25519.PublicKey, error) {
	node, err := s.store.NetworkNode(networkID, deviceID)
	if err != nil {
		return nil, err
	}
	raw, err := base64.RawURLEncoding.DecodeString(node.PublicKey)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("invalid enrolled public key")
	}
	return ed25519.PublicKey(raw), nil
}

func (s *Server) acceptJoin(w http.ResponseWriter, r *http.Request) {
	networkID := r.PathValue("network")
	var body joinRequest
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	invitation, err := s.store.ConsumeInvitation(networkID, mesh.TokenHash(body.Token))
	if err != nil {
		fail(w, 403, "That join code is invalid, expired, or has already been used.")
		return
	}
	public, err := base64.RawURLEncoding.DecodeString(body.PublicKey)
	if err != nil || len(public) != ed25519.PublicKeySize || body.NodeID == "" || body.Name == "" {
		fail(w, 400, "The joining device supplied an invalid identity.")
		return
	}
	if s.usesCentralAccounts() {
		client, clientErr := accountclient.New(s.cfg.Account.Server)
		if clientErr != nil {
			fail(w, 500, clientErr.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		central, authErr := client.Session(ctx, body.AccountToken)
		if authErr != nil || central.ID != body.Account.ID {
			fail(w, 403, "The joining device could not prove its Plainshow account.")
			return
		}
		body.Account = centralAccount(central)
	} else if body.Account.ID == "" || body.Account.Username == "" {
		// Older nodes used their device key as the local account identity.
		body.Account = store.Account{ID: body.NodeID, Username: body.Name,
			DisplayName: body.Name, PublicKey: body.PublicKey}
	}
	if err := s.store.UpsertAccount(body.Account); err != nil {
		fail(w, 409, "That device name is already used by another account.")
		return
	}
	accountRole := invitation.Role
	if existing, memberErr := s.store.NetworkMember(networkID, body.Account.ID); memberErr == nil {
		// Enrolling another device must not demote an account that already owns
		// or administers the network.
		accountRole = existing.Role
	} else if !errors.Is(memberErr, store.ErrNotFound) {
		fail(w, 500, memberErr.Error())
		return
	} else if err := s.store.AddNetworkMember(networkID, body.Account.ID, accountRole); err != nil {
		fail(w, 500, err.Error())
		return
	}
	if err := s.store.UpsertNetworkNode(store.NetworkNode{NetworkID: networkID, NodeID: body.NodeID,
		Name: body.Name, Roles: []string{string(config.RoleWorker)}, OS: body.Info.OS, Arch: body.Info.Arch,
		PublicKey: body.PublicKey, Fingerprint: body.Fingerprint, Address: body.Endpoint,
		Policy: body.Policy, Capacity: map[string]any{"cpu_cores": body.Info.CPUCores,
			"ram_total_mb": body.Info.RAMTotalMB, "disk_total_gb": body.Info.DiskTotalGB,
			"gpus": body.Info.GPUs}, LastSeen: store.Now()}); err != nil {
		fail(w, 500, err.Error())
		return
	}
	network, err := s.store.NetworkByID(networkID)
	if err != nil {
		fail(w, 404, "No such network.")
		return
	}
	nodes, _ := s.store.NetworkNodes(networkID)
	s.hub.Publish("networks.changed", network)
	writeJSON(w, 201, joinResponse{Network: network, Role: accountRole, Nodes: nodes})
}

// StartPeerDiscovery periodically exchanges each network's directory with
// every reachable peer. There is no coordinator: any live edge carries new
// device records across the network, and repeated exchanges converge after an
// offline machine returns.
func (s *Server) StartPeerDiscovery(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 30 * time.Second
	}
	go func() {
		s.syncPeersOnce(ctx)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.syncPeersOnce(ctx)
			}
		}
	}()
}

func (s *Server) syncPeersOnce(ctx context.Context) {
	networks, err := s.store.Networks(s.cfg.AccountID())
	if err != nil {
		return
	}
	for _, network := range networks {
		if ctx.Err() != nil {
			return
		}
		s.syncNetworkPeers(ctx, network.ID)
	}
}

func (s *Server) syncNetworkPeers(ctx context.Context, networkID string) {
	_ = s.store.TouchNetworkNode(networkID, s.cfg.Node.ID, store.Now())
	nodes, err := s.store.NetworkNodes(networkID)
	if err != nil {
		return
	}
	controllers, _ := s.store.NetworkControllers(networkID)
	request := peerExchange{Nodes: nodes, Controllers: controllers}
	var wg sync.WaitGroup
	for _, node := range nodes {
		if node.NodeID == s.cfg.Node.ID || node.Address == "" || node.Fingerprint == "" {
			continue
		}
		wg.Add(1)
		go func(node store.NetworkNode) {
			defer wg.Done()
			if ctx.Err() != nil {
				return
			}
			client, err := s.clientForNode(networkID, node.NodeID)
			if err != nil {
				return
			}
			var response peerExchange
			if err := client.JSON("POST", "/mesh/v1/peers/check-in", request, &response, true); err != nil {
				return
			}
			s.mergePeerNodes(networkID, response.Nodes)
			s.mergeControllers(networkID, response.Controllers)
		}(node)
	}
	wg.Wait()
}

func (s *Server) acceptPeerCheckIn(w http.ResponseWriter, r *http.Request) {
	networkID := r.Header.Get("X-Plainshow-Network")
	if !hasMembership(s.cfg, networkID) {
		fail(w, 403, "This device is not active in that network.")
		return
	}
	var exchange peerExchange
	if err := decode(r, &exchange); err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.mergePeerNodes(networkID, exchange.Nodes)
	s.mergeControllers(networkID, exchange.Controllers)
	_ = s.store.TouchNetworkNode(networkID, s.cfg.Node.ID, store.Now())
	nodes, err := s.store.NetworkNodes(networkID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	controllers, _ := s.store.NetworkControllers(networkID)
	writeJSON(w, 200, peerExchange{Nodes: nodes, Controllers: controllers})
}

func (s *Server) mergeControllers(networkID string, items []store.NetworkController) {
	for _, item := range items {
		if item.NetworkID != networkID || item.ID == "" || item.PublicKey == "" ||
			item.Fingerprint == "" || item.Address == "" || item.CollabToken == "" {
			continue
		}
		_ = s.store.UpsertNetworkController(item)
	}
}

// mergePeerNodes accepts only complete, newer records and never lets gossip
// overwrite this machine's own row. A peer may have been offline for days, so
// arrival order cannot be used as freshness.
func (s *Server) mergePeerNodes(networkID string, candidates []store.NetworkNode) {
	current, err := s.store.NetworkNodes(networkID)
	if err != nil {
		return
	}
	known := make(map[string]store.NetworkNode, len(current))
	for _, node := range current {
		known[node.NodeID] = node
	}
	for _, candidate := range candidates {
		if candidate.NetworkID != networkID || candidate.NodeID == s.cfg.Node.ID || !validPeerRecord(candidate) {
			continue
		}
		if old, ok := known[candidate.NodeID]; ok && !nodeRecordNewer(candidate.LastSeen, old.LastSeen) {
			continue
		}
		candidate.Roles = []string{string(config.RoleWorker)}
		candidate.IsSelf = false
		if s.store.UpsertNetworkNode(candidate) == nil {
			known[candidate.NodeID] = candidate
		}
	}
}

func validPeerRecord(node store.NetworkNode) bool {
	if node.NodeID == "" || strings.TrimSpace(node.Name) == "" || node.LastSeen == "" {
		return false
	}
	if _, err := time.Parse(time.RFC3339, node.LastSeen); err != nil {
		return false
	}
	public, err := base64.RawURLEncoding.DecodeString(node.PublicKey)
	if err != nil || len(public) != ed25519.PublicKeySize {
		return false
	}
	fingerprint, err := base64.RawURLEncoding.DecodeString(node.Fingerprint)
	if err != nil || len(fingerprint) != 32 {
		return false
	}
	endpoint, err := url.Parse(node.Address)
	return err == nil && endpoint.Scheme == "https" && endpoint.Host != ""
}

func nodeRecordNewer(candidate, current string) bool {
	next, err := time.Parse(time.RFC3339, candidate)
	if err != nil {
		return false
	}
	previous, err := time.Parse(time.RFC3339, current)
	return err != nil || next.After(previous)
}

type remoteJobRequest struct {
	ProjectID   string            `json:"project_id"`
	Project     string            `json:"project"`
	Description string            `json:"description"`
	Kind        string            `json:"kind"`
	Title       string            `json:"title"`
	Command     string            `json:"command"`
	Archive     []byte            `json:"archive"`
	Environment map[string]string `json:"environment"`
}

func (s *Server) acceptRemoteJob(w http.ResponseWriter, r *http.Request) {
	networkID := r.Header.Get("X-Plainshow-Network")
	if !hasMembership(s.cfg, networkID) {
		fail(w, 403, "This worker is not active in that network.")
		return
	}
	var body remoteJobRequest
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(body.Command) == "" ||
		(body.Kind != "terminal" && (body.ProjectID == "" || body.Project == "")) {
		fail(w, 400, "The remote job is missing its project or command.")
		return
	}
	project := store.Project{ID: body.ProjectID, NetworkID: networkID, Name: body.Project}
	dir := s.layout.Root
	if body.Project != "" {
		var err error
		project, err = s.store.ProjectByNameInNetwork(networkID, body.Project)
		if errors.Is(err, store.ErrNotFound) {
			project = store.Project{ID: body.ProjectID, NetworkID: networkID, Name: body.Project, Description: body.Description}
			if err = s.store.CreateProject(&project); err != nil {
				fail(w, 500, err.Error())
				return
			}
		} else if err != nil {
			fail(w, 500, err.Error())
			return
		}
		dir = filepath.Join(s.layout.Projects(), networkID, body.Project)
		if err := os.RemoveAll(dir); err != nil {
			fail(w, 500, err.Error())
			return
		}
		if err := mesh.ExtractArchive(body.Archive, dir); err != nil {
			fail(w, 400, "Could not materialise project: "+err.Error())
			return
		}
	}
	job, err := s.sup.Start(jobs.Request{ProjectID: project.ID, Project: project.Name,
		Kind: body.Kind, Title: body.Title, Command: body.Command, Workdir: dir, Env: body.Environment})
	if err != nil {
		var policy jobs.ErrPolicy
		if errors.As(err, &policy) {
			fail(w, 403, policy.Reason)
		} else {
			fail(w, 500, err.Error())
		}
		return
	}
	writeJSON(w, 201, job)
}

type datasetSyncRequest struct {
	Dataset  store.Dataset     `json:"dataset"`
	Manifest string            `json:"manifest"`
	Chunks   map[string][]byte `json:"chunks"`
}

func (s *Server) acceptDatasetSync(w http.ResponseWriter, r *http.Request) {
	networkID := r.Header.Get("X-Plainshow-Network")
	var body datasetSyncRequest
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if body.Dataset.NetworkID != networkID {
		fail(w, 403, "Dataset belongs to a different network.")
		return
	}
	body.Dataset.Manifest = body.Manifest
	if err := s.datasets.Import(body.Dataset, body.Chunks, s.cfg.Node.ID); err != nil {
		fail(w, 400, err.Error())
		return
	}
	target := filepath.Join(s.layout.Datasets(), "materialized", body.Dataset.ID)
	if err := s.datasets.Materialize(body.Dataset, target); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.hub.Publish("dataset.placement", store.DatasetPlacement{DatasetID: body.Dataset.ID, NodeID: s.cfg.Node.ID, State: "ready", BytesDone: body.Dataset.SizeBytes})
	writeJSON(w, 200, map[string]any{"status": "ready", "path": target, "bytes": body.Dataset.SizeBytes})
}

func (s *Server) remoteJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.store.Job(r.PathValue("id"))
	if err != nil {
		fail(w, 404, "No such job.")
		return
	}
	writeJSON(w, 200, job)
}
func (s *Server) remoteJobLogs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	writeJSON(w, 200, s.sup.Tail(r.PathValue("id"), limit))
}
func (s *Server) remoteJobStop(w http.ResponseWriter, r *http.Request) {
	if err := s.sup.Stop(r.PathValue("id")); err != nil {
		fail(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "stopping"})
}

func (s *Server) remoteJobInput(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Input string `json:"input"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err := s.sup.Input(r.PathValue("id"), body.Input); err != nil {
		fail(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "written"})
}

// advertisedEndpoint is the address other machines should use to reach this
// one.
//
// A tailnet address wins whenever there is one: it is stable, it works from
// any network without a forwarded port, and it is the same address torch and
// NCCL will use, so what the mesh proves reachable is what training will
// actually use. Without tailscale this falls back to whatever the host
// configured, which only works where the machines can already reach each other.
func advertisedEndpointFor(cfg *config.Config, status tailnet.Status) string {
	if status.Running && status.Self.Address != "" {
		port := cfg.Network.PeerPort
		if port == 0 {
			port = 10000
		}
		return "https://" + net.JoinHostPort(status.Self.Address, strconv.Itoa(port))
	}
	return advertisedEndpoint(cfg)
}

func advertisedEndpoint(cfg *config.Config) string {
	if strings.TrimSpace(cfg.Network.Advertise) != "" {
		return strings.TrimRight(cfg.Network.Advertise, "/")
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "localhost"
	}
	return "https://" + net.JoinHostPort(host, strconv.Itoa(cfg.Network.PeerPort))
}

func policyMap(policy config.WorkerConfig) map[string]any {
	return map[string]any{"enabled": policy.Enabled, "allow_jobs": policy.AllowJobs,
		"allow_gpu": policy.AllowGPU, "allow_terminal": policy.AllowTerminal,
		"max_cpu": policy.MaxCPU, "max_ram_mb": policy.MaxRAMMB}
}

func (s *Server) clientForNode(networkID, nodeID string) (peerTransport, error) {
	node, err := s.store.NetworkNode(networkID, nodeID)
	if err != nil {
		return nil, err
	}
	if node.Address == "" || node.Fingerprint == "" {
		// Machines on different networks reach each other through tailscale.
		// Plainshow does not carry traffic for them: saying so is more use than
		// a timeout, because the fix is one command on the other machine.
		return nil, fmt.Errorf(
			"%s has no address this machine can reach. If it is on another "+
				"network, install tailscale on both and sign them in — Plainshow "+
				"uses the address tailscale gives it", node.Name)
	}
	return mesh.NewClient(node.Address, node.Fingerprint, networkID, s.device), nil
}

func terminalJobState(state string) bool {
	return state == store.JobSucceeded || state == store.JobFailed || state == store.JobStopped
}

func (s *Server) monitorRemoteJob(networkID, nodeID string, client peerTransport, initial store.Job) {
	lastSeq := 0
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		var remote store.Job
		if err := client.JSON("GET", "/mesh/v1/jobs/"+initial.ID, nil, &remote, true); err != nil {
			continue
		}
		remote.MachineID, remote.Machine = nodeID, initial.Machine
		_ = s.store.SyncRemoteJob(remote)
		var lines []jobs.LogLine
		if err := client.JSON("GET", "/mesh/v1/jobs/"+initial.ID+"/logs?limit=2000", nil, &lines, true); err == nil {
			for _, line := range lines {
				if line.Seq > lastSeq {
					lastSeq = line.Seq
					s.remoteMu.Lock()
					s.remoteLogs[initial.ID] = append(s.remoteLogs[initial.ID], line)
					if len(s.remoteLogs[initial.ID]) > 2000 {
						s.remoteLogs[initial.ID] = s.remoteLogs[initial.ID][len(s.remoteLogs[initial.ID])-2000:]
					}
					s.remoteMu.Unlock()
					s.hub.Publish("job.log", line)
				}
			}
		}
		s.hub.Publish("job.state", remote)
		if terminalJobState(remote.State) {
			return
		}
	}
}

func (s *Server) remoteClient(jobID string) (peerTransport, bool) {
	s.remoteMu.RLock()
	defer s.remoteMu.RUnlock()
	client, ok := s.remoteClients[jobID]
	return client, ok
}

func (s *Server) remoteTail(jobID string, limit int) ([]jobs.LogLine, bool) {
	s.remoteMu.RLock()
	defer s.remoteMu.RUnlock()
	lines, ok := s.remoteLogs[jobID]
	if !ok {
		return nil, false
	}
	if limit <= 0 || limit > len(lines) {
		limit = len(lines)
	}
	out := append([]jobs.LogLine(nil), lines[len(lines)-limit:]...)
	return out, true
}
