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
	if snapshot.Content != content {
		if err := fsys.WriteFile(rel, snapshot.Content); err != nil {
			fail(w, 500, "The latest collaborative version is safe in SQLite, but the project file could not be repaired: "+err.Error())
			return
		}
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
		if err != nil || p.NetworkID != op.NetworkID {
			return
		}
		content, err := fsys.ReadFile(op.Path)
		if err != nil {
			return
		}
		// Project ids are local database identities. Two machines can clone the
		// same named project in the same network and legitimately assign it
		// different ids. Map a controller-relayed operation onto this clone's
		// identity, and cap its unrelated revision clock to what this clone has.
		if op.ProjectID != p.ID {
			snapshot, openErr := s.collab.Open(p.NetworkID, p.ID, op.Path, content)
			if openErr != nil {
				return
			}
			op.ProjectID = p.ID
			if op.Base > snapshot.Revision {
				op.Base = snapshot.Revision
			}
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
		var presence struct {
			NetworkID string `json:"network_id"`
			ProjectID string `json:"project_id"`
			Project   string `json:"project"`
			Path      string `json:"path"`
			ClientID  string `json:"client_id"`
			Name      string `json:"name"`
			From      int    `json:"from"`
			To        int    `json:"to"`
		}
		if json.Unmarshal(message.Data, &presence) != nil {
			return
		}
		p, _, err := s.project(presence.Project)
		if err == nil && p.NetworkID == presence.NetworkID {
			presence.ProjectID = p.ID
			s.hub.Publish("collab.presence", presence)
		}
	}
}
