package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/huggan360/plainshow-cluster/internal/github"
	"github.com/huggan360/plainshow-cluster/internal/gitrepo"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

// tokenStore is where this node keeps its GitHub credential.
func (s *Server) tokenStore() github.TokenStore {
	return github.TokenStore{Path: s.layout.GitHubToken()}
}

// client builds a GitHub client from the saved token.
func (s *Server) client() (*github.Client, error) {
	token, err := s.tokenStore().Read()
	if err != nil {
		return nil, err
	}
	return github.New(token), nil
}

// githubFail renders a GitHub error in the words the interface should show.
func githubFail(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, github.ErrNotConnected):
		status = http.StatusPreconditionRequired
	case errors.Is(err, github.ErrBadCredentials):
		status = http.StatusUnauthorized
	}
	fail(w, status, github.Friendly(err))
}

// ------------------------------------------------------------- connection --

func (s *Server) githubStatus(w http.ResponseWriter, r *http.Request) {
	client, err := s.client()
	if err != nil {
		writeJSON(w, 200, map[string]any{"connected": false})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*1e9)
	defer cancel()

	account, err := client.Viewer(ctx)
	if err != nil {
		writeJSON(w, 200, map[string]any{
			"connected": false,
			"error":     github.Friendly(err),
		})
		return
	}

	repos, repoErr := client.Repositories(ctx)
	private := 0
	admin := 0
	for _, repo := range repos {
		if repo.Private {
			private++
		}
		if repo.Permissions.Admin {
			admin++
		}
	}

	// "Ready" means the token can actually do the things this product needs:
	// see repositories and administer at least one. A token with only read
	// scope connects fine and then fails at the first push, which is worse
	// than saying so here.
	response := map[string]any{
		"connected": true,
		"account":   account.Login,
		"name":      account.Name,
		"email":     account.Email,
		"repositories": map[string]int{
			"total": len(repos), "private": private, "admin": admin,
		},
		"git_ready": repoErr == nil && admin > 0,
	}
	if repoErr != nil {
		response["error"] = github.Friendly(repoErr)
		response["git_ready"] = false
	} else if admin == 0 {
		response["error"] = "This token can see repositories but cannot administer any. " +
			"It needs the full repo scope to create repositories and manage collaborators."
	}
	writeJSON(w, 200, response)
}

func (s *Server) githubConnect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	body.Token = strings.TrimSpace(body.Token)
	if len(body.Token) < 20 {
		fail(w, 400, "That does not look like a GitHub token.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*1e9)
	defer cancel()

	// Verify before saving, so a bad token is rejected at the point the person
	// can still see what they pasted.
	client := github.New(body.Token)
	account, err := client.Viewer(ctx)
	if err != nil {
		githubFail(w, err)
		return
	}
	if err := s.tokenStore().Write(body.Token); err != nil {
		fail(w, 500, fmt.Sprintf("Could not save the token: %v", err))
		return
	}

	s.hub.Publish("github.connected", map[string]string{"account": account.Login})
	writeJSON(w, 200, map[string]any{"connected": true, "account": account.Login})
}

func (s *Server) githubDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.tokenStore().Clear(); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.hub.Publish("github.disconnected", nil)
	writeJSON(w, 200, map[string]string{"status": "disconnected"})
}

func (s *Server) githubRepositories(w http.ResponseWriter, r *http.Request) {
	client, err := s.client()
	if err != nil {
		githubFail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*1e9)
	defer cancel()

	repos, err := client.Repositories(ctx)
	if err != nil {
		githubFail(w, err)
		return
	}
	writeJSON(w, 200, repos)
}

// ------------------------------------------------------- project ↔ repo ----

// projectRepo returns the repository a project mirrors, preferring the live git
// remote over the stored value so the two cannot silently disagree.
func (s *Server) projectRepo(p store.Project) string {
	repo := gitrepo.Open(s.projectDir(p)).RemoteRepository()
	if repo != "" {
		return repo
	}
	return p.Repository
}

func (s *Server) linkRepository(w http.ResponseWriter, r *http.Request) {
	p, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	var body struct {
		Repository string `json:"repository"`
		Create     bool   `json:"create"`
		Private    bool   `json:"private"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}

	client, err := s.client()
	if err != nil {
		githubFail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*1e9)
	defer cancel()

	repoName := strings.TrimSpace(body.Repository)
	if body.Create {
		if _, err := client.Viewer(ctx); err != nil {
			githubFail(w, err)
			return
		}
		name := repoName
		if name == "" {
			name = p.Name
		}
		if strings.Contains(name, "/") {
			name = name[strings.LastIndex(name, "/")+1:]
		}
		created, err := client.CreateRepository(ctx, name, p.Description, body.Private)
		if err != nil {
			githubFail(w, err)
			return
		}
		repoName = created.FullName
	} else {
		if !github.ValidRepository(repoName) {
			fail(w, 400, "Give the repository as owner/name.")
			return
		}
		if _, err := client.Repository(ctx, repoName); err != nil {
			githubFail(w, err)
			return
		}
	}

	repo := gitrepo.Open(s.projectDir(p))
	if err := repo.SetRemote(repoName); err != nil {
		fail(w, 500, err.Error())
		return
	}
	if err := s.store.SetProjectRepositoryID(p.ID, repoName); err != nil {
		fail(w, 500, err.Error())
		return
	}

	s.hub.Publish("project.repository", map[string]string{
		"project": p.Name, "repository": repoName,
	})
	writeJSON(w, 200, map[string]string{"repository": repoName})
}

func (s *Server) unlinkRepository(w http.ResponseWriter, r *http.Request) {
	p, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	// Only the link is removed. Nothing is deleted on GitHub, and the local
	// history stays exactly as it is. Both the database memo and git's live
	// remote must be cleared because projectRepo deliberately trusts the latter.
	repo := gitrepo.Open(s.projectDir(p))
	if err := repo.RemoveRemote(); err != nil {
		fail(w, 500, err.Error())
		return
	}
	if err := s.store.SetProjectRepositoryID(p.ID, ""); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.hub.Publish("project.repository", map[string]string{"project": p.Name, "repository": ""})
	writeJSON(w, 200, map[string]string{"status": "unlinked"})
}

// --------------------------------------------------------------- push/pull --

func (s *Server) gitPush(w http.ResponseWriter, r *http.Request) {
	p, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	repoName := s.projectRepo(p)
	if repoName == "" {
		fail(w, 400, "This project is not connected to a repository yet.")
		return
	}
	token, err := s.tokenStore().Read()
	if err != nil {
		githubFail(w, err)
		return
	}

	repo := gitrepo.Open(s.projectDir(p))
	// Commit anything outstanding first: pushing a project with unsaved work
	// still on disk is never what someone means by "push".
	if _, err := repo.Commit("Update from " + s.cfg.Node.Name); err != nil {
		fail(w, 400, err.Error())
		return
	}
	out, err := repo.Push(token)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.hub.Publish("git.pushed", map[string]string{"project": p.Name})
	writeJSON(w, 200, map[string]any{"output": out, "repository": repoName})
}

func (s *Server) gitPull(w http.ResponseWriter, r *http.Request) {
	p, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	if s.projectRepo(p) == "" {
		fail(w, 400, "This project is not connected to a repository yet.")
		return
	}
	token, err := s.tokenStore().Read()
	if err != nil {
		githubFail(w, err)
		return
	}

	repo := gitrepo.Open(s.projectDir(p))
	// Local work is committed before merging, so a merge never has to reason
	// about a dirty working tree and nothing can be lost to a checkout.
	if _, err := repo.Commit("Local work on " + s.cfg.Node.Name); err != nil {
		fail(w, 400, err.Error())
		return
	}

	result, err := repo.Pull(token)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.hub.Publish("tree.changed", map[string]string{"project": p.Name})
	writeJSON(w, 200, result)
}

func (s *Server) gitAbortMerge(w http.ResponseWriter, r *http.Request) {
	p, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	repo := gitrepo.Open(s.projectDir(p))
	if !repo.InMerge() {
		fail(w, 409, "There is no merge in progress.")
		return
	}
	if err := repo.AbortMerge(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.hub.Publish("tree.changed", map[string]string{"project": p.Name})
	writeJSON(w, 200, map[string]string{"status": "aborted"})
}

// clone creates a project from an existing repository.
func (s *Server) cloneRepository(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Repository string `json:"repository"`
		Name       string `json:"name"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if !github.ValidRepository(body.Repository) {
		fail(w, 400, "Give the repository as owner/name.")
		return
	}

	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = body.Repository[strings.Index(body.Repository, "/")+1:]
	}
	if !validProjectName(name) {
		fail(w, 400, "That repository name cannot be used as a project name. Give one explicitly.")
		return
	}
	if _, err := s.store.ProjectByNameInNetwork(s.cfg.ActiveNetwork, name); err == nil {
		fail(w, 409, fmt.Sprintf("A project called %q already exists.", name))
		return
	}

	token, err := s.tokenStore().Read()
	if err != nil {
		githubFail(w, err)
		return
	}

	p := store.Project{ID: newID(), NetworkID: s.cfg.ActiveNetwork,
		Name: name, Repository: body.Repository}
	dir := s.projectDir(p)
	if err := gitrepo.Clone(token, body.Repository, dir); err != nil {
		os.RemoveAll(dir)
		fail(w, 400, err.Error())
		return
	}

	if err := s.store.CreateProject(&p); err != nil {
		os.RemoveAll(dir)
		fail(w, 500, err.Error())
		return
	}
	_ = s.store.SetProjectRepositoryID(p.ID, body.Repository)
	s.seedOwner(p)

	s.hub.Publish("project.created", p)
	writeJSON(w, 201, p)
}
