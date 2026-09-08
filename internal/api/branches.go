package api

// Branches, and the projects that hold them.
//
// A repository's branches live in separate directories here, one project each.
// That is not a filing decision: two branches in one directory means checking
// out to switch, which throws away uncommitted work and stops whichever machine
// is mid-run from finding the files it was using. Separate directories mean dev
// and main can both be checked out, on the same machine, at the same time.
//
// A repository plus a branch is one working tree. Pushing to dev twice must
// therefore not make two dev projects — the second would drift from the first
// and each would think it was the one.

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/gitrepo"
	"github.com/huggan360/plainshow-cluster/internal/projectfs"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

// branchProjectName is what a branch's project is called.
//
// The default branch keeps the repository's own name, so the common case reads
// as it always did; anything else is suffixed, because two projects cannot
// share a name in one network and "vision" meaning three different working
// trees would be worse than a longer name.
func branchProjectName(base, branch string, isDefault bool) string {
	if isDefault {
		return base
	}
	return base + "@" + branch
}

func defaultBranch(name string) bool { return name == "main" || name == "master" }

// getBranches lists this repository's branches and which of them already have
// a project on this machine.
func (s *Server) getBranches(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, http.StatusNotFound, "No such project.")
		return
	}
	repo := gitrepo.Open(s.projectDir(project))
	local, _ := repo.Branches()
	siblings, err := s.store.SiblingProjects(project.NetworkID, project.Repository)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	held := make(map[string]store.Project, len(siblings))
	for _, item := range siblings {
		held[item.Branch] = item
	}
	// Every branch worth showing: one this directory knows about, or one some
	// other directory on this machine is already holding.
	seen := map[string]bool{}
	rows := []map[string]any{}
	add := func(branch string) {
		if branch == "" || seen[branch] {
			return
		}
		seen[branch] = true
		row := map[string]any{
			"branch": branch, "current": branch == repo.Branch(),
			"default": defaultBranch(branch),
		}
		if item, ok := held[branch]; ok {
			row["project"] = map[string]string{
				"id": item.ID, "name": item.Name, "path": s.projectDir(item),
			}
			row["is_this_project"] = item.ID == project.ID
		}
		rows = append(rows, row)
	}
	for _, branch := range local {
		add(branch)
	}
	for _, item := range siblings {
		add(item.Branch)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"project": project.Name, "repository": project.Repository,
		"current": repo.Branch(), "branches": rows,
	})
}

// pushToBranch sends this working tree's work to a branch, and makes sure the
// branch has somewhere to live on this machine.
func (s *Server) pushToBranch(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, http.StatusNotFound, "No such project.")
		return
	}
	var body struct {
		Branch string `json:"branch"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	branch := strings.TrimSpace(body.Branch)
	if branch == "" || !projectfs.ValidName(branch) {
		fail(w, http.StatusBadRequest,
			"Give a branch name using letters, numbers, dashes and underscores.")
		return
	}
	if project.Repository == "" {
		fail(w, http.StatusConflict,
			"Connect this project to a GitHub repository before pushing to a branch.")
		return
	}
	token, err := s.tokenStore().Read()
	if err != nil {
		githubFail(w, err)
		return
	}

	repo := gitrepo.Open(s.projectDir(project))
	output, err := repo.PushTo(token, branch)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}

	response := map[string]any{
		"branch": branch, "detail": output, "pushed": true,
	}
	// Pushing to the branch this directory is already on is just a push.
	if branch == repo.Branch() {
		response["project"] = project.Name
		writeJSON(w, http.StatusOK, response)
		return
	}
	// Somewhere already holds this branch: use it rather than making a second.
	if existing, lookupErr := s.store.ProjectOnBranch(
		project.NetworkID, project.Repository, branch); lookupErr == nil {
		response["project"] = existing.Name
		response["created"] = false
		response["detail"] = fmt.Sprintf(
			"Pushed to %s. %s already holds that branch — pull there to pick this up.",
			branch, existing.Name)
		writeJSON(w, http.StatusOK, response)
		return
	}

	created, err := s.createBranchProject(project, branch)
	if err != nil {
		// The push succeeded, so say so rather than reporting total failure:
		// the work is safely on GitHub either way.
		response["created"] = false
		response["detail"] = fmt.Sprintf(
			"Pushed to %s, but could not make a local directory for it: %v", branch, err)
		writeJSON(w, http.StatusOK, response)
		return
	}
	response["project"] = created.Name
	response["created"] = true
	s.hub.Publish("project.created", created)
	writeJSON(w, http.StatusCreated, response)
}

// createBranchProject gives a branch its own directory and project row.
func (s *Server) createBranchProject(source store.Project, branch string) (store.Project, error) {
	base := projectFolder(source)
	if strings.Contains(base, "@") {
		base = strings.SplitN(base, "@", 2)[0]
	}
	name := branchProjectName(base, branch, false)
	if _, err := s.store.ProjectByNameInNetwork(source.NetworkID, name); err == nil {
		return store.Project{}, fmt.Errorf("a project called %q already exists", name)
	}

	sourceDir := s.projectDir(source)
	target := filepath.Join(s.layout.Projects(), source.NetworkID, name)
	if _, err := os.Stat(target); err == nil {
		return store.Project{}, fmt.Errorf("the folder %q already exists", name)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return store.Project{}, err
	}
	// The branch has to exist locally before it can be cloned by name.
	if err := gitrepo.Open(sourceDir).CreateBranch(branch); err != nil {
		return store.Project{}, err
	}
	if err := gitrepo.CloneLocal(sourceDir, target, branch); err != nil {
		return store.Project{}, err
	}
	if err := projectfs.OwnTree(target, s.layout.Projects()); err != nil {
		return store.Project{}, err
	}

	project := store.Project{
		ID: config.NewID(), NetworkID: source.NetworkID, Name: name,
		Description: source.Description, Branch: branch,
	}
	if err := s.store.CreateProject(&project); err != nil {
		_ = os.RemoveAll(target)
		return store.Project{}, err
	}
	if err := s.store.SetProjectRepositoryID(project.ID, source.Repository); err != nil {
		return store.Project{}, err
	}
	if err := s.store.SetProjectBranch(project.ID, branch); err != nil {
		return store.Project{}, err
	}
	project.Repository = source.Repository
	s.seedOwner(project)
	return project, nil
}
