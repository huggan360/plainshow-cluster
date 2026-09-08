package api

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/huggan360/plainshow-cluster/internal/projectfs"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

func projectFolder(p store.Project) string {
	if _, name, found := strings.Cut(p.Repository, "/"); found && projectfs.ValidName(name) {
		return name
	}
	return p.Name
}

// A newly independent project may own a flat directory that an old scoped
// project once used as a migration fallback. Never resolve/share its files as
// the old project merely because both have the same display name.
func (s *Server) legacyProjectDir(p store.Project) string {
	if p.NetworkID != "" {
		local, err := s.store.ProjectsInNetwork("")
		if err != nil {
			return ""
		}
		for _, other := range local {
			if other.ID != p.ID && (other.Name == p.Name || projectFolder(other) == p.Name) {
				return ""
			}
		}
	}
	return filepath.Join(s.layout.Projects(), p.Name)
}

// MigrateProjectFolders runs before the node accepts requests. Renames never
// merge directories or discard an existing destination.
func (s *Server) MigrateProjectFolders() error {
	projects, err := s.store.Projects()
	if err != nil {
		return err
	}
	var failures []error
	for _, p := range projects {
		desired := filepath.Join(s.layout.Projects(), p.NetworkID, projectFolder(p))
		old := filepath.Join(s.layout.Projects(), p.NetworkID, p.Name)
		if _, err := os.Stat(old); os.IsNotExist(err) {
			old = s.legacyProjectDir(p)
			if old == "" {
				continue
			}
		}
		if old == desired {
			continue
		}
		if _, err := os.Stat(old); os.IsNotExist(err) {
			continue
		}
		if _, err := os.Lstat(desired); err == nil {
			failures = append(failures, fmt.Errorf("%s: both legacy and repository folders exist; kept both", p.Name))
			continue
		}
		if err := projectfs.MkdirOwned(filepath.Dir(desired)); err != nil {
			failures = append(failures, err)
			continue
		}
		if err := os.Rename(old, desired); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
