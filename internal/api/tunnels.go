package api

import (
	"context"
	"strings"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/store"
	"github.com/huggan360/plainshow-cluster/internal/tunnel"
)

// StartTunnels opens an outbound connection to the coordinator of every network
// this machine joined but does not itself coordinate.
//
// This is what makes a worker on an ordinary home connection usable. It has no
// address anybody outside can open, so it connects out and keeps that
// connection; work then arrives down it. Nothing is forwarded, nothing is
// configured on a router, and the machine can move networks without anybody
// updating an address.
func (s *Server) StartTunnels(ctx context.Context) {
	for _, membership := range s.cfg.Memberships {
		endpoint := coordinatorEndpoint(membership)
		if endpoint == "" {
			continue // this node is the coordinator, or has no address for one
		}
		fingerprint, err := s.coordinatorFingerprint(membership.ID)
		if err != nil || fingerprint == "" {
			continue
		}

		networkID := membership.ID
		dialer := &tunnel.Dialer{
			Endpoint:    endpoint,
			Fingerprint: fingerprint,
			NetworkID:   networkID,
			Device:      s.device,
			Handler:     s.TunnelHandler(),
			OnState: func(up bool) {
				s.hub.Publish("mesh.tunnel", map[string]any{
					"network": networkID, "connected": up,
				})
			},
		}
		go dialer.Run(ctx)
	}
}

// coordinatorEndpoint returns the address to dial for a membership, or "" when
// this node coordinates the network itself.
func coordinatorEndpoint(membership config.MembershipConfig) string {
	for _, role := range membership.Roles {
		if role == config.RoleMaster || role == config.RoleController {
			return "" // we are the one others dial
		}
	}
	for _, endpoint := range membership.Coordinator {
		if trimmed := strings.TrimSpace(endpoint); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// coordinatorFingerprint finds the pinned certificate of the machine that
// coordinates a network. There is no certificate authority here: a peer is
// trusted because its certificate matches the one recorded at enrolment.
func (s *Server) coordinatorFingerprint(networkID string) (string, error) {
	nodes, err := s.store.NetworkNodes(networkID)
	if err != nil {
		return "", err
	}
	for _, node := range nodes {
		if node.NodeID == s.cfg.Node.ID {
			continue
		}
		if node.Fingerprint != "" && coordinates(node) {
			return node.Fingerprint, nil
		}
	}
	// Fall back to any enrolled peer with an address: on a two-machine cluster
	// the other machine is the coordinator whether or not it says so.
	for _, node := range nodes {
		if node.NodeID != s.cfg.Node.ID && node.Fingerprint != "" && node.Address != "" {
			return node.Fingerprint, nil
		}
	}
	return "", nil
}

func coordinates(node store.NetworkNode) bool {
	for _, role := range node.Roles {
		if role == string(config.RoleMaster) || role == string(config.RoleController) {
			return true
		}
	}
	return false
}
