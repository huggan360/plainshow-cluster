package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/identity"
	"github.com/huggan360/plainshow-cluster/internal/mesh"
	"github.com/huggan360/plainshow-cluster/internal/store"
	"github.com/huggan360/plainshow-cluster/internal/sysinfo"
	"github.com/huggan360/plainshow-cluster/internal/tailnet"
)

func (s *Server) listNetworks(w http.ResponseWriter, r *http.Request) {
	networks, err := s.store.Networks(s.cfg.AccountID())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"active": s.cfg.ActiveNetwork, "networks": networks,
	})
}

func (s *Server) networkControllers(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.NetworkControllers(r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	for index := range items {
		items[index].PublicKey = ""
		items[index].Fingerprint = ""
		items[index].CollabToken = ""
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createNetworkInvite(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !hasMembership(s.cfg, id) {
		fail(w, 404, "This device does not belong to that network.")
		return
	}
	member, err := s.store.NetworkMember(id, s.cfg.AccountID())
	if err != nil || !member.Permissions.ManageMembers {
		fail(w, 403, "Your account cannot invite accounts or devices to this network.")
		return
	}
	network, err := s.store.NetworkByID(id)
	if err != nil {
		fail(w, 404, "No such network.")
		return
	}
	var body struct {
		Endpoint string `json:"endpoint"`
		Role     string `json:"role"`
		Minutes  int    `json:"minutes"`
		MaxUses  int    `json:"max_uses"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if body.Endpoint == "" {
		body.Endpoint = advertisedEndpointFor(s.cfg, tailnet.Probe(r.Context()))
	}
	if body.Role == "" {
		body.Role = store.NetworkMember
	}
	if !store.ValidNetworkRole(body.Role) {
		fail(w, 400, "Unknown network role.")
		return
	}
	if body.Role == store.NetworkOwner {
		fail(w, 400, "Ownership cannot be granted by a device invitation. Invite an administrator or member instead.")
		return
	}
	if body.Minutes <= 0 {
		body.Minutes = 15
	}
	if body.Minutes > 24*60 {
		fail(w, 400, "A join code may be valid for at most 24 hours.")
		return
	}
	if body.MaxUses <= 0 {
		body.MaxUses = 1
	}
	invite, tokenHash, err := mesh.NewInvite(id, network.Name, body.Endpoint, s.fingerprint,
		body.Role, time.Duration(body.Minutes)*time.Minute)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if err := s.store.CreateInvitation(store.Invitation{ID: invite.ID, NetworkID: id,
		TokenHash: tokenHash, Role: body.Role, Expires: invite.Expires,
		MaxUses: body.MaxUses, CreatedBy: s.cfg.Node.ID}); err != nil {
		fail(w, 500, err.Error())
		return
	}
	code, err := invite.Encode()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"code": code, "expires_at": invite.Expires,
		"endpoint": invite.Endpoint, "role": invite.Role, "max_uses": body.MaxUses})
}

func (s *Server) joinNetwork(w http.ResponseWriter, r *http.Request) {
	if s.usesCentralAccounts() && s.cfg.Account.ID == "" {
		fail(w, 409, "Sign in with your Plainshow account before joining a network.")
		return
	}
	var body struct {
		Code     string `json:"code"`
		Endpoint string `json:"endpoint"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	invite, err := mesh.DecodeInvite(body.Code)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if invite.TailnetAuthKey != "" {
		if err := tailnet.Up(r.Context(), invite.TailnetAuthKey, s.cfg.Node.Name,
			invite.TailnetLoginServer); err != nil {
			fail(w, 502, "Could not join the network tailnet: "+err.Error())
			return
		}
	}
	if hasMembership(s.cfg, invite.NetworkID) {
		fail(w, 409, "This device already belongs to that network.")
		return
	}
	if body.Endpoint == "" {
		body.Endpoint = advertisedEndpointFor(s.cfg, tailnet.Probe(r.Context()))
	}
	info := sysinfo.Probe(s.layout.Root)
	account, err := s.store.Account(s.cfg.AccountID())
	if err != nil {
		fail(w, 409, "This device has no account identity. Sign in and try again.")
		return
	}
	accountToken, err := config.LoadAccountToken(s.layout)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	client := mesh.NewClient(invite.Endpoint, invite.Fingerprint, invite.NetworkID, s.device)
	request := joinRequest{Token: invite.Token, NodeID: s.device.ID, Name: s.cfg.Node.Name,
		PublicKey: base64.RawURLEncoding.EncodeToString(s.device.Public), Fingerprint: s.fingerprint,
		Endpoint: body.Endpoint, Account: account, AccountToken: accountToken,
		Info: info, Policy: policyMap(s.cfg.Worker)}
	var response joinResponse
	if err := client.JSON("POST", "/mesh/v1/join/"+invite.NetworkID, request, &response, false); err != nil {
		fail(w, 502, "Could not join the network: "+err.Error())
		return
	}
	if len(response.ManagementKey) < 32 {
		fail(w, 502, "The inviting node is too old to share this network's management key. Update it and create a new code.")
		return
	}
	membership := config.MembershipConfig{ID: response.Network.ID, Name: response.Network.Name,
		Roles: []config.Role{config.RoleWorker}, Enabled: true,
		AccountRole: response.Role, ManagementKey: response.ManagementKey,
		Coordinator: []string{invite.Endpoint}, Policy: s.cfg.Worker}
	s.cfg.Memberships = append(s.cfg.Memberships, membership)
	s.cfg.SetActiveNetwork(membership.ID)
	s.cfg.Network.Advertise = strings.TrimRight(body.Endpoint, "/")
	if err := config.Save(s.layout, s.cfg); err != nil {
		fail(w, 500, err.Error())
		return
	}
	for _, node := range response.Nodes {
		node.IsSelf = node.NodeID == s.device.ID
		if err := s.store.UpsertNetworkNode(node); err != nil {
			fail(w, 500, err.Error())
			return
		}
	}
	if err := s.recordLocalMembership(membership, response.Network); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.hub.Publish("networks.changed", membership)
	go s.checkInAccountServer(context.Background())
	writeJSON(w, 201, map[string]any{"network": response.Network, "role": response.Role,
		"nodes": response.Nodes, "active": membership.ID})
}

func (s *Server) createNetwork(w http.ResponseWriter, r *http.Request) {
	if s.usesCentralAccounts() && s.cfg.Account.ID == "" {
		fail(w, 409, "Sign in with your Plainshow account before creating a network.")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len(body.Name) > 64 {
		fail(w, 400, "A network name must be between 1 and 64 characters.")
		return
	}
	membership := config.MembershipConfig{
		ID: config.NewID(), Name: body.Name,
		Roles: []config.Role{config.RoleWorker}, AccountRole: store.NetworkOwner,
		ManagementKey: config.NewSecret(), Enabled: true, Policy: s.cfg.Worker,
	}
	s.cfg.Memberships = append(s.cfg.Memberships, membership)
	s.cfg.SetActiveNetwork(membership.ID)
	if err := config.Save(s.layout, s.cfg); err != nil {
		fail(w, 500, err.Error())
		return
	}
	if err := s.recordLocalMembership(membership, store.Network{
		ID: membership.ID, Name: membership.Name, OwnerAccountID: s.cfg.AccountID(),
	}); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.hub.Publish("networks.changed", membership)
	go s.checkInAccountServer(context.Background())
	writeJSON(w, 201, membership)
}

func (s *Server) activateNetwork(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.cfg.SetActiveNetwork(id) {
		fail(w, 404, "This device does not belong to that network.")
		return
	}
	if err := config.Save(s.layout, s.cfg); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.hub.Publish("network.active", s.cfg.ActiveMembership())
	writeJSON(w, 200, s.cfg.ActiveMembership())
}

func (s *Server) networkNodes(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !hasMembership(s.cfg, id) {
		fail(w, 404, "This device does not belong to that network.")
		return
	}
	nodes, err := s.store.NetworkNodes(id)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, nodes)
}

func (s *Server) networkMembers(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !hasMembership(s.cfg, id) {
		fail(w, 404, "This device does not belong to that network.")
		return
	}
	members, err := s.store.NetworkMembers(id)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, members)
}

func (s *Server) updateNetworkPolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !hasMembership(s.cfg, id) {
		fail(w, 404, "This device does not belong to that network.")
		return
	}
	var body struct {
		Enabled *bool                `json:"enabled"`
		Policy  *config.WorkerConfig `json:"policy"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	for i := range s.cfg.Memberships {
		membership := &s.cfg.Memberships[i]
		if membership.ID != id {
			continue
		}
		if body.Enabled != nil {
			membership.Enabled = *body.Enabled
		}
		if body.Policy != nil {
			membership.Policy = *body.Policy
		}
		if id == s.cfg.ActiveNetwork {
			s.cfg.SetActiveNetwork(id)
		}
		if err := config.Save(s.layout, s.cfg); err != nil {
			fail(w, 500, err.Error())
			return
		}
		network, err := s.store.NetworkByID(membership.ID)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		if err := s.recordLocalMembership(*membership, network); err != nil {
			fail(w, 500, err.Error())
			return
		}
		s.hub.Publish("network.policy", *membership)
		writeJSON(w, 200, membership)
		return
	}
}

func (s *Server) updateNetworkManagementKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !hasMembership(s.cfg, id) {
		fail(w, 404, "This device does not belong to that network.")
		return
	}
	var body struct {
		ManagementKey string `json:"management_key"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	body.ManagementKey = strings.TrimSpace(body.ManagementKey)
	if len(body.ManagementKey) < 32 {
		fail(w, 400, "The management key is incomplete.")
		return
	}
	for i := range s.cfg.Memberships {
		if s.cfg.Memberships[i].ID == id {
			s.cfg.Memberships[i].ManagementKey = body.ManagementKey
			if err := config.Save(s.layout, s.cfg); err != nil {
				fail(w, 500, err.Error())
				return
			}
			go s.checkInAccountServer(context.Background())
			writeJSON(w, 200, map[string]string{"status": "updated"})
			return
		}
	}
	fail(w, 404, "This device does not belong to that network.")
}

func hasMembership(cfg *config.Config, id string) bool {
	for _, membership := range cfg.Memberships {
		if membership.ID == id {
			return true
		}
	}
	return false
}

func (s *Server) recordLocalMembership(membership config.MembershipConfig, network store.Network) error {
	device, err := identity.LoadOrCreate(s.layout.DeviceKey())
	if err != nil {
		return err
	}
	public := base64.RawURLEncoding.EncodeToString(device.Public)
	account := store.Account{ID: s.cfg.AccountID(), Username: s.cfg.Account.Username,
		DisplayName: s.cfg.Account.DisplayName}
	if account.Username == "" {
		account.Username, account.DisplayName, account.PublicKey = s.cfg.Node.Name, s.cfg.Node.Name, public
	}
	if err := s.store.UpsertAccount(account); err != nil {
		return err
	}
	if err := s.store.UpsertNetwork(network); err != nil {
		return err
	}
	role := membership.AccountRole
	if role == "" {
		role = store.NetworkOwner
	}
	if err := s.store.AddNetworkMember(membership.ID, account.ID, role); err != nil {
		return err
	}
	policyRaw, _ := json.Marshal(membership.Policy)
	policy := map[string]any{}
	_ = json.Unmarshal(policyRaw, &policy)
	roles := make([]string, 0, len(membership.Roles))
	for _, role := range membership.Roles {
		roles = append(roles, string(role))
	}
	info := sysinfo.Probe(s.layout.Root)
	return s.store.UpsertNetworkNode(store.NetworkNode{
		NetworkID: membership.ID, NodeID: s.cfg.Node.ID, Name: s.cfg.Node.Name,
		Roles: roles, OS: info.OS, Arch: info.Arch, PublicKey: public,
		Fingerprint: s.fingerprint,
		Address:     advertisedEndpointFor(s.cfg, tailnet.Probe(context.Background())),
		Policy:      policy, IsSelf: true, LastSeen: store.Now(),
		Capacity: map[string]any{
			"cpu_cores": info.CPUCores, "ram_total_mb": info.RAMTotalMB,
			"disk_total_gb": info.DiskTotalGB, "gpus": info.GPUs,
		},
	})
}
