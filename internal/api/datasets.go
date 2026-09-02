package api

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/huggan360/plainshow-cluster/internal/store"
)

func (s *Server) listDatasets(w http.ResponseWriter, r *http.Request) {
	datasets, err := s.store.Datasets(s.cfg.ActiveNetwork)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	type item struct {
		store.Dataset
		Placements []store.DatasetPlacement `json:"placements"`
	}
	out := make([]item, 0, len(datasets))
	for _, dataset := range datasets {
		placements, _ := s.store.DatasetPlacements(dataset.ID)
		out = append(out, item{dataset, placements})
	}
	writeJSON(w, 200, out)
}

func (s *Server) registerDataset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Source  string `json:"source"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	body.Source = strings.TrimSpace(body.Source)
	if body.Source == "" {
		fail(w, 400, "Choose a source directory on this machine.")
		return
	}
	dataset, err := s.datasets.Register(s.cfg.ActiveNetwork, s.cfg.Node.ID, strings.TrimSpace(body.Name), strings.TrimSpace(body.Version), body.Source)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.hub.Publish("dataset.created", dataset)
	writeJSON(w, 201, dataset)
}

func (s *Server) materializeDataset(w http.ResponseWriter, r *http.Request) {
	dataset, err := s.store.Dataset(r.PathValue("id"))
	if err != nil || dataset.NetworkID != s.cfg.ActiveNetwork {
		fail(w, 404, "No such dataset.")
		return
	}
	var body struct {
		Target string `json:"target"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(body.Target) == "" {
		body.Target = filepath.Join(s.layout.Datasets(), "materialized", dataset.ID)
	}
	if err := s.datasets.Materialize(dataset, body.Target); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"path": body.Target})
}
