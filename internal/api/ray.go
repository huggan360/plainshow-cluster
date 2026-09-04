package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/ray"
	"github.com/huggan360/plainshow-cluster/internal/tailnet"
)

// rayHead returns the head recorded for a network, if any.
func (s *Server) rayHead(networkID string) string {
	for _, membership := range s.cfg.Memberships {
		if membership.ID == networkID {
			return membership.RayHead
		}
	}
	return ""
}

// setRayHead records which machine runs a network's Ray head.
func (s *Server) setRayHead(networkID, head string) error {
	for i := range s.cfg.Memberships {
		if s.cfg.Memberships[i].ID == networkID {
			s.cfg.Memberships[i].RayHead = head
			return config.Save(s.layout, s.cfg)
		}
	}
	return nil
}

// getRay reports the state of this network's Ray cluster.
func (s *Server) getRay(w http.ResponseWriter, r *http.Request) {
	head := s.rayHead(s.cfg.ActiveNetwork)
	status := ray.Probe(r.Context(), ray.DashboardURL(hostOf(head), ray.DefaultDashboard))
	response := map[string]any{
		"installed": status.Installed,
		"running":   status.Running,
		"head":      head,
		"is_head":   status.Head,
		"nodes":     status.Nodes,
		"total_cpu": status.TotalCPU,
		"total_gpu": status.TotalGPU,
		"detail":    status.Detail,
	}
	// Say what to do next, in the order somebody would hit it.
	switch {
	case !status.Installed:
		response["advice"] = "Install Ray on this machine: pip install 'ray[default]'"
	case head == "":
		response["advice"] = "No Ray cluster is running for this network yet. " +
			"Start one on any machine — the others attach to it."
	case !status.Running:
		response["advice"] = "The Ray head for this network is not answering. " +
			"Start it again on " + head + ", or start one here instead."
	}
	writeJSON(w, http.StatusOK, response)
}

// hostOf strips a port from an address.
func hostOf(address string) string {
	if address == "" {
		return ""
	}
	if host, _, found := strings.Cut(address, ":"); found {
		return host
	}
	return address
}

// startRay brings this machine into the network's Ray cluster: as the head when
// there is none, otherwise attached to the existing one.
func (s *Server) startRay(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Head bool `json:"head"`
	}
	_ = decode(r, &body)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	// Ray must bind to the address the other machines reach this one on, which
	// is its tailnet address whenever there is one.
	address := tailnet.Probe(ctx).Self.Address
	if address == "" {
		fail(w, http.StatusConflict,
			"This machine has no private network address yet, so the others could not "+
				"reach its Ray cluster. Join a network first.")
		return
	}

	existing := s.rayHead(s.cfg.ActiveNetwork)
	if body.Head || existing == "" {
		if err := ray.StartHead(ctx, address, ray.DefaultPort, ray.DefaultDashboard); err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		head := address + ":" + itoa(ray.DefaultPort)
		if err := s.setRayHead(s.cfg.ActiveNetwork, head); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.hub.Publish("ray.changed", map[string]any{"head": head})
		writeJSON(w, http.StatusOK, map[string]any{"head": head, "role": "head"})
		return
	}

	if err := ray.StartWorker(ctx, address, existing); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	s.hub.Publish("ray.changed", map[string]any{"head": existing})
	writeJSON(w, http.StatusOK, map[string]any{"head": existing, "role": "worker"})
}

// stopRay takes this machine out of the cluster.
func (s *Server) stopRay(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := ray.Stop(ctx); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	// If this machine was the head, the network no longer has one.
	head := s.rayHead(s.cfg.ActiveNetwork)
	if address := tailnet.Probe(ctx).Self.Address; address != "" &&
		strings.HasPrefix(head, address+":") {
		_ = s.setRayHead(s.cfg.ActiveNetwork, "")
	}
	s.hub.Publish("ray.changed", map[string]any{"head": s.rayHead(s.cfg.ActiveNetwork)})
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// getRayJobs lists what Ray is running. These are Ray's jobs, as Ray reports
// them; Plainshow keeps no parallel job model for work Ray owns.
func (s *Server) getRayJobs(w http.ResponseWriter, r *http.Request) {
	head := s.rayHead(s.cfg.ActiveNetwork)
	if head == "" {
		writeJSON(w, http.StatusOK, map[string]any{"jobs": []any{}, "running": false})
		return
	}
	jobs, err := ray.Jobs(r.Context(), ray.DashboardURL(hostOf(head), ray.DefaultDashboard))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"jobs": []any{}, "running": false, "detail": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "running": true})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
