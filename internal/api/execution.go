package api

import (
	"errors"

	"github.com/huggan360/plainshow-cluster/internal/store"
)

// Explicit CLI targets override the desktop's selected compute network. The
// project itself never selects a run target, and missing selection fails closed.
func (s *Server) executionNetwork(requested string) (string, error) {
	id := requested
	if id == "" {
		id = s.cfg.ActiveNetwork
	}
	if id == "" || !hasMembership(s.cfg, id) {
		return "", errors.New("Choose an active network on the Networks page before running or testing a project")
	}
	for _, membership := range s.cfg.Memberships {
		if membership.ID == id && !membership.Enabled {
			return "", errors.New("The selected network is paused; enable it before running tasks")
		}
		if membership.ID == id && membership.AccountRole != "" && !store.PermissionsForRole(membership.AccountRole).RunJobs {
			return "", errors.New("Your role in the selected network does not allow running tasks")
		}
	}
	return id, nil
}
