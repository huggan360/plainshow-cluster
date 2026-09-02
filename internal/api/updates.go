package api

import (
	"context"
	"net/http"
	"time"
)

func (s *Server) updateStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.updater.Status())
}

func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	status, err := s.updater.Check(ctx)
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, status)
}

// updateApply installs a found update and restarts into it.
//
// The response is sent before the restart, because a process replaced by exec
// never gets to write one. The browser learns the node is back through its
// event socket reconnecting.
func (s *Server) updateApply(w http.ResponseWriter, r *http.Request) {
	status := s.updater.Status()
	if status.Release == nil || !status.Available {
		fail(w, 409, "There is no update to install. Check for one first.")
		return
	}

	// Detach from the request: the install outlives the response.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	release := status.Release

	if err := s.updater.Apply(ctx, release); err != nil {
		cancel()
		fail(w, 502, err.Error())
		return
	}

	writeJSON(w, 200, map[string]any{
		"status":  "installed",
		"version": release.Version,
		"restart": true,
	})

	go func() {
		defer cancel()
		// Let the response flush and the browser see it before the process is
		// replaced under it.
		time.Sleep(700 * time.Millisecond)
		if err := s.updater.Restart(); err != nil {
			s.hub.Publish("update.progress", map[string]any{
				"stage": "restart-failed", "error": err.Error(),
			})
		}
	}()
}
