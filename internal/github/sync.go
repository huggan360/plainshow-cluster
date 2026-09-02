package github

import (
	"context"
	"fmt"
	"strings"

	"github.com/huggan360/plainshow-cluster/internal/store"
)

// roleCapabilities maps a GitHub role onto what it means here.
//
// GitHub's roles are coarser than these capabilities, which is the source of
// every subtlety below: a role can always be derived from a capability set, but
// a capability set cannot always be recovered from a role.
var roleCapabilities = map[string][]string{
	"admin":    {store.CapView, store.CapCode, store.CapPush, store.CapRun, store.CapTrain, store.CapManage},
	"maintain": {store.CapView, store.CapCode, store.CapPush, store.CapRun, store.CapTrain},
	"push":     {store.CapView, store.CapCode, store.CapPush, store.CapRun},
	"write":    {store.CapView, store.CapCode, store.CapPush, store.CapRun},
	"triage":   {store.CapView, store.CapCode},
	"pull":     {store.CapView},
	"read":     {store.CapView},
}

// organisationRoles are the roles an organisation repository accepts.
var organisationRoles = []string{"pull", "triage", "push", "maintain", "admin"}

// personalRoles are the roles a personal repository accepts. There is exactly
// one: a personal repository can say who is a collaborator, but not what they
// may do.
var personalRoles = []string{"push"}

// CapabilitiesForRole is what a GitHub role grants here.
func CapabilitiesForRole(role string) map[string]bool {
	names, ok := roleCapabilities[strings.ToLower(role)]
	if !ok {
		names = []string{store.CapView}
	}
	return store.CapabilitySet(names...)
}

// RoleForCapabilities picks the closest GitHub role a repository can express.
func RoleForCapabilities(caps map[string]bool, supported []string) string {
	has := func(name string) bool { return caps[name] }
	allows := func(role string) bool {
		for _, r := range supported {
			if r == role {
				return true
			}
		}
		return false
	}
	switch {
	case has(store.CapManage) && allows("admin"):
		return "admin"
	case has(store.CapTrain) && allows("maintain"):
		return "maintain"
	case has(store.CapPush) || has(store.CapRun):
		return "push"
	case has(store.CapCode):
		if allows("triage") {
			return "triage"
		}
		return "push"
	}
	if allows("pull") {
		return "pull"
	}
	return "push"
}

// SupportedRoles reports which roles a repository accepts.
func SupportedRoles(ownerType string) []string {
	if ownerType == "Organization" {
		return organisationRoles
	}
	return personalRoles
}

// RolesAreMeaningful reports whether a repository can express more than "is a
// collaborator". On a personal repository it cannot, and GitHub's answer
// carries no information about what someone should be allowed to do.
func RolesAreMeaningful(ownerType string) bool { return ownerType == "Organization" }

// SyncResult is what a sync changed, in words the interface shows directly.
type SyncResult struct {
	Repository string   `json:"repository"`
	OwnerType  string   `json:"owner_type"`
	Changes    []string `json:"changes"`
	Warnings   []string `json:"warnings"`
}

// PushMember mirrors one person's access onto the repository.
//
// GitHub has no "no access" role, so withdrawing view means withdrawing the
// collaborator entirely; anything less would silently leave them read access
// there after being removed here.
func PushMember(ctx context.Context, c *Client, repo, ownerType string,
	m store.Member, remove bool) (string, error) {

	if m.GitHubLogin == "" {
		return "", nil
	}
	if remove || !m.Can(store.CapView) {
		if err := c.RemoveCollaborator(ctx, repo, m.GitHubLogin); err != nil {
			return "", err
		}
		return fmt.Sprintf("removed %s from GitHub", m.GitHubLogin), nil
	}

	supported := SupportedRoles(ownerType)
	role := RoleForCapabilities(m.Capabilities, supported)
	if err := c.AddCollaborator(ctx, repo, m.GitHubLogin, role); err != nil {
		return "", err
	}

	detail := ""
	if role != RoleForCapabilities(m.Capabilities, organisationRoles) {
		detail = " (this repository has no finer role)"
	}
	return fmt.Sprintf("invited %s to GitHub as %s%s", m.GitHubLogin, role, detail), nil
}

// Sync brings a project's membership and the repository's collaborators into
// agreement, in both directions.
//
// The rule that keeps this from thrashing: GitHub's roles cannot express these
// capabilities exactly, so adopting GitHub's answer on every sync would quietly
// undo any local change it cannot represent. A member's stored GitHubRole is
// what GitHub reported last time; the local capabilities are only replaced when
// GitHub's answer has actually changed since then.
func Sync(ctx context.Context, c *Client, st *store.Store,
	project store.Project, repo string, prune bool) (SyncResult, error) {

	result := SyncResult{Repository: repo, Changes: []string{}, Warnings: []string{}}

	details, err := c.Repository(ctx, repo)
	if err != nil {
		return result, err
	}
	result.OwnerType = details.Owner.Type
	meaningful := RolesAreMeaningful(details.Owner.Type)
	if !meaningful {
		result.Warnings = append(result.Warnings,
			"This is a personal repository, so GitHub only records who is a collaborator, "+
				"not what they may do. Access levels are kept here and are not overwritten by GitHub.")
	}

	remote, err := c.Collaborators(ctx, repo)
	if err != nil {
		return result, err
	}

	local, err := st.Members(project.ID)
	if err != nil {
		return result, err
	}
	byLogin := map[string]store.Member{}
	for _, m := range local {
		if m.GitHubLogin != "" {
			byLogin[strings.ToLower(m.GitHubLogin)] = m
		}
	}

	// GitHub -> here.
	seen := map[string]bool{}
	for _, entry := range remote {
		login := strings.ToLower(entry.Login)
		seen[login] = true
		role := entry.Role()

		existing, known := byLogin[login]
		if !known {
			// A collaborator we have never seen. On a personal repository
			// GitHub cannot say more than "write", so they get the default
			// working set rather than a set derived from a meaningless role.
			caps := CapabilitiesForRole(role)
			if !meaningful {
				caps = store.DefaultCapabilities()
			}
			member := store.Member{
				ProjectID: project.ID, Username: entry.Login,
				GitHubLogin: entry.Login, Capabilities: caps, GitHubRole: role,
			}
			if err := st.UpsertMember(member); err != nil {
				return result, err
			}
			result.Changes = append(result.Changes,
				fmt.Sprintf("added %s from GitHub", entry.Login))
			continue
		}
		if existing.Owner {
			continue
		}
		if !meaningful {
			// Membership syncs; the access level stays whatever was set here.
			if existing.GitHubRole != role {
				existing.GitHubRole = role
				if err := st.UpsertMember(existing); err != nil {
					return result, err
				}
			}
			continue
		}
		if existing.GitHubRole == role {
			continue // unchanged on GitHub — leave the local answer alone
		}
		existing.Capabilities = CapabilitiesForRole(role)
		existing.GitHubRole = role
		if err := st.UpsertMember(existing); err != nil {
			return result, err
		}
		result.Changes = append(result.Changes,
			fmt.Sprintf("matched %s to GitHub %s", entry.Login, role))
	}

	// Here -> GitHub, for anyone GitHub has not heard of.
	for _, m := range local {
		if m.Owner || m.GitHubLogin == "" || seen[strings.ToLower(m.GitHubLogin)] {
			continue
		}
		message, err := PushMember(ctx, c, repo, details.Owner.Type, m, false)
		if err != nil {
			// One failed invitation must not abandon the rest of the sync.
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("could not update %s on GitHub: %s", m.GitHubLogin, Friendly(err)))
			continue
		}
		if message != "" {
			m.GitHubRole = RoleForCapabilities(m.Capabilities, SupportedRoles(details.Owner.Type))
			if err := st.UpsertMember(m); err != nil {
				return result, err
			}
			result.Changes = append(result.Changes, message)
		}
	}

	// Anyone removed on GitHub loses access here — but only people who are
	// linked to a GitHub account, since GitHub cannot speak for anyone else.
	if prune {
		for _, m := range local {
			if m.Owner || m.GitHubLogin == "" || seen[strings.ToLower(m.GitHubLogin)] {
				continue
			}
			// Only prune someone GitHub already knew about. A member we just
			// failed to invite has no remembered role and must be left alone.
			if m.GitHubRole == "" {
				continue
			}
			if err := st.RemoveMember(project.ID, m.Username); err != nil {
				return result, err
			}
			result.Changes = append(result.Changes,
				fmt.Sprintf("removed %s, no longer a GitHub collaborator", m.Username))
		}
	}

	return result, nil
}
