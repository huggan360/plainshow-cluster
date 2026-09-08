package api

// Downloading a project's files onto this machine.
//
// A project can exist here as a row long before its files do: adopted from the
// account, or joined through somebody else's invitation. Until the working tree
// arrives there is nothing to edit, run or commit, so the interface offers one
// thing and this is what it calls.
//
// Progress is git's own, read off its stderr, because a clone of a real
// repository takes long enough that a spinner is a lie — it says "working" when
// what somebody wants to know is "how much longer".

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/gitrepo"
	"github.com/huggan360/plainshow-cluster/internal/projectfs"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

// download is one transfer in flight.
type download struct {
	Project string `json:"project"`
	Source  string `json:"source"`
	Phase   string `json:"phase"`
	Percent int    `json:"percent"`
	Done    bool   `json:"done"`
	Error   string `json:"error"`
}

type downloads struct {
	mu      sync.Mutex
	running map[string]download
}

func (d *downloads) set(name string, item download) {
	d.mu.Lock()
	if d.running == nil {
		d.running = map[string]download{}
	}
	d.running[name] = item
	d.mu.Unlock()
}

func (d *downloads) clear(name string) {
	d.mu.Lock()
	delete(d.running, name)
	d.mu.Unlock()
}

// active reports whether a download is already running for a project, so a
// second click cannot start a second clone into the same directory.
func (d *downloads) active(name string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, running := d.running[name]
	return running
}

func (d *downloads) list() []download {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]download, 0, len(d.running))
	for _, item := range d.running {
		out = append(out, item)
	}
	return out
}

// startDownload fetches a project's files and reports as it goes.
func (s *Server) startDownload(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, http.StatusNotFound, "No such project.")
		return
	}
	dir := s.projectDir(project)
	if info, statErr := os.Stat(dir); statErr == nil && info.IsDir() {
		fail(w, http.StatusConflict, "This machine already has that project's files.")
		return
	}
	if s.downloads.active(project.Name) {
		fail(w, http.StatusConflict, "That project is already downloading.")
		return
	}
	if project.Repository == "" {
		fail(w, http.StatusConflict,
			"This project has no GitHub repository, so its files can only come from "+
				"another machine. Open it on a machine that has them and send them here.")
		return
	}
	token, err := s.tokenStore().Read()
	if err != nil {
		githubFail(w, err)
		return
	}

	s.downloads.set(project.Name, download{
		Project: project.Name, Source: project.Repository, Phase: "Starting",
	})
	s.publishDownload(project.Name)
	// Detached: a clone outlives the request that asked for it, and closing the
	// page must not abandon a half-written working tree.
	go s.runDownload(project, dir, token)

	writeJSON(w, http.StatusAccepted, map[string]string{
		"status": "downloading", "project": project.Name,
	})
}

func (s *Server) runDownload(project store.Project, dir, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	defer s.downloads.clear(project.Name)

	// Clone into a staging directory beside the destination. A clone that fails
	// half way must not leave something that looks like a project, because
	// everything downstream decides "has the files" by asking whether the
	// directory exists.
	staging := dir + ".downloading"
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		s.failDownload(project.Name, err)
		return
	}

	err := gitrepo.CloneWithProgress(ctx, project.Repository, staging, token,
		func(progress gitrepo.Progress) {
			s.downloads.set(project.Name, download{
				Project: project.Name, Source: project.Repository,
				Phase: progress.Phase, Percent: progress.Percent,
			})
			s.publishDownload(project.Name)
		})
	if err != nil {
		_ = os.RemoveAll(staging)
		s.failDownload(project.Name, err)
		return
	}
	if err := projectfs.OwnTree(staging, s.layout.Projects()); err != nil {
		_ = os.RemoveAll(staging)
		s.failDownload(project.Name, err)
		return
	}
	if err := os.Rename(staging, dir); err != nil {
		_ = os.RemoveAll(staging)
		s.failDownload(project.Name, err)
		return
	}
	if branch := gitrepo.Open(dir).Branch(); branch != "" {
		_ = s.store.SetProjectBranch(project.ID, branch)
	}

	s.downloads.set(project.Name, download{
		Project: project.Name, Source: project.Repository,
		Phase: "Ready", Percent: 100, Done: true,
	})
	s.publishDownload(project.Name)
	s.hub.Publish("project.updated", map[string]string{"project": project.Name})
}

func (s *Server) failDownload(name string, err error) {
	s.downloads.set(name, download{Project: name, Error: friendlyDownload(err), Done: true})
	s.publishDownload(name)
}

// friendlyDownload keeps a git error readable without hiding what went wrong.
func friendlyDownload(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "The download took longer than thirty minutes and was stopped."
	}
	return err.Error()
}

func (s *Server) publishDownload(name string) {
	s.downloads.mu.Lock()
	item := s.downloads.running[name]
	s.downloads.mu.Unlock()
	s.hub.Publish("project.download", item)
}

// getDownloads lets a page that has just been opened catch up with transfers
// that started before it existed.
func (s *Server) getDownloads(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"downloads": s.downloads.list()})
}
