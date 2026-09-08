package api

// Keeping the account's project list and this machine's in step.
//
// Two different things travel here, and keeping them apart is the point.
// Upwards goes metadata: a name, a repository, a branch, a size. Downwards
// comes the same, for projects made on the account's other machines. Files
// never touch this path — they go directly between machines, or from GitHub.
//
// The result is that a second machine shows you the project you made on the
// first, marked as having no files yet, instead of an empty workspace and no
// explanation of where your work went.

import (
	"context"
	"os"
	"path/filepath"

	"github.com/huggan360/plainshow-cluster/internal/accountclient"
	"github.com/huggan360/plainshow-cluster/internal/accountserver"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

// publishProjects reports the projects whose files this machine actually holds.
//
// Only those: a row adopted from the account and never downloaded is somebody
// else's report, and echoing it back would let an empty placeholder overwrite
// the real project's size and description.
func (s *Server) publishProjects(ctx context.Context, client *accountclient.Client, token string) {
	projects, err := s.store.Projects()
	if err != nil {
		return
	}
	for _, project := range projects {
		dir := s.projectDir(project)
		info, statErr := os.Stat(dir)
		if statErr != nil || !info.IsDir() {
			continue
		}
		members := []string{}
		if rows, memberErr := s.store.Members(project.ID); memberErr == nil {
			for _, member := range rows {
				members = append(members, member.Username)
			}
		}
		_, _ = client.SyncProject(ctx, token, accountserver.ProjectRegistration{
			ID: project.ID, Name: project.Name, Description: project.Description,
			Repository: project.Repository, Branch: project.Branch,
			SizeKB: directorySizeKB(dir), Members: members,
		})
	}
}

// adoptAccountProjects creates a local row for every project the account can
// see that this machine has never heard of.
//
// The row carries no files, and that is the state the interface has to render:
// a project you own, on a machine that cannot open it yet.
func (s *Server) adoptAccountProjects(ctx context.Context, client *accountclient.Client, token string) int {
	remote, err := client.Projects(ctx, token)
	if err != nil {
		return 0
	}
	adopted := 0
	for _, item := range remote {
		if item.ID == "" || item.Name == "" {
			continue
		}
		if _, err := s.store.ProjectByID(item.ID); err == nil {
			continue
		}
		// A different project already using the name would collide on disk and
		// in every lookup, so it is left alone rather than renamed silently.
		if _, err := s.store.ProjectByName(item.Name); err == nil {
			continue
		}
		project := store.Project{
			ID: item.ID, Name: item.Name, Description: item.Description,
			Branch: item.Branch,
		}
		if err := s.store.CreateProject(&project); err != nil {
			continue
		}
		if item.Repository != "" {
			_ = s.store.SetProjectRepositoryID(project.ID, item.Repository)
		}
		if item.Branch != "" {
			_ = s.store.SetProjectBranch(project.ID, item.Branch)
		}
		adopted++
	}
	if adopted > 0 {
		s.hub.Publish("project.created", map[string]any{"adopted": adopted})
	}
	return adopted
}

// directorySizeKB is what a person is told before they agree to a download.
//
// It walks rather than asking git, because the thing about to cross the network
// is the working tree — data files included — and a repository's packed size
// says nothing useful about that.
func directorySizeKB(root string) int {
	var total int64
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if info, statErr := entry.Info(); statErr == nil {
			total += info.Size()
		}
		return nil
	})
	return int(total / 1024)
}
