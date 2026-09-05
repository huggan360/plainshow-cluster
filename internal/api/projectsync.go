package api

// Moving a project's files between machines.
//
// A project belongs to a network; its files belong to whichever machines have
// been given them. Those are deliberately different questions. Joining a
// network must not drag every repository and every dataset onto your laptop,
// and a machine cannot run anything useful until somebody has decided it
// should have the files.
//
// So the files travel on request, in either direction, and only if the machine
// receiving them has said it is willing.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/mesh"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

// projectPayload is one project and its files, as they cross the mesh.
type projectPayload struct {
	Project     string `json:"project"`
	Description string `json:"description"`
	Repository  string `json:"repository"`
	Archive     []byte `json:"archive"`
}

// projectSyncAllowed reports whether this machine will exchange project files
// for a network, and says why not when it will not.
//
// Both ceilings apply. The device-wide setting is the machine owner's, and a
// per-network setting can only narrow it — no network can grant itself more of
// somebody's computer than they offered.
func (s *Server) projectSyncAllowed(networkID string) error {
	if !s.cfg.Worker.AllowProjectSync {
		return errors.New("This machine does not accept project files. Turn on project files in Settings.")
	}
	for _, membership := range s.cfg.Memberships {
		if membership.ID != networkID {
			continue
		}
		if !membership.Enabled || !membership.Policy.AllowProjectSync {
			return errors.New("This machine does not accept project files from that network.")
		}
		return nil
	}
	return errors.New("This machine does not belong to that network.")
}

// ------------------------------------------------------------------ mesh ----

// acceptProject materialises a project pushed by a peer.
func (s *Server) acceptProject(w http.ResponseWriter, r *http.Request) {
	networkID := r.Header.Get("X-Plainshow-Network")
	if !hasMembership(s.cfg, networkID) {
		fail(w, http.StatusForbidden, "This device is not active in that network.")
		return
	}
	if err := s.projectSyncAllowed(networkID); err != nil {
		fail(w, http.StatusForbidden, err.Error())
		return
	}
	var body projectPayload
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(body.Project) == "" {
		fail(w, http.StatusBadRequest, "The transfer is missing its project name.")
		return
	}
	project, err := s.materialiseProject(networkID, body)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.hub.Publish("project.created", project)
	writeJSON(w, http.StatusCreated, project)
}

// serveProject hands a peer this machine's copy of a project.
func (s *Server) serveProject(w http.ResponseWriter, r *http.Request) {
	networkID := r.Header.Get("X-Plainshow-Network")
	if !hasMembership(s.cfg, networkID) {
		fail(w, http.StatusForbidden, "This device is not active in that network.")
		return
	}
	if err := s.projectSyncAllowed(networkID); err != nil {
		fail(w, http.StatusForbidden, err.Error())
		return
	}
	name := r.PathValue("name")
	project, err := s.store.ProjectByNameInNetwork(networkID, name)
	if err != nil {
		fail(w, http.StatusNotFound, "No such project in that network.")
		return
	}
	dir := filepath.Join(s.layout.Projects(), networkID, project.Name)
	if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
		fail(w, http.StatusNotFound, "This machine does not have that project's files.")
		return
	}
	archive, err := mesh.ArchiveDir(dir)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, projectPayload{
		Project: project.Name, Description: project.Description,
		Repository: project.Repository, Archive: archive,
	})
}

// materialiseProject writes a received project to disk and records it.
//
// The directory is replaced rather than merged: a half-overwritten working tree
// is worse than either version of it, and the sending machine's copy is what
// was asked for.
func (s *Server) materialiseProject(networkID string, body projectPayload) (store.Project, error) {
	project, err := s.store.ProjectByNameInNetwork(networkID, body.Project)
	if errors.Is(err, store.ErrNotFound) {
		project = store.Project{NetworkID: networkID, Name: body.Project,
			Description: body.Description, Repository: body.Repository}
		if err = s.store.CreateProject(&project); err != nil {
			return store.Project{}, err
		}
	} else if err != nil {
		return store.Project{}, err
	}
	dir := filepath.Join(s.layout.Projects(), networkID, project.Name)
	if err := os.RemoveAll(dir); err != nil {
		return store.Project{}, err
	}
	if err := mesh.ExtractArchive(body.Archive, dir); err != nil {
		return store.Project{}, fmt.Errorf("could not write the project files: %w", err)
	}
	return project, nil
}

// ------------------------------------------------------------------- api ----

// sendProject pushes this machine's copy of a project to another machine.
func (s *Server) sendProject(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, http.StatusNotFound, "No such project.")
		return
	}
	var body struct {
		NodeID string `json:"node_id"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	networkID := s.cfg.ActiveNetwork
	dir := s.projectDir(project)
	if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
		fail(w, http.StatusConflict,
			"This machine does not have that project's files, so it has nothing to send.")
		return
	}
	client, err := s.clientForNode(networkID, body.NodeID)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	archive, err := mesh.ArchiveDir(dir)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	payload := projectPayload{Project: project.Name, Description: project.Description,
		Repository: project.Repository, Archive: archive}
	var accepted store.Project
	if err := client.JSON("POST", "/mesh/v1/projects", payload, &accepted, true); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "sent", "bytes": len(archive), "project": accepted.Name,
	})
}

// fetchProject copies a project onto this machine from one that has it.
func (s *Server) fetchProject(w http.ResponseWriter, r *http.Request) {
	networkID := s.cfg.ActiveNetwork
	name := r.PathValue("name")
	var body struct {
		NodeID string `json:"node_id"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	// The receiving machine is this one, so its own policy decides — the same
	// check a peer would have made had the push come the other way.
	if err := s.projectSyncAllowed(networkID); err != nil {
		fail(w, http.StatusForbidden, err.Error())
		return
	}
	client, err := s.clientForNode(networkID, body.NodeID)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	var payload projectPayload
	if err := client.JSON("GET", "/mesh/v1/projects/"+url.PathEscape(name)+"/archive",
		nil, &payload, true); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	project, err := s.materialiseProject(networkID, payload)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.hub.Publish("project.created", project)
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "received", "bytes": len(payload.Archive), "project": project.Name,
	})
}

// projectReadiness answers "which machines could run this right now".
func (s *Server) projectReadiness(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, http.StatusNotFound, "No such project.")
		return
	}
	nodes, err := s.store.NetworkNodes(s.cfg.ActiveNetwork)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	devices := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		held := false
		if node.IsSelf {
			// Never report this machine from gossip: the disk is right here.
			held = s.hasProjectFiles(s.cfg.ActiveNetwork, project.Name)
		} else {
			for _, name := range node.Projects {
				if name == project.Name {
					held = true
					break
				}
			}
		}
		devices = append(devices, map[string]any{
			"node_id": node.NodeID, "name": node.Name, "is_self": node.IsSelf,
			"online": nodeCapacityOnline(node, time.Now()), "has_files": held,
			"accepts_files": policyAllowsProjectSync(node),
			"last_seen":     node.LastSeen,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project": project.Name, "devices": devices,
	})
}

func (s *Server) hasProjectFiles(networkID, name string) bool {
	info, err := os.Stat(filepath.Join(s.layout.Projects(), networkID, name))
	return err == nil && info.IsDir()
}

// policyAllowsProjectSync reads a peer's advertised policy. A machine that has
// never advertised the field predates it, and every machine used to accept
// project files, so its absence reads as yes.
func policyAllowsProjectSync(node store.NetworkNode) bool {
	value, ok := node.Policy["allow_project_sync"]
	if !ok {
		return true
	}
	allowed, isBool := value.(bool)
	return !isBool || allowed
}

// moveProjectNetwork changes which network a project belongs to.
//
// A project lives in exactly one network. That is not a limitation to work
// around: it is what makes "who can see this" answerable at all, and what lets
// a repository's collaborators mean one thing. Moving takes the files with it,
// because a project whose row moved and whose directory did not is a project
// that has quietly stopped working.
func (s *Server) moveProjectNetwork(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, http.StatusNotFound, "No such project.")
		return
	}
	var body struct {
		NetworkID string `json:"network_id"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	target := strings.TrimSpace(body.NetworkID)
	if target == "" || target == project.NetworkID {
		fail(w, http.StatusBadRequest, "Choose a different network to move this project to.")
		return
	}
	if !hasMembership(s.cfg, target) {
		fail(w, http.StatusForbidden, "This device does not belong to that network.")
		return
	}
	// Only the project's owner moves it. Being able to push to a repository is
	// not the same as deciding which network of people can see it at all.
	if !s.ownsProject(project.ID) {
		fail(w, http.StatusForbidden, "Only the project owner can move it to another network.")
		return
	}
	if _, err := s.store.ProjectByNameInNetwork(target, project.Name); err == nil {
		fail(w, http.StatusConflict,
			fmt.Sprintf("That network already has a project called %q.", project.Name))
		return
	}

	// Move the files first. A failed rename leaves the row where it was, which
	// is recoverable; a moved row with stranded files is not.
	from := s.projectDir(project)
	to := filepath.Join(s.layout.Projects(), target, project.Name)
	if _, statErr := os.Stat(from); statErr == nil {
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := os.Rename(from, to); err != nil {
			fail(w, http.StatusInternalServerError,
				"Could not move the project's files: "+err.Error())
			return
		}
	}
	if err := s.store.MoveProjectToNetwork(project.ID, target); err != nil {
		_ = os.Rename(to, from) // put the files back where the row still says they are
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.hub.Publish("project.moved", map[string]string{
		"project": project.Name, "from": project.NetworkID, "to": target,
	})
	writeJSON(w, http.StatusOK, map[string]string{
		"project": project.Name, "network_id": target,
	})
}

// ownsProject reports whether the person at this machine owns the project.
//
// Project membership is keyed by the name somebody is known by on GitHub, so
// that adding a collaborator here and there means the same thing. A machine
// with no GitHub connected records its own node name instead. Neither of those
// is the Plainshow account username, so ownership has to be checked against
// every identity this device answers to rather than a single guess.
func (s *Server) ownsProject(projectID string) bool {
	members, err := s.store.Members(projectID)
	if err != nil {
		return false
	}
	mine := s.localIdentities()
	for _, member := range members {
		if !member.Owner {
			continue
		}
		if mine[strings.ToLower(member.Username)] || mine[strings.ToLower(member.GitHubLogin)] {
			return true
		}
	}
	return false
}

// localIdentities is every name this device might be recorded under.
func (s *Server) localIdentities() map[string]bool {
	names := map[string]bool{}
	add := func(name string) {
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			names[name] = true
		}
	}
	add(s.cfg.Node.Name)
	add(s.cfg.Account.Username)
	if client, err := s.client(); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if account, viewerErr := client.Viewer(ctx); viewerErr == nil {
			add(account.Login)
		}
	}
	return names
}
