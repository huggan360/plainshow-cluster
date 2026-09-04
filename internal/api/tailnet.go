package api

import (
	"net/http"

	"github.com/huggan360/plainshow-cluster/internal/tailnet"
)

// getTailnet reports what the private-network client daemon knows.
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
		response["advice"] = "Re-run the PlainShow installer to add private networking."
	case !status.Running:
		response["advice"] = "Sign in with your PlainShow account. Private networking " +
			"is enrolled automatically and will retry in the background."
	}
	writeJSON(w, http.StatusOK, response)
}
