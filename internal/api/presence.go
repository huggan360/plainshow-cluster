package api

// Whether this machine is available to the cluster.
//
// Distinct from the browser's connection, which is what the corner used to
// show: that told you whether the page could reach the node, which is rarely
// the question. This says whether other people's work may run here.
//
// Deliberately not saved. A machine is available when it starts, because that
// is what somebody who installed a cluster expects, and going offline is a
// decision about right now — the afternoon you need your GPU back — not a
// setting to discover months later and wonder who changed. Settings holds the
// durable version of the same idea.

import (
	"context"
	"net/http"
	"sync/atomic"
)

// availability is per-process and starts true. See the note above. It is not
// the same thing as peer presence, which is whether other machines can be
// reached from here.
type availability struct{ offline atomic.Bool }

// Available reports whether this machine will take part in the cluster.
func (s *Server) Available() bool { return !s.availability.offline.Load() }

func (s *Server) getPresence(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.presenceState())
}

func (s *Server) presenceState() map[string]any {
	// The device-wide policy is the other half: a machine set to refuse work in
	// Settings is unavailable however this flag is set, and saying otherwise
	// here would be a corner of the interface contradicting a page of it.
	return map[string]any{
		"available":   s.Available(),
		"accept_work": s.cfg.Worker.Enabled && s.cfg.Worker.AllowJobs,
		"resets":      true,
	}
}

// putPresence takes this machine in or out of the cluster.
func (s *Server) putPresence(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Available bool `json:"available"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.Available() == body.Available {
		writeJSON(w, http.StatusOK, s.presenceState())
		return
	}
	s.availability.offline.Store(!body.Available)

	// Going offline has to mean something. Ray is how other people's work
	// reaches this machine, so the reconciler is asked to act now rather than
	// leaving a raylet running for another fifteen seconds after somebody has
	// said stop.
	go s.reconcileRay(context.Background())
	s.hub.Publish("presence.changed", s.presenceState())
	writeJSON(w, http.StatusOK, s.presenceState())
}
