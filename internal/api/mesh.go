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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/jobs"
	"github.com/huggan360/plainshow-cluster/internal/mesh"
	"github.com/huggan360/plainshow-cluster/internal/store"
	"github.com/huggan360/plainshow-cluster/internal/sysinfo"
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
	Token       string         `json:"token"`
	NodeID      string         `json:"node_id"`
	Name        string         `json:"name"`
	PublicKey   string         `json:"public_key"`
	Fingerprint string         `json:"fingerprint"`
	Endpoint    string         `json:"endpoint"`
	Roles       []string       `json:"roles"`
	Info        sysinfo.Info   `json:"info"`
	Policy      map[string]any `json:"policy"`
}

type joinResponse struct {
	Network store.Network       `json:"network"`
	Role    string              `json:"role"`
	Nodes   []store.NetworkNode `json:"nodes"`
}

// MeshHandler is the deliberately narrow API exposed on the encrypted peer
// port. The browser interface and local administration API are never exposed
// there.
func (s *Server) MeshHandler() http.Handler {
	root := http.NewServeMux()
	root.HandleFunc("POST /mesh/v1/join/{network}", s.acceptJoin)
	authed := http.NewServeMux()
	authed.HandleFunc("POST /mesh/v1/jobs", s.acceptRemoteJob)
	authed.HandleFunc("POST /mesh/v1/datasets/sync", s.acceptDatasetSync)
	authed.HandleFunc("GET /mesh/v1/jobs/{id}", s.remoteJob)
	authed.HandleFunc("GET /mesh/v1/jobs/{id}/logs", s.remoteJobLogs)
	authed.HandleFunc("POST /mesh/v1/jobs/{id}/stop", s.remoteJobStop)
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
	account := store.Account{ID: body.NodeID, Username: body.Name, DisplayName: body.Name, PublicKey: body.PublicKey}
	if err := s.store.UpsertAccount(account); err != nil {
		fail(w, 409, "That device name is already used by another account.")
		return
	}
	if err := s.store.AddNetworkMember(networkID, account.ID, invitation.Role); err != nil {
		fail(w, 500, err.Error())
		return
	}
	if len(body.Roles) == 0 {
		body.Roles = []string{"worker"}
	}
	if err := s.store.UpsertNetworkNode(store.NetworkNode{NetworkID: networkID, NodeID: body.NodeID,
		Name: body.Name, Roles: body.Roles, OS: body.Info.OS, Arch: body.Info.Arch,
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
	writeJSON(w, 201, joinResponse{Network: network, Role: invitation.Role, Nodes: nodes})
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
	if body.ProjectID == "" || body.Project == "" || strings.TrimSpace(body.Command) == "" {
		fail(w, 400, "The remote job is missing its project or command.")
		return
	}
	project, err := s.store.ProjectByNameInNetwork(networkID, body.Project)
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
	dir := filepath.Join(s.layout.Projects(), networkID, body.Project)
	if err := os.RemoveAll(dir); err != nil {
		fail(w, 500, err.Error())
		return
	}
	if err := mesh.ExtractArchive(body.Archive, dir); err != nil {
		fail(w, 400, "Could not materialise project: "+err.Error())
		return
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

func joiningRoles() []string { return []string{string(config.RoleWorker)} }

func (s *Server) clientForNode(networkID, nodeID string) (*mesh.Client, error) {
	node, err := s.store.NetworkNode(networkID, nodeID)
	if err != nil {
		return nil, err
	}
	if node.Address == "" || node.Fingerprint == "" {
		return nil, fmt.Errorf("machine %s has no reachable mesh address", node.Name)
	}
	return mesh.NewClient(node.Address, node.Fingerprint, networkID, s.device), nil
}

func terminalJobState(state string) bool {
	return state == store.JobSucceeded || state == store.JobFailed || state == store.JobStopped
}

func (s *Server) monitorRemoteJob(networkID, nodeID string, client *mesh.Client, initial store.Job) {
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

func (s *Server) remoteClient(jobID string) (*mesh.Client, bool) {
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
