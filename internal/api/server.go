// Package api serves the web interface and the node's HTTP API.
//
// Two transports, deliberately: REST for everything that reads or changes
// state, and one multiplexed WebSocket for everything that streams. There is no
// polling loop in the browser and no second socket per feature.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/huggan360/plainshow-cluster/internal/collab"
	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/dataset"
	"github.com/huggan360/plainshow-cluster/internal/events"
	"github.com/huggan360/plainshow-cluster/internal/gitrepo"
	"github.com/huggan360/plainshow-cluster/internal/identity"
	"github.com/huggan360/plainshow-cluster/internal/jobs"
	"github.com/huggan360/plainshow-cluster/internal/mesh"
	"github.com/huggan360/plainshow-cluster/internal/notebook"
	"github.com/huggan360/plainshow-cluster/internal/projectfs"
	"github.com/huggan360/plainshow-cluster/internal/store"
	"github.com/huggan360/plainshow-cluster/internal/sysinfo"
	"github.com/huggan360/plainshow-cluster/internal/training"
	"github.com/huggan360/plainshow-cluster/internal/updater"
	"github.com/huggan360/plainshow-cluster/internal/version"
)

// Server holds everything a request might need.
type Server struct {
	cfg           *config.Config
	layout        config.Layout
	store         *store.Store
	hub           *events.Hub
	sup           *jobs.Supervisor
	notebooks     *notebook.Manager
	collab        *collab.Manager
	datasets      *dataset.Manager
	updater       *updater.Updater
	device        *identity.Device
	fingerprint   string
	remoteMu      sync.RWMutex
	remoteClients map[string]peerTransport
	remoteLogs    map[string][]jobs.LogLine
	reservations  *training.Reservations
	localToken    string
	web           fs.FS
}

// New builds a server. web is the embedded interface, rooted at its index.html.
func New(cfg *config.Config, l config.Layout, st *store.Store, hub *events.Hub,
	sup *jobs.Supervisor, notebooks *notebook.Manager, collaboration *collab.Manager,
	datasets *dataset.Manager, up *updater.Updater,
	device *identity.Device, fingerprint string, web fs.FS) *Server {
	return &Server{cfg: cfg, layout: l, store: st, hub: hub, sup: sup,
		notebooks: notebooks, collab: collaboration, datasets: datasets, updater: up, device: device, fingerprint: fingerprint,
		remoteClients: make(map[string]peerTransport), remoteLogs: make(map[string][]jobs.LogLine),
		reservations: training.NewReservations(), web: web}
}

// peerTransport is how this node reaches another machine.
//
// It stays an interface although there is one implementation: it is the seam
// the tailscale work is happening behind, and having it meant swapping how a
// peer is reached without touching anything that calls it.
type peerTransport interface {
	JSON(method, path string, input, output any, authenticate bool) error
}

// UseLocalToken lets the command line authenticate as the machine's owner
// against its own daemon.
func (s *Server) UseLocalToken(token string) { s.localToken = token }

// Handler builds the route table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/overview", s.getOverview)
	mux.HandleFunc("GET /api/auth/status", s.authStatus)
	mux.HandleFunc("POST /api/auth/setup", s.authSetup)
	mux.HandleFunc("POST /api/auth/login", s.authLogin)
	mux.HandleFunc("POST /api/auth/logout", s.authLogout)
	mux.HandleFunc("GET /api/sysinfo", s.getSysinfo)
	mux.HandleFunc("GET /api/machines", s.getMachines)
	mux.HandleFunc("GET /api/tailnet", s.getTailnet)
	mux.HandleFunc("GET /api/networks", s.listNetworks)
	mux.HandleFunc("POST /api/networks", s.createNetwork)
	mux.HandleFunc("PUT /api/networks/{id}/active", s.activateNetwork)
	mux.HandleFunc("GET /api/networks/{id}/nodes", s.networkNodes)
	mux.HandleFunc("GET /api/networks/{id}/members", s.networkMembers)
	mux.HandleFunc("PUT /api/networks/{id}/policy", s.updateNetworkPolicy)
	mux.HandleFunc("POST /api/networks/{id}/invites", s.createNetworkInvite)
	mux.HandleFunc("POST /api/networks/{id}/controller-invites", s.createControllerInvite)
	mux.HandleFunc("GET /api/networks/{id}/controllers", s.networkControllers)
	mux.HandleFunc("POST /api/networks/join", s.joinNetwork)

	mux.HandleFunc("GET /api/projects", s.listProjects)
	mux.HandleFunc("GET /api/datasets", s.listDatasets)
	mux.HandleFunc("POST /api/datasets", s.registerDataset)
	mux.HandleFunc("POST /api/datasets/{id}/materialize", s.materializeDataset)
	mux.HandleFunc("GET /api/training", s.listTrainingRuns)
	mux.HandleFunc("POST /api/training/preflight", s.trainingPreflight)
	mux.HandleFunc("POST /api/training/advisor", s.trainingAdvice)
	mux.HandleFunc("POST /api/training/run", s.startTraining)
	mux.HandleFunc("POST /api/projects", s.createProject)
	mux.HandleFunc("DELETE /api/projects/{name}", s.deleteProject)
	mux.HandleFunc("GET /api/projects/{name}/tree", s.projectTree)
	mux.HandleFunc("GET /api/projects/{name}/file", s.readFile)
	mux.HandleFunc("PUT /api/projects/{name}/file", s.writeFile)
	mux.HandleFunc("GET /api/projects/{name}/collab", s.openCollabDocument)
	mux.HandleFunc("POST /api/projects/{name}/dir", s.createDir)
	mux.HandleFunc("POST /api/projects/{name}/rename", s.renameEntry)
	mux.HandleFunc("DELETE /api/projects/{name}/entry", s.deleteEntry)
	mux.HandleFunc("GET /api/projects/{name}/git", s.gitState)
	mux.HandleFunc("POST /api/projects/{name}/commit", s.gitCommit)

	mux.HandleFunc("GET /api/jobs", s.listJobs)
	mux.HandleFunc("POST /api/jobs", s.createJob)
	mux.HandleFunc("GET /api/jobs/{id}", s.getJob)
	mux.HandleFunc("GET /api/jobs/{id}/logs", s.getJobLogs)
	mux.HandleFunc("POST /api/jobs/{id}/stop", s.stopJob)
	mux.HandleFunc("POST /api/jobs/{id}/input", s.jobInput)

	mux.HandleFunc("POST /api/projects/{name}/notebooks", s.createNotebook)
	mux.HandleFunc("GET /api/projects/{name}/kernel", s.notebookStatus)
	mux.HandleFunc("POST /api/projects/{name}/jupyter", s.openJupyter)
	mux.HandleFunc("POST /api/projects/{name}/kernel/execute", s.executeNotebookCell)
	mux.HandleFunc("POST /api/projects/{name}/kernel/interrupt", s.interruptNotebook)
	mux.HandleFunc("POST /api/projects/{name}/kernel/restart", s.restartNotebook)
	mux.HandleFunc("/jupyter/{id}/{path...}", s.proxyJupyter)

	mux.HandleFunc("GET /api/projects/{name}/members", s.listMembers)
	mux.HandleFunc("POST /api/projects/{name}/members", s.addMember)
	mux.HandleFunc("PUT /api/projects/{name}/members/{username}", s.updateMember)
	mux.HandleFunc("DELETE /api/projects/{name}/members/{username}", s.removeMember)
	mux.HandleFunc("POST /api/projects/{name}/members/sync", s.syncMembers)

	mux.HandleFunc("POST /api/projects/{name}/repository", s.linkRepository)
	mux.HandleFunc("DELETE /api/projects/{name}/repository", s.unlinkRepository)
	mux.HandleFunc("POST /api/projects/{name}/push", s.gitPush)
	mux.HandleFunc("POST /api/projects/{name}/pull", s.gitPull)
	mux.HandleFunc("POST /api/projects/{name}/merge/abort", s.gitAbortMerge)

	mux.HandleFunc("GET /api/github", s.githubStatus)
	mux.HandleFunc("POST /api/github", s.githubConnect)
	mux.HandleFunc("DELETE /api/github", s.githubDisconnect)
	mux.HandleFunc("GET /api/github/repositories", s.githubRepositories)
	mux.HandleFunc("POST /api/github/clone", s.cloneRepository)

	mux.HandleFunc("GET /api/update", s.updateStatus)
	mux.HandleFunc("POST /api/update/check", s.updateCheck)
	mux.HandleFunc("POST /api/update/apply", s.updateApply)

	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)

	mux.HandleFunc("GET /ws", s.serveWS)

	mux.Handle("/", s.staticHandler())
	return logRequests(s.authenticate(mux))
}

// ------------------------------------------------------------- plumbing ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("api: encode response: %v", err)
	}
}

// fail returns an error the interface can show directly. Messages say what went
// wrong in the user's terms, because they are rendered verbatim.
func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 16<<20))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("could not read the request: %w", err)
	}
	return nil
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if strings.HasPrefix(r.URL.Path, "/api/") && time.Since(start) > 500*time.Millisecond {
			log.Printf("slow: %s %s took %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
		}
	})
}

// fsError turns a filesystem failure into a message safe and useful to show.
// ErrEscapes in particular must never reach the browser as raw text: it is a
// rejected attack as often as it is a typo.
func fsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, projectfs.ErrEscapes):
		fail(w, 400, "That path is outside the project.")
	case errors.Is(err, projectfs.ErrTooLarge):
		fail(w, 413, "That file is too large to open in the editor.")
	case errors.Is(err, os.ErrNotExist):
		fail(w, 404, "That file no longer exists.")
	default:
		fail(w, 400, err.Error())
	}
}

// project resolves the named project to a rooted filesystem view.
func (s *Server) project(name string) (store.Project, projectfs.Project, error) {
	p, err := s.store.ProjectByNameInNetwork(s.cfg.ActiveNetwork, name)
	if err != nil {
		return p, projectfs.Project{}, err
	}
	return p, projectfs.New(s.projectDir(p)), nil
}

func (s *Server) projectDir(p store.Project) string {
	scoped := filepath.Join(s.layout.Projects(), p.NetworkID, p.Name)
	if _, err := os.Stat(scoped); err == nil {
		return scoped
	}
	legacy := filepath.Join(s.layout.Projects(), p.Name)
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return scoped
}

// ------------------------------------------------------------- overview ----

func (s *Server) getOverview(w http.ResponseWriter, r *http.Request) {
	machines, err := s.store.NetworkNodes(s.cfg.ActiveNetwork)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	projects, err := s.store.ProjectsInNetwork(s.cfg.ActiveNetwork)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	active, err := s.store.ActiveJobs()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	recent, err := s.store.Jobs(8)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	networks, _ := s.store.Networks(s.cfg.AccountID())
	controllers, _ := s.store.NetworkControllers(s.cfg.ActiveNetwork)
	var activeController any
	if len(controllers) > 0 {
		item := controllers[0]
		wsURL := strings.Replace(item.Address, "https://", "wss://", 1)
		wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
		activeController = map[string]string{"id": item.ID, "name": item.Name,
			"ws_url": strings.TrimRight(wsURL, "/") + "/ws?network=" +
				url.QueryEscape(item.NetworkID) + "&token=" + url.QueryEscape(item.CollabToken)}
	}
	writeJSON(w, 200, map[string]any{
		"cluster": map[string]string{
			"id":   s.cfg.Cluster.ID,
			"name": s.cfg.Cluster.Name,
		},
		"node": map[string]any{
			"id":    s.cfg.Node.ID,
			"name":  s.cfg.Node.Name,
			"roles": s.cfg.RoleNames(),
			"root":  s.layout.Root,
		},
		"version":        version.Version,
		"networks":       networks,
		"active_network": s.cfg.ActiveNetwork,
		"machines":       machines,
		"projects":       projects,
		"active_jobs":    active,
		"recent_jobs":    recent,
		"system":         sysinfo.Probe(s.layout.Root),
		"git_available":  gitrepo.Available(),
		"github":         map[string]bool{"connected": s.tokenStore().Connected()},
		"update":         s.updater.Status(),
		"controller":     activeController,
	})
}

func (s *Server) getSysinfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, sysinfo.Probe(s.layout.Root))
}

func (s *Server) getMachines(w http.ResponseWriter, r *http.Request) {
	machines, err := s.store.NetworkNodes(s.cfg.ActiveNetwork)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, machines)
}

// ------------------------------------------------------------- projects ----

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.ProjectsInNetwork(s.cfg.ActiveNetwork)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, projects)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if !projectfs.ValidName(body.Name) {
		fail(w, 400, "Project names use letters, numbers, dashes and underscores, and cannot start with a dot.")
		return
	}
	if _, err := s.store.ProjectByNameInNetwork(s.cfg.ActiveNetwork, body.Name); err == nil {
		fail(w, 409, fmt.Sprintf("A project called %q already exists.", body.Name))
		return
	}

	p := store.Project{ID: config.NewID(), NetworkID: s.cfg.ActiveNetwork,
		Name: body.Name, Description: body.Description}
	dir := s.projectDir(p)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		fail(w, 500, fmt.Sprintf("Could not create the project directory: %v", err))
		return
	}

	if err := s.store.CreateProject(&p); err != nil {
		fail(w, 500, err.Error())
		return
	}

	// A starter file and a git history from the first moment, so that every
	// project is immediately runnable and immediately versioned.
	fsys := projectfs.New(dir)
	_ = fsys.WriteFile("README.md", fmt.Sprintf("# %s\n\n%s\n", p.Name, body.Description))
	_ = fsys.WriteFile("main.py", "print(\"Hello from "+p.Name+"\")\n")
	if gitrepo.Available() {
		repo := gitrepo.Open(dir)
		if err := repo.Init(); err != nil {
			log.Printf("project %s: git init: %v", p.Name, err)
		} else if _, err := repo.Commit("Create project " + p.Name); err != nil {
			log.Printf("project %s: initial commit: %v", p.Name, err)
		}
	}

	s.seedOwner(p)
	s.hub.Publish("project.created", p)
	writeJSON(w, 201, p)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p, _, err := s.project(name)
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	// Remove files first: if that fails the row survives, so the project is
	// still visible and can be retried rather than silently orphaned on disk.
	if err := os.RemoveAll(s.projectDir(p)); err != nil {
		fail(w, 500, fmt.Sprintf("Could not remove the project files: %v", err))
		return
	}
	if err := s.store.DeleteProjectID(p.ID); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.hub.Publish("project.deleted", map[string]string{"name": p.Name})
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (s *Server) projectTree(w http.ResponseWriter, r *http.Request) {
	_, fsys, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	tree, err := fsys.Tree(8)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, tree)
}

func (s *Server) readFile(w http.ResponseWriter, r *http.Request) {
	_, fsys, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	rel := r.URL.Query().Get("path")
	content, err := fsys.ReadFile(rel)
	if err != nil {
		fsError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"path": rel, "content": content})
}

func (s *Server) writeFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	_, fsys, err := s.project(name)
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	var body struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(body.Path) == "" {
		fail(w, 400, "No file path given.")
		return
	}
	if err := fsys.WriteFile(body.Path, body.Content); err != nil {
		fsError(w, err)
		return
	}
	if p, _, err := s.project(name); err == nil {
		_ = s.store.TouchProjectID(p.ID)
	}
	s.hub.Publish("file.saved", map[string]string{"project": name, "path": body.Path})
	writeJSON(w, 200, map[string]string{"status": "saved"})
}

func (s *Server) createDir(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	_, fsys, err := s.project(name)
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err := fsys.CreateDir(body.Path); err != nil {
		fsError(w, err)
		return
	}
	s.hub.Publish("tree.changed", map[string]string{"project": name})
	writeJSON(w, 201, map[string]string{"status": "created"})
}

func (s *Server) renameEntry(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	_, fsys, err := s.project(name)
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err := fsys.Rename(body.From, body.To); err != nil {
		fsError(w, err)
		return
	}
	s.hub.Publish("tree.changed", map[string]string{"project": name})
	writeJSON(w, 200, map[string]string{"status": "renamed"})
}

func (s *Server) deleteEntry(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	_, fsys, err := s.project(name)
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	if err := fsys.Remove(r.URL.Query().Get("path")); err != nil {
		fsError(w, err)
		return
	}
	s.hub.Publish("tree.changed", map[string]string{"project": name})
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

// ------------------------------------------------------------------ git ----

func (s *Server) gitState(w http.ResponseWriter, r *http.Request) {
	p, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	if !gitrepo.Available() {
		writeJSON(w, 200, map[string]any{
			"available": false, "changes": []any{}, "log": []any{},
			"conflicts": []any{}, "repository": "",
		})
		return
	}
	repo := gitrepo.Open(s.projectDir(p))
	changes, err := repo.Status()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	entries, err := repo.Log(20)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	repository := s.projectRepo(p)
	response := map[string]any{
		"available":  true,
		"branch":     repo.Branch(),
		"changes":    changes,
		"log":        entries,
		"repository": repository,
		"in_merge":   repo.InMerge(),
		"conflicts":  []string{},
	}
	if repository != "" {
		response["ahead"] = repo.Ahead()
		response["behind"] = repo.Behind()
	}
	if repo.InMerge() {
		if conflicts, err := repo.Conflicts(); err == nil {
			response["conflicts"] = conflicts
		}
	}
	writeJSON(w, 200, response)
}

func (s *Server) gitCommit(w http.ResponseWriter, r *http.Request) {
	p, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	repo := gitrepo.Open(s.projectDir(p))
	committed, err := repo.Commit(body.Message)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if !committed {
		writeJSON(w, 200, map[string]any{"committed": false, "message": "Nothing to commit."})
		return
	}
	s.hub.Publish("git.committed", map[string]string{"project": p.Name})
	writeJSON(w, 200, map[string]any{"committed": true})
}

// ----------------------------------------------------------------- jobs ----

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.store.Jobs(limit)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) createJob(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project  string   `json:"project"`
		Command  string   `json:"command"`
		Kind     string   `json:"kind"`
		Title    string   `json:"title"`
		Machine  string   `json:"machine_id"`
		Datasets []string `json:"datasets"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(body.Command) == "" {
		fail(w, 400, "Nothing to run — give a command.")
		return
	}

	req := jobs.Request{
		Kind: body.Kind, Title: body.Title, Command: body.Command,
		Workdir: s.layout.Root,
	}
	if body.Project != "" {
		p, _, err := s.project(body.Project)
		if err != nil {
			fail(w, 404, "No such project.")
			return
		}
		req.ProjectID = p.ID
		req.Project = p.Name
		req.Workdir = s.projectDir(p)
	}
	if body.Machine != "" && body.Machine != s.cfg.Node.ID {
		if req.ProjectID == "" && body.Kind != "terminal" {
			fail(w, 400, "Remote jobs must belong to a project so its files can be transferred.")
			return
		}
		var p store.Project
		var archive []byte
		if body.Project != "" {
			p, _, _ = s.project(body.Project)
			var archiveErr error
			archive, archiveErr = mesh.ArchiveDir(s.projectDir(p))
			if archiveErr != nil {
				fail(w, 500, "Could not prepare project: "+archiveErr.Error())
				return
			}
		}
		node, err := s.store.NetworkNode(s.cfg.ActiveNetwork, body.Machine)
		if err != nil {
			fail(w, 404, "No such machine in this network.")
			return
		}
		client, err := s.clientForNode(s.cfg.ActiveNetwork, body.Machine)
		if err != nil {
			fail(w, 409, err.Error())
			return
		}
		environment := map[string]string{}
		datasetPaths := []string{}
		for _, datasetID := range body.Datasets {
			dataset, err := s.store.Dataset(datasetID)
			if err != nil || dataset.NetworkID != s.cfg.ActiveNetwork {
				fail(w, 404, "A requested dataset does not exist in this network.")
				return
			}
			chunks, err := s.datasets.Export(dataset)
			if err != nil {
				fail(w, 500, "Could not read dataset: "+err.Error())
				return
			}
			var ready struct {
				Path string `json:"path"`
			}
			if err := client.JSON("POST", "/mesh/v1/datasets/sync", datasetSyncRequest{Dataset: dataset, Manifest: dataset.Manifest, Chunks: chunks}, &ready, true); err != nil {
				fail(w, 502, "Could not prepare dataset on worker: "+err.Error())
				return
			}
			datasetPaths = append(datasetPaths, ready.Path)
			_ = s.store.SetDatasetPlacement(store.DatasetPlacement{DatasetID: dataset.ID, NodeID: node.NodeID, State: "ready", BytesDone: dataset.SizeBytes})
		}
		if len(datasetPaths) > 0 {
			environment["PLAINSHOW_DATASET_DIR"] = datasetPaths[0]
			environment["PLAINSHOW_DATASET_DIRS"] = strings.Join(datasetPaths, ":")
		}
		var remote store.Job
		err = client.JSON("POST", "/mesh/v1/jobs", remoteJobRequest{ProjectID: p.ID,
			Project: p.Name, Description: p.Description, Kind: body.Kind, Title: body.Title,
			Command: body.Command, Archive: archive, Environment: environment}, &remote, true)
		if err != nil {
			fail(w, 502, "Worker refused the job: "+err.Error())
			return
		}
		remote.ProjectID, remote.Project = p.ID, p.Name
		remote.MachineID, remote.Machine = node.NodeID, node.Name
		_ = s.store.UpsertMachine(store.Machine{ID: node.NodeID, Name: node.Name, Roles: node.Roles,
			OS: node.OS, Arch: node.Arch, Address: node.Address, LastSeen: store.Now()})
		if err := s.store.CreateRemoteJob(remote); err != nil {
			fail(w, 500, err.Error())
			return
		}
		s.remoteMu.Lock()
		s.remoteClients[remote.ID] = client
		s.remoteLogs[remote.ID] = []jobs.LogLine{}
		s.remoteMu.Unlock()
		go s.monitorRemoteJob(s.cfg.ActiveNetwork, node.NodeID, client, remote)
		writeJSON(w, 201, remote)
		return
	}
	if len(body.Datasets) > 0 {
		paths := []string{}
		for _, id := range body.Datasets {
			dataset, err := s.store.Dataset(id)
			if err != nil || dataset.NetworkID != s.cfg.ActiveNetwork {
				fail(w, 404, "A requested dataset does not exist in this network.")
				return
			}
			target := filepath.Join(s.layout.Datasets(), "materialized", id)
			if err := s.datasets.Materialize(dataset, target); err != nil {
				fail(w, 500, err.Error())
				return
			}
			paths = append(paths, target)
		}
		req.Env = map[string]string{"PLAINSHOW_DATASET_DIR": paths[0], "PLAINSHOW_DATASET_DIRS": strings.Join(paths, ":")}
	}

	job, err := s.sup.Start(req)
	if err != nil {
		var pol jobs.ErrPolicy
		if errors.As(err, &pol) {
			fail(w, 403, pol.Reason)
			return
		}
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, job)
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.store.Job(r.PathValue("id"))
	if err != nil {
		fail(w, 404, "No such job.")
		return
	}
	writeJSON(w, 200, job)
}

func (s *Server) getJobLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.Job(id); err != nil {
		fail(w, 404, "No such job.")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if lines, ok := s.remoteTail(id, limit); ok {
		writeJSON(w, 200, lines)
		return
	}
	writeJSON(w, 200, s.sup.Tail(id, limit))
}

func (s *Server) stopJob(w http.ResponseWriter, r *http.Request) {
	if client, ok := s.remoteClient(r.PathValue("id")); ok {
		if err := client.JSON("POST", "/mesh/v1/jobs/"+r.PathValue("id")+"/stop", nil, nil, true); err != nil {
			fail(w, 409, err.Error())
			return
		}
		writeJSON(w, 200, map[string]string{"status": "stopping"})
		return
	}
	if err := s.sup.Stop(r.PathValue("id")); err != nil {
		fail(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "stopping"})
}

func (s *Server) jobInput(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Input string `json:"input"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	job, err := s.store.Job(r.PathValue("id"))
	if err != nil || job.Kind != "terminal" {
		fail(w, 404, "No such terminal job.")
		return
	}
	if client, ok := s.remoteClient(job.ID); ok {
		if err := client.JSON("POST", "/mesh/v1/jobs/"+job.ID+"/input", body, nil, true); err != nil {
			fail(w, 409, err.Error())
			return
		}
	} else if err := s.sup.Input(job.ID, body.Input); err != nil {
		fail(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "written"})
}

// ------------------------------------------------------------- settings ----

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"node":           s.cfg.Node,
		"cluster":        s.cfg.Cluster,
		"memberships":    s.cfg.Memberships,
		"active_network": s.cfg.ActiveNetwork,
		"network":        s.cfg.Network,
		"worker":         s.cfg.Worker,
		"update":         s.cfg.Update,
		"root":           s.layout.Root,
		"version":        version.Version,
		"paths": map[string]string{
			"config":    s.layout.ConfigFile(),
			"database":  s.layout.Database(),
			"projects":  s.layout.Projects(),
			"datasets":  s.layout.Datasets(),
			"artifacts": s.layout.Artifacts(),
			"logs":      s.layout.Logs(),
		},
	})
}

// putSettings updates machine-local policy. Identity and network settings
// still belong to whoever has a shell on the machine; worker limits and the
// release channel are safe to administer from the local interface.
//
// The incoming object is decoded *onto a copy of the current policy*, so a
// request that omits a field leaves it alone. Decoding into a zero value would
// silently turn every unmentioned permission off — which for a policy that
// exists to protect the machine's owner is the worst possible default.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	current := s.cfg.Worker
	currentUpdate := s.cfg.Update
	body := struct {
		Worker *config.WorkerConfig `json:"worker"`
		Update *config.UpdateConfig `json:"update"`
	}{Worker: &current, Update: &currentUpdate}

	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if body.Worker == nil || body.Update == nil {
		fail(w, 400, "No settings given.")
		return
	}
	body.Update.Repository = strings.TrimSpace(body.Update.Repository)
	if body.Update.Repository == "" || strings.Count(body.Update.Repository, "/") != 1 {
		fail(w, 400, "The update repository must be written as owner/name.")
		return
	}
	if body.Update.Channel != "stable" && body.Update.Channel != "beta" && body.Update.Channel != "any" {
		fail(w, 400, "The update channel must be stable, beta, or any.")
		return
	}
	if _, err := time.ParseDuration(body.Update.CheckEvery); err != nil {
		fail(w, 400, "The update interval must be a duration such as 6h or 30m.")
		return
	}
	s.cfg.Worker = *body.Worker
	s.cfg.Update = *body.Update
	if err := config.Save(s.layout, s.cfg); err != nil {
		fail(w, 500, fmt.Sprintf("Could not save settings: %v", err))
		return
	}
	s.updater.Configure(s.cfg.Update)
	s.hub.Publish("settings.changed", s.cfg.Worker)
	writeJSON(w, 200, map[string]any{"worker": s.cfg.Worker, "update": s.cfg.Update})
}

// -------------------------------------------------------------- realtime ----

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 16 * 1024,
	// The interface is served from the same origin as this socket, and the
	// node binds to loopback or the overlay rather than the public internet.
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	sub := s.hub.Subscribe()
	defer sub.Close()

	// A dead peer is only detectable by a failed write or a missed pong, so
	// read deadlines are refreshed by pongs and the writer pings periodically.
	conn.SetReadLimit(2 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	})
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				sub.Close()
				return
			}
			s.handleClientEvent(raw)
		}
	}()

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	for {
		select {
		case ev, ok := <-sub.C:
			if !ok {
				return
			}
			payload, err := ev.Encode()
			if err != nil {
				continue
			}
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ping.C:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ---------------------------------------------------------------- static ----

// staticHandler serves the embedded interface, falling back to index.html so
// the single-page app owns its own routes.
func (s *Server) staticHandler() http.Handler {
	files := http.FileServer(http.FS(s.web))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if clean != "" {
			if f, err := s.web.Open(clean); err == nil {
				info, statErr := f.Stat()
				f.Close()
				if statErr == nil && !info.IsDir() {
					if strings.HasSuffix(clean, ".js") || strings.HasSuffix(clean, ".css") {
						w.Header().Set("Cache-Control", "no-cache")
					}
					files.ServeHTTP(w, r)
					return
				}
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			fail(w, 404, "No such endpoint.")
			return
		}
		index, err := fs.ReadFile(s.web, "index.html")
		if err != nil {
			http.Error(w, "interface not built into this binary", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

// ----------------------------------------------------------------- serve ----

// ListenAndServe starts the HTTP server and blocks until ctx is cancelled.
// The chosen port is reported back so the caller can print the real URL when
// the configured port was zero.
func (s *Server) ListenAndServe(ctx context.Context, onReady func(addr string)) error {
	bind := s.cfg.Network.Bind
	if bind == "" {
		bind = "127.0.0.1"
	}
	ln, port, err := listen(bind, s.cfg.Network.Port)
	if err != nil {
		return err
	}
	s.cfg.Network.Port = port
	if onReady != nil {
		onReady(fmt.Sprintf("http://%s:%d", displayHost(bind), port))
	}

	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No write timeout: the event socket is a long-lived stream and a
		// deadline here would sever it every time it fired.
		IdleTimeout: 120 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// listen binds the requested port, or probes upward from the default when the
// configured port is zero. Nothing assumes a port is free.
func listen(bind string, port int) (net.Listener, int, error) {
	if port > 0 {
		ln, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(port)))
		if err != nil {
			return nil, 0, fmt.Errorf("port %d on %s is not available: %w", port, bind, err)
		}
		return ln, port, nil
	}
	for p := config.DefaultPort; p < config.DefaultPort+40; p++ {
		ln, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(p)))
		if err == nil {
			return ln, p, nil
		}
	}
	return nil, 0, fmt.Errorf("no free port found between %d and %d on %s",
		config.DefaultPort, config.DefaultPort+40, bind)
}

func displayHost(bind string) string {
	if bind == "0.0.0.0" || bind == "::" || bind == "" {
		return "localhost"
	}
	return bind
}

// StartTelemetry publishes a host snapshot on the event stream at a fixed
// interval, so the interface updates without polling. Telemetry is never
// persisted: it streams and is discarded.
func (s *Server) StartTelemetry(ctx context.Context, every time.Duration) {
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if s.hub.Count() == 0 {
					continue // nobody is watching; do not probe
				}
				s.hub.Publish("system", sysinfo.Probe(s.layout.Root))
			}
		}
	}()
}
