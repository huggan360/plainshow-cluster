package api

import (
	"net/http"

	"github.com/huggan360/plainshow-cluster/internal/tailnet"
)

// getTailnet reports what the tailscale daemon knows.
//
// This is the page somebody lands on when two machines cannot see each other,
// so it answers the three questions in order: is it installed, is it signed in,
// and is each peer reached directly or through a relay.
func (s *Server) getTailnet(w http.ResponseWriter, r *http.Request) {
	status := tailnet.Probe(r.Context())
	response := map[string]any{
		"installed": status.Installed,
		"running":   status.Running,
		"state":     status.State,
		"detail":    status.Detail,
		"self":      status.Self,
		"peers":     status.Peers,
	}
	// Say what to do, not just what is wrong.
	switch {
	case !status.Installed:
		response["advice"] = "Install tailscale on this machine to reach machines on " +
			"other networks. Without it, a cluster only works where the machines " +
			"can already reach each other."
	case !status.Running:
		response["advice"] = "Tailscale is installed but not connected. Joining a " +
			"network with a join code will sign it in."
	}
	writeJSON(w, http.StatusOK, response)
}
