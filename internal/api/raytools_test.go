package api

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/store"
)

func TestPresetCreatesNewFileAndNeverOverwritesUserCode(t *testing.T) {
	s := syncingNode(t, true, true)
	p := store.Project{ID: "preset-project", NetworkID: "net-1", Name: "repo"}
	if err := s.store.CreateProject(&p); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.projectDir(p), 0750); err != nil {
		t.Fatal(err)
	}
	create := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.SetPathValue("name", p.ID)
		w := httptest.NewRecorder()
		s.createRayPreset(w, r)
		return w
	}
	w := create(`{"mode":"mixed","filename":"ray_mix.py","limit":2}`)
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	file := filepath.Join(s.projectDir(p), "ray_mix.py")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "__HEAD__") || !strings.Contains(string(raw), `MODE = "mixed"`) {
		t.Fatal("unrendered preset")
	}
	if !strings.Contains(string(raw), `os.environ.get("RAY_ADDRESS", "")`) {
		t.Fatal("a project without a Ray head must not default to an unrelated local cluster")
	}
	if err := os.WriteFile(file, []byte("user code"), 0640); err != nil {
		t.Fatal(err)
	}
	if w := create(`{"mode":"cpu","filename":"ray_mix.py"}`); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	raw, _ = os.ReadFile(file)
	if string(raw) != "user code" {
		t.Fatal("overwrote user code")
	}
	if w := create(`{"mode":"cpu","filename":"../outside.py"}`); w.Code != 400 {
		t.Fatal("accepted traversal")
	}
}
