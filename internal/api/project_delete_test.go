package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

func TestDeleteProjectForgetsCentralMetadataBeforeLocalFiles(t *testing.T) {
	forgotten := false
	authority := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/projects/project-1" {
			http.NotFound(w, r)
			return
		}
		forgotten = true
		writeJSON(w, http.StatusOK, map[string]string{"status": "forgotten"})
	}))
	defer authority.Close()

	srv, _ := newTestServer(t)
	srv.cfg.Account.ID = "account-1"
	srv.cfg.Account.Server = authority.URL
	if err := config.SaveAccountToken(srv.layout, "account-token"); err != nil {
		t.Fatal(err)
	}
	project := store.Project{ID: "project-1", Name: "demo"}
	if err := srv.store.CreateProject(&project); err != nil {
		t.Fatal(err)
	}
	dir := srv.projectDir(project)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/projects/project-1", nil)
	request.SetPathValue("name", "project-1")
	srv.deleteProject(recorder, request)
	if recorder.Code != http.StatusOK || !forgotten {
		t.Fatalf("delete = %d %q, central=%v", recorder.Code, recorder.Body.String(), forgotten)
	}
	if _, err := srv.store.ProjectByID(project.ID); err == nil {
		t.Fatal("local project row survived deletion")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("project directory still exists: %v", err)
	}
}

func TestDeleteProjectKeepsFilesWhenCentralForgetFails(t *testing.T) {
	authority := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"temporary failure"}`, http.StatusServiceUnavailable)
	}))
	defer authority.Close()

	srv, _ := newTestServer(t)
	srv.cfg.Account.ID = "account-1"
	srv.cfg.Account.Server = authority.URL
	if err := config.SaveAccountToken(srv.layout, "account-token"); err != nil {
		t.Fatal(err)
	}
	project := store.Project{ID: "project-1", Name: "demo"}
	if err := srv.store.CreateProject(&project); err != nil {
		t.Fatal(err)
	}
	dir := srv.projectDir(project)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/projects/project-1", nil)
	request.SetPathValue("name", "project-1")
	srv.deleteProject(recorder, request)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("delete returned %d: %s", recorder.Code, recorder.Body.String())
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("files were removed before central deletion succeeded: %v", err)
	}
}
