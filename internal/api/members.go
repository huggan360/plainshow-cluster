package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/github"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

// newID is the id generator used for rows created by the API.
func newID() string { return config.NewID() }

// validProjectName mirrors the filesystem's rule, so a name that cannot be a
// directory is refused before anything is created.
func validProjectName(name string) bool {
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return name != "" && len(name) <= 64 && !strings.HasPrefix(name, ".")
}

// seedOwner records whoever runs this node as the project's owner, so a project
// always has exactly one person who can repair its access.
func (s *Server) seedOwner(p store.Project) {
	login := ""
	if client, err := s.client(); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if account, err := client.Viewer(ctx); err == nil {
			login = account.Login
		}
	}
	name := login
	if name == "" {
		name = s.cfg.Node.Name
	}
	_ = s.store.UpsertMember(store.Member{
		ProjectID:    p.ID,
		Username:     name,
		GitHubLogin:  login,
		Capabilities: store.OwnerCapabilities(),
		Owner:        true,
	})
}

// ------------------------------------------------------------------ list ---

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	p, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	members, err := s.store.Members(p.ID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	// Projects created before membership support was added have no owner row.
	// Repair them lazily the first time they are opened, using the same identity
	// rule as a newly created or cloned project.
	if len(members) == 0 {
		s.seedOwner(p)
		members, err = s.store.Members(p.ID)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
	}
	writeJSON(w, 200, map[string]any{
		"members":      members,
		"capabilities": store.Capabilities,
		"repository":   s.projectRepo(p),
	})
}

// ------------------------------------------------------------------- add ---

func (s *Server) addMember(w http.ResponseWriter, r *http.Request) {
	p, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	var body struct {
		// Username is the person's Plainshow account. Login is their GitHub
		// name. Either alone is enough: somebody invited by account may not
		// have connected GitHub yet, and a headless caller may only know a
		// GitHub login.
		Username     string   `json:"username"`
		Login        string   `json:"login"`
		Capabilities []string `json:"capabilities"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	login := strings.TrimSpace(strings.TrimPrefix(body.Login, "@"))
	username := strings.TrimSpace(strings.TrimPrefix(body.Username, "@"))
	if username == "" && login == "" {
		fail(w, 400, "Give a Plainshow account or a GitHub username.")
		return
	}
	// Inviting by account is the normal path, so look up what GitHub calls
	// them. Somebody who has not connected GitHub still gets access here; they
	// simply cannot be made a collaborator on the repository until they do,
	// and the interface says so rather than failing the invitation.
	if username != "" && login == "" {
		login = s.githubLoginForAccount(r.Context(), username)
	}
	if username == "" {
		username = login
	}

	caps := store.DefaultCapabilities()
	if len(body.Capabilities) > 0 {
		caps, err = store.CapabilitiesFromNames(body.Capabilities)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		// Everything implies being able to open the project; granting code
		// without view would be a state nothing can express.
		caps[store.CapView] = true
	}

	client, clientErr := s.client()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Catch a typo before it becomes a member row nobody can use.
	if clientErr == nil && login != "" {
		exists, err := client.UserExists(ctx, login)
		if err == nil && !exists {
			fail(w, 404, fmt.Sprintf("GitHub has no user called %q.", login))
			return
		}
	}

	member := store.Member{
		ProjectID: p.ID, Username: username, GitHubLogin: login, Capabilities: caps,
	}
	if err := s.store.UpsertMember(member); err != nil {
		fail(w, 500, err.Error())
		return
	}

	response := map[string]any{"member": member, "github": ""}
	if login == "" {
		response["github"] = username + " has access here. They cannot be added to " +
			"the repository until they connect GitHub to their Plainshow account."
	}
	if repo := s.projectRepo(p); repo != "" && clientErr == nil && login != "" {
		details, err := client.Repository(ctx, repo)
		if err != nil {
			response["github"] = "Added here, but GitHub could not be reached: " + github.Friendly(err)
		} else if message, err := github.PushMember(ctx, client, repo, details.Owner.Type, member, false); err != nil {
			response["github"] = "Added here, but GitHub was not updated: " + github.Friendly(err)
		} else {
			member.GitHubRole = github.RoleForCapabilities(caps, github.SupportedRoles(details.Owner.Type))
			_ = s.store.UpsertMember(member)
			response["github"] = message
			response["member"] = member
		}
	} else if repo == "" {
		response["github"] = "This project has no repository yet, so nothing was sent to GitHub."
	}

	s.hub.Publish("members.changed", map[string]string{"project": p.Name})
	writeJSON(w, 201, response)
}

// --------------------------------------------------------------- update ----

func (s *Server) updateMember(w http.ResponseWriter, r *http.Request) {
	p, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	username := r.PathValue("username")
	member, err := s.store.Member(p.ID, username)
	if err != nil {
		fail(w, 404, "That person is not on this project.")
		return
	}
	if member.Owner {
		fail(w, 400, "The project owner always holds every capability.")
		return
	}

	var body struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	caps, err := store.CapabilitiesFromNames(body.Capabilities)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if caps[store.CapCode] || caps[store.CapPush] || caps[store.CapRun] ||
		caps[store.CapTrain] || caps[store.CapManage] {
		caps[store.CapView] = true
	}
	member.Capabilities = caps

	if err := s.store.UpsertMember(member); err != nil {
		fail(w, 500, err.Error())
		return
	}

	note := ""
	if repo := s.projectRepo(p); repo != "" {
		if client, err := s.client(); err == nil {
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			if details, err := client.Repository(ctx, repo); err == nil {
				if message, err := github.PushMember(ctx, client, repo, details.Owner.Type, member, false); err != nil {
					note = "Saved here, but GitHub was not updated: " + github.Friendly(err)
				} else {
					member.GitHubRole = github.RoleForCapabilities(caps,
						github.SupportedRoles(details.Owner.Type))
					_ = s.store.UpsertMember(member)
					note = message
				}
			}
		}
	}

	s.hub.Publish("members.changed", map[string]string{"project": p.Name})
	writeJSON(w, 200, map[string]any{"member": member, "github": note})
}

// --------------------------------------------------------------- remove ----

func (s *Server) removeMember(w http.ResponseWriter, r *http.Request) {
	p, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	username := r.PathValue("username")
	member, err := s.store.Member(p.ID, username)
	if err != nil {
		fail(w, 404, "That person is not on this project.")
		return
	}
	if err := s.store.RemoveMember(p.ID, username); err != nil {
		fail(w, 400, err.Error())
		return
	}

	note := ""
	if repo := s.projectRepo(p); repo != "" && member.GitHubLogin != "" {
		if client, err := s.client(); err == nil {
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			if message, err := github.PushMember(ctx, client, repo, "", member, true); err != nil {
				note = "Removed here, but GitHub was not updated: " + github.Friendly(err)
			} else {
				note = message
			}
		}
	}

	s.hub.Publish("members.changed", map[string]string{"project": p.Name})
	writeJSON(w, 200, map[string]any{"status": "removed", "github": note})
}

// ----------------------------------------------------------------- sync ----

func (s *Server) syncMembers(w http.ResponseWriter, r *http.Request) {
	p, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	repo := s.projectRepo(p)
	if repo == "" {
		fail(w, 400, "This project is not connected to a repository yet.")
		return
	}
	client, err := s.client()
	if err != nil {
		githubFail(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	result, err := github.Sync(ctx, client, s.store, p, repo, true)
	if err != nil {
		githubFail(w, err)
		return
	}
	s.hub.Publish("members.changed", map[string]string{"project": p.Name})
	writeJSON(w, 200, result)
}

// githubLoginForAccount asks the account service what GitHub calls somebody.
//
// It answers "" for an account with no GitHub connected, and for a node with no
// account authority at all. Both are ordinary: access here is recorded either
// way, and only the repository half waits.
func (s *Server) githubLoginForAccount(parent context.Context, username string) string {
	client, token, err := s.authority(parent)
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	accounts, err := client.SearchAccounts(ctx, token, username)
	if err != nil {
		return ""
	}
	for _, account := range accounts {
		if strings.EqualFold(account.Username, username) {
			return account.GitHubLogin
		}
	}
	return ""
}
