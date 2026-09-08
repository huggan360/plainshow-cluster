package api

// Project folders with no project behind them.
//
// A working tree outlives its row more often than you would think: a project
// deleted on another machine, a network left, a download that landed after
// somebody removed the project, an upgrade that renamed folders. The files sit
// there taking disk and meaning nothing, and nothing in the interface ever
// mentions them because every page starts from the database.
//
// So this asks the disk instead, and offers to clear up. It never deletes on
// its own: a folder that looks orphaned to this node may be the only copy of
// somebody's work, and "I could not find a row for it" is not good enough
// grounds to remove data.

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// orphan is one directory that no project claims.
type orphan struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	SizeKB int    `json:"size_kb"`
}

// findOrphans lists project directories with no matching row.
//
// It looks one level down as well as at the top, because a project's folder
// lives either directly under projects/ or inside a network's subdirectory,
// and both shapes exist on a machine that has been through the move to
// independent projects.
func (s *Server) findOrphans() []orphan {
	projects, err := s.store.Projects()
	if err != nil {
		return []orphan{}
	}
	claimed := map[string]bool{}
	for _, project := range projects {
		claimed[filepath.Clean(s.projectDir(project))] = true
	}

	out := []orphan{}
	root := s.layout.Projects()
	consider := func(path, name string) {
		if claimed[filepath.Clean(path)] {
			return
		}
		// A partial download is in progress, not abandoned.
		if strings.HasSuffix(name, ".downloading") {
			return
		}
		out = append(out, orphan{
			Path: path, Name: name, SizeKB: directorySizeKB(path),
		})
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name())
		// A directory holding a repository is a project folder. One that only
		// holds other directories is a network scope, so look inside it.
		if isProjectFolder(path) {
			consider(path, entry.Name())
			continue
		}
		inner, err := os.ReadDir(path)
		if err != nil {
			continue
		}
		for _, child := range inner {
			if child.IsDir() {
				consider(filepath.Join(path, child.Name()),
					entry.Name()+"/"+child.Name())
			}
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

// isProjectFolder distinguishes a working tree from a directory that merely
// contains them.
func isProjectFolder(path string) bool {
	for _, marker := range []string{".git", "README.md", "main.py"} {
		if _, err := os.Stat(filepath.Join(path, marker)); err == nil {
			return true
		}
	}
	return false
}

func (s *Server) getOrphans(w http.ResponseWriter, r *http.Request) {
	orphans := s.findOrphans()
	total := 0
	for _, item := range orphans {
		total += item.SizeKB
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"orphans": orphans, "size_kb": total,
	})
}

// deleteOrphans removes the folders, and only the folders, that were offered.
//
// The paths are re-derived here rather than taken from the request. A caller
// naming its own path would turn this into a way to delete anything the node
// can reach, which is a great deal more than a leftover project folder.
func (s *Server) deleteOrphans(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Names []string `json:"names"`
	}
	_ = decode(r, &body)
	wanted := map[string]bool{}
	for _, name := range body.Names {
		wanted[name] = true
	}

	removed := []string{}
	freed := 0
	for _, item := range s.findOrphans() {
		if len(wanted) > 0 && !wanted[item.Name] {
			continue
		}
		if err := os.RemoveAll(item.Path); err != nil {
			continue
		}
		removed = append(removed, item.Name)
		freed += item.SizeKB
	}
	if len(removed) > 0 {
		s.hub.Publish("project.updated", map[string]any{"removed_orphans": len(removed)})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"removed": removed, "size_kb": freed,
	})
}
