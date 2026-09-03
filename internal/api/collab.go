package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/huggan360/plainshow-cluster/internal/collab"
)

func (s *Server) openCollabDocument(w http.ResponseWriter, r *http.Request) {
	p, fsys, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	rel := strings.TrimSpace(r.URL.Query().Get("path"))
	content, err := fsys.ReadFile(rel)
	if err != nil {
		fsError(w, err)
		return
	}
	snapshot, err := s.collab.Open(p.NetworkID, p.ID, rel, content)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, snapshot)
}

func (s *Server) handleClientEvent(raw []byte) {
	var message struct {
		Topic string          `json:"topic"`
		Data  json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &message) != nil {
		return
	}
	switch message.Topic {
	case "collab.op":
		var op collab.Operation
		if json.Unmarshal(message.Data, &op) != nil {
			return
		}
		p, fsys, err := s.project(op.Project)
		if err != nil || p.ID != op.ProjectID || p.NetworkID != op.NetworkID {
			return
		}
		content, err := fsys.ReadFile(op.Path)
		if err != nil {
			return
		}
		applied, err := s.collab.Apply(op, content, func(next string) error { return fsys.WriteFile(op.Path, next) })
		if err != nil {
			if errors.Is(err, collab.ErrDuplicate) {
				return
			}
			s.hub.Publish("collab.reject", map[string]any{"client_id": op.ClientID,
				"sequence": op.Sequence, "project_id": op.ProjectID, "path": op.Path, "error": err.Error()})
			return
		}
		_ = s.store.TouchProjectID(p.ID)
		s.hub.Publish("collab.op", applied)
	case "collab.presence":
		var presence map[string]any
		if json.Unmarshal(message.Data, &presence) == nil {
			s.hub.Publish("collab.presence", presence)
		}
	}
}
