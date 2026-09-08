package api

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

func TestLocalProjectNeedsNoNetwork(t *testing.T) {
	s, _ := newTestServer(t)
	s.cfg.Memberships = nil
	s.cfg.ActiveNetwork = ""
	w := httptest.NewRecorder()
	s.createProject(w, httptest.NewRequest("POST", "/api/projects", strings.NewReader(`{"name":"standalone"}`)))
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var project store.Project
	if err := json.Unmarshal(w.Body.Bytes(), &project); err != nil || project.NetworkID != "" {
		t.Fatalf("project was assigned a network: %+v %v", project, err)
	}
	if s.projectDir(project) != filepath.Join(s.layout.Projects(), "standalone") {
		t.Fatal("local project placed below a network")
	}
	if _, err := os.Stat(filepath.Join(s.projectDir(project), "main.py")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.executionNetwork(""); err == nil {
		t.Fatal("a run without a selected network was allowed")
	}
}

func TestExecutionUsesSelectionAndPreservesProjectsAfterNetworkDeletion(t *testing.T) {
	s := syncingNode(t, true, true)
	s.cfg.Memberships = append(s.cfg.Memberships, config.MembershipConfig{ID: "net-2", Enabled: true, AccountRole: "owner"})
	p := store.Project{ID: "legacy", Name: "legacy", NetworkID: "net-1"}
	if err := s.store.CreateProject(&p); err != nil {
		t.Fatal(err)
	}
	dir := s.projectDir(p)
	if err := os.MkdirAll(dir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "code.py"), []byte("my code"), 0640); err != nil {
		t.Fatal(err)
	}
	s.cfg.ActiveNetwork = "net-2"
	if got, err := s.executionNetwork(""); err != nil || got != "net-2" {
		t.Fatalf("selection = %q, %v", got, err)
	}
	if got, err := s.executionNetwork("net-1"); err != nil || got != "net-1" {
		t.Fatalf("explicit CLI target = %q, %v", got, err)
	}
	if err := s.store.DeleteNetwork("net-1"); err != nil {
		t.Fatal(err)
	}
	kept, _, err := s.project(p.ID)
	if err != nil || s.projectDir(kept) != dir {
		t.Fatalf("deleted network changed project: %+v %v", kept, err)
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "code.py")); err != nil || string(raw) != "my code" {
		t.Fatal("network deletion lost code")
	}
	s.cfg.Memberships[1].AccountRole = "viewer"
	if _, err := s.executionNetwork(""); err == nil {
		t.Fatal("viewer could run tasks")
	}
	s.cfg.Memberships[1].AccountRole = "owner"
	s.cfg.Memberships[1].Enabled = false
	if _, err := s.executionNetwork(""); err == nil {
		t.Fatal("paused network accepted tasks")
	}
}

func TestPresetTargetsSelectedNetworkNotProjectLegacyScope(t *testing.T) {
	s := syncingNode(t, true, true)
	s.cfg.Memberships = append(s.cfg.Memberships, config.MembershipConfig{ID: "net-2", Enabled: true})
	s.cfg.ActiveNetwork = "net-2"
	p := store.Project{ID: "preset", Name: "legacy", NetworkID: "net-1"}
	if err := s.store.CreateProject(&p); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.projectDir(p), 0750); err != nil {
		t.Fatal(err)
	}
	if err := s.announceRayHead("net-2", "100.64.0.2:6379"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"mode":"cpu","filename":"selected.py"}`))
	r.SetPathValue("name", p.ID)
	w := httptest.NewRecorder()
	s.createRayPreset(w, r)
	if w.Code != 201 || !strings.Contains(w.Body.String(), "100.64.0.2:6379") {
		t.Fatalf("preset ignored selection: %d %s", w.Code, w.Body)
	}
}
