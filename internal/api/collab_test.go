package api

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/collab"
	"github.com/huggan360/plainshow-cluster/internal/projectfs"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

func TestRelayedEditMapsOntoLocalProjectIdentity(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.cfg.EnsureMemberships()
	project := store.Project{ID: "local-project-id", NetworkID: srv.cfg.ActiveNetwork,
		Name: "shared", Description: "same Git clone on each node"}
	if err := srv.store.CreateProject(&project); err != nil {
		t.Fatal(err)
	}
	directory := srv.projectDir(project)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	fsys := projectfs.New(directory)
	if err := fsys.WriteFile("main.py", "ab"); err != nil {
		t.Fatal(err)
	}
	srv.collab = collab.New(srv.store)
	subscription := srv.hub.Subscribe()
	defer subscription.Close()

	payload, err := json.Marshal(map[string]any{
		"topic": "collab.op",
		"data": map[string]any{
			"network_id": srv.cfg.ActiveNetwork, "project_id": "remote-project-id",
			"project": "shared", "path": "main.py", "client_id": "remote-browser",
			"sequence": 1, "base_revision": 99, "from": 1, "to": 1, "insert": "X",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.handleClientEvent(payload)
	content, err := fsys.ReadFile("main.py")
	if err != nil || content != "aXb" {
		t.Fatalf("relayed content = %q, %v", content, err)
	}
	select {
	case event := <-subscription.C:
		operation, ok := event.Data.(collab.Operation)
		if event.Topic != "collab.op" || !ok || operation.ProjectID != project.ID || operation.Revision != 1 {
			t.Fatalf("mapped event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("mapped operation was not published")
	}
}

func TestLocalDocumentEditsIgnoreSameNamedLegacyProject(t *testing.T) {
	s, _ := newTestServer(t)
	s.collab = collab.New(s.store)
	for _, p := range []store.Project{{ID: "local", Name: "same"}, {ID: "shared", Name: "same", NetworkID: "old-network"}} {
		if err := s.store.CreateProject(&p); err != nil {
			t.Fatal(err)
		}
	}
	p, fsys, err := s.collabProject("", "local", "same")
	if err != nil || p.ID != "local" {
		t.Fatalf("local resolution: %+v %v", p, err)
	}
	if err := os.MkdirAll(fsys.Root, 0750); err != nil {
		t.Fatal(err)
	}
	if err := fsys.WriteFile("main.py", "ab"); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"topic": "collab.op", "data": collab.Operation{
		ProjectID: "local", Project: "same", Path: "main.py", ClientID: "local-browser",
		Sequence: 1, From: 1, To: 1, Insert: "X",
	}})
	s.handleClientEvent(raw)
	if content, err := fsys.ReadFile("main.py"); err != nil || content != "aXb" {
		t.Fatalf("local edit: %q %v", content, err)
	}
}
