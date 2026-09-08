package api

// Resolving a merge.
//
// The same screen as the Plainshow console: both versions side by side, the
// result underneath, and three ways to settle a file without reading a diff.
// Conflict markers are a poor interface — they look like code, they compile in
// some languages, and committing them produces a file that appears merged and
// is wrong.

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/huggan360/plainshow-cluster/internal/gitrepo"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

// getConflicts lists what is unresolved, and loads one file to work on.
func (s *Server) getConflicts(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, http.StatusNotFound, "No such project.")
		return
	}
	repo := gitrepo.Open(s.projectDir(project))
	conflicts, err := repo.Conflicts()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	response := map[string]any{
		"in_merge": repo.InMerge(), "conflicts": conflicts, "branch": repo.Branch(),
	}
	// The interface asks for a file by name and falls back to the first, so
	// moving between them keeps the same screen open around the change.
	wanted := strings.TrimSpace(r.URL.Query().Get("path"))
	if wanted == "" && len(conflicts) > 0 {
		wanted = conflicts[0]
	}
	if wanted != "" && listed(conflicts, wanted) {
		raw, readErr := os.ReadFile(filepath.Join(s.projectDir(project), filepath.FromSlash(wanted)))
		if readErr == nil {
			response["file"] = map[string]string{"path": wanted, "content": string(raw)}
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func listed(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// resolveConflict takes one side of a file whole.
func (s *Server) resolveConflict(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, http.StatusNotFound, "No such project.")
		return
	}
	var body struct {
		Path   string `json:"path"`
		Choice string `json:"choice"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	repo := gitrepo.Open(s.projectDir(project))
	if !repo.InMerge() {
		fail(w, http.StatusConflict, "There is no merge in progress.")
		return
	}
	if err := repo.ResolveConflict(body.Path, body.Choice); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.afterResolve(project, body.Path)
	writeJSON(w, http.StatusOK, map[string]string{"status": "resolved", "path": body.Path})
}

// markResolved stages a file somebody edited by hand.
func (s *Server) markResolved(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, http.StatusNotFound, "No such project.")
		return
	}
	var body struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	repo := gitrepo.Open(s.projectDir(project))
	if !repo.InMerge() {
		fail(w, http.StatusConflict, "There is no merge in progress.")
		return
	}
	// Written through the project filesystem so path containment is enforced
	// by the one place that knows how, rather than a second time here.
	_, fs, err := s.project(project.Name)
	if err != nil {
		fail(w, http.StatusNotFound, "No such project.")
		return
	}
	if err := fs.WriteFile(body.Path, body.Content); err != nil {
		fsError(w, err)
		return
	}
	if err := repo.MarkResolved(body.Path); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.afterResolve(project, body.Path)
	writeJSON(w, http.StatusOK, map[string]string{"status": "resolved", "path": body.Path})
}

// finishMerge commits a merge whose conflicts are all settled.
func (s *Server) finishMerge(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, http.StatusNotFound, "No such project.")
		return
	}
	repo := gitrepo.Open(s.projectDir(project))
	if err := repo.FinishMerge(); err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	s.hub.Publish("git.committed", map[string]string{"project": project.Name})
	writeJSON(w, http.StatusOK, map[string]string{"status": "merged"})
}

// afterResolve tells every open browser the file changed underneath it.
//
// Resolving rewrites the working tree from outside the editor, so a browser
// holding the old text would otherwise save it back over the resolution.
func (s *Server) afterResolve(project store.Project, path string) {
	_ = s.collab.Reset(project.NetworkID, project.ID, path)
	s.hub.Publish("file.changed", map[string]string{"project": project.Name, "path": path})
}
