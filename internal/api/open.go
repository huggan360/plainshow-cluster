package api

import (
	"context"
	"net/http"
	"os/exec"
	"path/filepath"
	"time"
)

// editors are tried in order for "open in editor". The first one present wins.
var editors = []struct{ command, name string }{
	{"code", "VS Code"},
	{"codium", "VSCodium"},
	{"code-insiders", "VS Code Insiders"},
	{"zed", "Zed"},
	{"subl", "Sublime Text"},
}

// detectEditor returns the first editor installed on this machine.
func detectEditor() (command, name string, found bool) {
	for _, editor := range editors {
		if _, err := exec.LookPath(editor.command); err == nil {
			return editor.command, editor.name, true
		}
	}
	return "", "", false
}

// openTargets tells the interface which buttons are worth showing. Offering
// "Open in VS Code" on a machine without it would be a button that fails.
func (s *Server) openTargets(w http.ResponseWriter, r *http.Request) {
	command, name, found := detectEditor()
	_, hasFileManager := lookAny("xdg-open", "gio", "nautilus", "thunar", "dolphin")
	writeJSON(w, http.StatusOK, map[string]any{
		"editor":         found,
		"editor_name":    name,
		"editor_command": command,
		"file_manager":   hasFileManager,
	})
}

func lookAny(candidates ...string) (string, bool) {
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, true
		}
	}
	return "", false
}

// openProject opens a project's folder in an editor or a file manager.
//
// The path is always resolved from the named project rather than taken from the
// request, so this cannot be talked into opening somewhere else. It only ever
// runs on the machine serving the interface, which is the machine the folder is
// on.
func (s *Server) openProject(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, http.StatusNotFound, "No such project.")
		return
	}
	var body struct {
		In string `json:"in"` // "editor" or "files"
	}
	_ = decode(r, &body)

	dir := s.projectDir(project)
	if _, err := filepath.Abs(dir); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	var command string
	var args []string
	switch body.In {
	case "files":
		opener, found := lookAny("xdg-open", "gio", "nautilus", "thunar", "dolphin")
		if !found {
			fail(w, http.StatusNotFound,
				"No file manager was found on this machine.")
			return
		}
		command = opener
		if filepath.Base(opener) == "gio" {
			args = []string{"open", dir}
		} else {
			args = []string{dir}
		}
	default:
		editor, name, found := detectEditor()
		if !found {
			fail(w, http.StatusNotFound,
				"No editor was found on this machine. Install VS Code, or open the "+
					"folder yourself: "+dir)
			return
		}
		command, args = editor, []string{dir}
		_ = name
	}

	// Detached: a desktop application outlives the request that started it, and
	// waiting for an editor to exit would hold the connection open for hours.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	cmd := exec.CommandContext(ctx, command, args...)
	if err := cmd.Start(); err != nil {
		cancel()
		fail(w, http.StatusInternalServerError, "Could not open it: "+err.Error())
		return
	}
	go func() { _ = cmd.Wait(); cancel() }()

	writeJSON(w, http.StatusOK, map[string]string{"opened": dir})
}
