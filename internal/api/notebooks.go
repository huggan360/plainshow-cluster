package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/huggan360/plainshow-cluster/internal/notebook"
	"github.com/huggan360/plainshow-cluster/internal/projectfs"
)

type notebookDocument struct {
	Cells         []notebookCell `json:"cells"`
	Metadata      map[string]any `json:"metadata"`
	NBFormat      int            `json:"nbformat"`
	NBFormatMinor int            `json:"nbformat_minor"`
}

type notebookCell struct {
	ID             string         `json:"id,omitempty"`
	CellType       string         `json:"cell_type"`
	Metadata       map[string]any `json:"metadata"`
	Source         []string       `json:"source"`
	Outputs        []any          `json:"outputs,omitempty"`
	ExecutionCount *int64         `json:"execution_count,omitempty"`
}

func blankNotebook() notebookDocument {
	return notebookDocument{
		Cells: []notebookCell{{
			ID: "cell-1", CellType: "code", Metadata: map[string]any{},
			Source: []string{"# Your first cell\n"}, Outputs: []any{},
		}},
		Metadata: map[string]any{
			"kernelspec": map[string]any{
				"display_name": "Python 3", "language": "python", "name": "python3",
			},
			"language_info": map[string]any{"name": "python"},
		},
		NBFormat: 4, NBFormatMinor: 5,
	}
}

func cleanNotebookPath(raw string) (string, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if raw == "" {
		raw = "notebook.ipynb"
	}
	if !strings.HasSuffix(strings.ToLower(raw), ".ipynb") {
		raw += ".ipynb"
	}
	clean := strings.TrimPrefix(path.Clean("/"+raw), "/")
	if clean == "." || strings.HasPrefix(clean, "../") {
		return "", errors.New("give the notebook a path inside the project")
	}
	for _, part := range strings.Split(clean, "/") {
		if !projectfs.ValidName(part) {
			return "", errors.New("notebook folders and names use letters, numbers, dots, dashes, and underscores")
		}
	}
	return clean, nil
}

func (s *Server) createNotebook(w http.ResponseWriter, r *http.Request) {
	p, project, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	notebookPath, err := cleanNotebookPath(body.Path)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if _, err := project.ReadFile(notebookPath); err == nil {
		fail(w, 409, "A notebook already exists at that path.")
		return
	}
	raw, _ := json.MarshalIndent(blankNotebook(), "", "  ")
	if err := project.WriteFile(notebookPath, string(raw)+"\n"); err != nil {
		fsError(w, err)
		return
	}
	_ = s.store.TouchProjectID(p.ID)
	s.hub.Publish("tree.changed", map[string]string{"project": p.Name, "path": notebookPath})
	writeJSON(w, 201, map[string]string{"path": notebookPath})
}

func (s *Server) notebookStatus(w http.ResponseWriter, r *http.Request) {
	_, project, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	writeJSON(w, 200, s.notebooks.Status(project.Root))
}

func (s *Server) openJupyter(w http.ResponseWriter, r *http.Request) {
	_, project, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	if !s.cfg.Worker.Enabled || !s.cfg.Worker.AllowJobs {
		fail(w, 403, "This machine is not accepting notebook kernels.")
		return
	}
	status, err := s.notebooks.Open(project.Root)
	if err != nil {
		code := 500
		if errors.Is(err, notebook.ErrNoJupyter) {
			code = 412
		}
		fail(w, code, err.Error())
		return
	}
	writeJSON(w, 200, status)
}

func (s *Server) proxyJupyter(w http.ResponseWriter, r *http.Request) {
	proxy, err := s.notebooks.Proxy(r.PathValue("id"))
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	proxy.ServeHTTP(w, r)
}

func (s *Server) executeNotebookCell(w http.ResponseWriter, r *http.Request) {
	fail(w, http.StatusGone,
		"Cell execution moved to Jupyter Server. Open this notebook from the Notebooks page.")
}

func (s *Server) interruptNotebook(w http.ResponseWriter, r *http.Request) {
	fail(w, http.StatusGone, "Interrupt kernels from the Jupyter interface.")
}

func (s *Server) restartNotebook(w http.ResponseWriter, r *http.Request) {
	_, project, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	if err := s.notebooks.Restart(project.Root); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "restarted"})
}
