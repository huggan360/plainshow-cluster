package api

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/controller"
	"github.com/huggan360/plainshow-cluster/internal/mesh"
	"github.com/huggan360/plainshow-cluster/internal/store"
	"github.com/huggan360/plainshow-cluster/internal/tailnet"
)

func (s *Server) createControllerInvite(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	network, err := s.store.NetworkByID(id)
	if err != nil || !hasMembership(s.cfg, id) {
		fail(w, 404, "No such network.")
		return
	}
	member, err := s.store.NetworkMember(id, s.cfg.AccountID())
	if err != nil || !member.Permissions.ManageNetwork {
		fail(w, 403, "Your account cannot attach a controller to this network.")
		return
	}
	endpoint := advertisedEndpointFor(s.cfg, tailnet.Probe(r.Context()))
	invite, tokenHash, err := mesh.NewInvite(id, network.Name, endpoint,
		s.fingerprint, "controller", 15*time.Minute)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if err := s.store.CreateInvitation(store.Invitation{ID: invite.ID, NetworkID: id,
		TokenHash: tokenHash, Role: "controller", Expires: invite.Expires,
		MaxUses: 1, CreatedBy: s.cfg.AccountID()}); err != nil {
		fail(w, 500, err.Error())
		return
	}
	code, err := invite.Encode()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"code": code, "expires_at": invite.Expires})
}

func (s *Server) acceptControllerJoin(w http.ResponseWriter, r *http.Request) {
	networkID := r.PathValue("network")
	var body controller.EnrollmentRequest
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	invitation, err := s.store.ConsumeInvitation(networkID, mesh.TokenHash(body.Token))
	if err != nil || invitation.Role != "controller" {
		fail(w, 403, "That controller code is invalid, expired, or already used.")
		return
	}
	public, err := base64.RawURLEncoding.DecodeString(body.PublicKey)
	if err != nil || len(public) != 32 || body.ID == "" || strings.TrimSpace(body.Address) == "" {
		fail(w, 400, "The controller supplied an invalid identity or address.")
		return
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		fail(w, 500, err.Error())
		return
	}
	collabToken := base64.RawURLEncoding.EncodeToString(random)
	item := store.NetworkController{NetworkID: networkID, ID: body.ID, Name: body.Name,
		PublicKey: body.PublicKey, Fingerprint: body.Fingerprint,
		Address: strings.TrimRight(body.Address, "/"), CollabToken: collabToken,
		LastSeen: store.Now()}
	if err := s.store.UpsertNetworkController(item); err != nil {
		fail(w, 500, err.Error())
		return
	}
	network, err := s.store.NetworkByID(networkID)
	if err != nil {
		fail(w, 404, "No such network.")
		return
	}
	nodes, _ := s.store.NetworkNodes(networkID)
	writeJSON(w, 201, controller.EnrollmentResponse{Network: network, Nodes: nodes, CollabToken: collabToken})
}

func (s *Server) networkControllers(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.NetworkControllers(r.PathValue("id"))
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	for i := range items {
		items[i].PublicKey = ""
		items[i].CollabToken = ""
	}
	writeJSON(w, 200, items)
}
