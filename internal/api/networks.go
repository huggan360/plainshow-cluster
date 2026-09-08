package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/accountclient"
	"github.com/huggan360/plainshow-cluster/internal/accountserver"
	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/gitrepo"
	"github.com/huggan360/plainshow-cluster/internal/identity"
	"github.com/huggan360/plainshow-cluster/internal/mesh"
	"github.com/huggan360/plainshow-cluster/internal/store"
	"github.com/huggan360/plainshow-cluster/internal/sysinfo"
	"github.com/huggan360/plainshow-cluster/internal/tailnet"
)

type networkSummary struct {
	store.Network
	ProjectCount int  `json:"project_count"`
	NodeCount    int  `json:"node_count"`
	GPUCount     int  `json:"gpu_count"`
	Enabled      bool `json:"enabled"`
	Active       bool `json:"active"`
}

func (s *Server) networkSummaries() ([]networkSummary, error) {
	networks, err := s.store.Networks(s.cfg.AccountID())
	if err != nil {
		return nil, err
	}
	out := make([]networkSummary, 0, len(networks))
	for _, network := range networks {
		projects, projectErr := s.store.ProjectsInNetwork(network.ID)
		if projectErr != nil {
			return nil, projectErr
		}
		nodes, nodeErr := s.liveNodes(network.ID)
		if nodeErr != nil {
			return nil, nodeErr
		}
		summary := networkSummary{Network: network, ProjectCount: len(projects), NodeCount: len(nodes)}
		summary.Active = network.ID == s.cfg.ActiveNetwork
		for _, membership := range s.cfg.Memberships {
			if membership.ID == network.ID {
				summary.Enabled = membership.Enabled
				break
			}
		}
		for _, node := range nodes {
			if nodeCapacityOnline(node, time.Now()) {
				summary.GPUCount += capacityGPUCount(node.Capacity)
			}
		}
		out = append(out, summary)
	}
	return out, nil
}

// allNetworkNodes returns account-wide device inventory without counting one
// physical machine once for every shared network. The newest observation wins.
func (s *Server) allNetworkNodes() ([]store.NetworkNode, error) {
	byID := make(map[string]store.NetworkNode)
	for _, membership := range s.cfg.Memberships {
		nodes, err := s.liveNodes(membership.ID)
		if err != nil {
			return nil, err
		}
		for _, node := range nodes {
			current, exists := byID[node.NodeID]
			if !exists || node.IsSelf || node.LastSeen > current.LastSeen {
				byID[node.NodeID] = node
			}
		}
	}
	out := make([]store.NetworkNode, 0, len(byID))
	for _, node := range byID {
		out = append(out, node)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsSelf != out[j].IsSelf {
			return out[i].IsSelf
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func capacityGPUCount(capacity map[string]any) int {
	raw, ok := capacity["gpus"]
	if !ok || raw == nil {
		return 0
	}
	switch values := raw.(type) {
	case []any:
		return len(values)
	case []sysinfo.GPU:
		return len(values)
	default:
		encoded, err := json.Marshal(raw)
		if err != nil {
			return 0
		}
		var gpus []sysinfo.GPU
		if json.Unmarshal(encoded, &gpus) != nil {
			return 0
		}
		return len(gpus)
	}
}

func (s *Server) listNetworks(w http.ResponseWriter, r *http.Request) {
	// A direct visit to Networks should not have to wait for the background
	// heartbeat. Pull account-owned memberships first and retain local data if
	// the management plane is temporarily unreachable.
	if client, token, authorityErr := s.authority(r.Context()); authorityErr == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		_, _ = s.AdoptAccountNetworks(ctx, client, token)
		cancel()
	}
	networks, err := s.networkSummaries()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"active": s.cfg.ActiveNetwork, "networks": networks,
	})
}

func (s *Server) networkDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !hasMembership(s.cfg, id) {
		fail(w, http.StatusNotFound, "This device does not belong to that network.")
		return
	}
	network, err := s.store.NetworkByID(id)
	if err != nil {
		fail(w, http.StatusNotFound, "No such network.")
		return
	}
	projects, err := s.store.Projects()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.decorateProjects(projects)
	nodes, err := s.liveNodes(id)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	members, err := s.store.NetworkMembers(id)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	controllers, _ := s.store.NetworkControllers(id)
	for index := range controllers {
		controllers[index].PublicKey = ""
		controllers[index].Fingerprint = ""
		controllers[index].CollabToken = ""
	}
	membership := config.MembershipConfig{}
	for _, item := range s.cfg.Memberships {
		if item.ID == id {
			membership = item
			membership.ManagementKey = ""
			break
		}
	}
	summary := networkSummary{Network: network, ProjectCount: len(projects), NodeCount: len(nodes),
		Enabled: membership.Enabled, Active: id == s.cfg.ActiveNetwork}
	for _, node := range nodes {
		if nodeCapacityOnline(node, time.Now()) {
			summary.GPUCount += capacityGPUCount(node.Capacity)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"network": summary, "membership": membership, "projects": projects,
		"nodes": nodes, "members": members, "controllers": controllers,
		"active": id == s.cfg.ActiveNetwork,
	})
}

// nodeCapacityOnline keeps summary capacity honest: stale inventory is useful
// for showing which machine belongs to a network, but it is not compute that a
// job can use right now. Peer telemetry normally refreshes every 30 seconds.
func nodeCapacityOnline(node store.NetworkNode, now time.Time) bool {
	if node.Online != nil {
		return *node.Online
	}
	if node.IsSelf {
		return true
	}
	seen, err := time.Parse(time.RFC3339Nano, node.LastSeen)
	return err == nil && now.Sub(seen) >= 0 && now.Sub(seen) < 2*time.Minute
}

type recentCommit struct {
	ProjectID string `json:"project_id"`
	Project   string `json:"project"`
	Branch    string `json:"branch"`
	Hash      string `json:"hash"`
	Short     string `json:"short"`
	Author    string `json:"author"`
	When      string `json:"when"`
	Subject   string `json:"subject"`
}

func (s *Server) decorateProjects(projects []store.Project) {
	for index := range projects {
		projects[index].Path = s.projectDir(projects[index])
		repo := gitrepo.Open(s.projectDir(projects[index]))
		if repo.IsRepo() {
			// The disk is the truth about what is checked out. Somebody can
			// switch branches in their own terminal, and the stored value is
			// only there so a branch can be looked up without opening every
			// repository on the machine.
			if live := repo.Branch(); live != "" && live != projects[index].Branch {
				projects[index].Branch = live
				_ = s.store.SetProjectBranch(projects[index].ID, live)
			}
		}
		if projects[index].Branch == "" {
			projects[index].Branch = "main"
		}
	}
}

func (s *Server) recentCommits(projects []store.Project, limit int) []recentCommit {
	if limit <= 0 {
		return []recentCommit{}
	}
	commits := []recentCommit{}
	for index, project := range projects {
		if index >= 12 {
			break
		}
		repo := gitrepo.Open(s.projectDir(project))
		if !repo.IsRepo() {
			continue
		}
		branch := repo.Branch()
		entries, err := repo.Log(3)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			commits = append(commits, recentCommit{ProjectID: project.ID,
				Project: project.Name, Branch: branch,
				Hash: entry.Hash, Short: entry.Short, Author: entry.Author,
				When: entry.When, Subject: entry.Subject})
		}
	}
	sort.Slice(commits, func(i, j int) bool { return commits[i].When > commits[j].When })
	if len(commits) > limit {
		commits = commits[:limit]
	}
	return commits
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
		Coordinator: []string{invite.Endpoint}, Policy: s.cfg.Worker,
		RayHead: response.Ray.Head, RayHeadNode: response.Ray.NodeID,
		RayHeadUpdated: response.Ray.Updated}
	s.cfg.Memberships = append(s.cfg.Memberships, membership)
	if s.cfg.ActiveNetwork == "" {
		s.cfg.SetActiveNetwork(membership.ID)
	}
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
		"nodes": response.Nodes, "active": s.cfg.ActiveNetwork})
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
	existing, err := s.store.Networks(s.cfg.AccountID())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	for _, network := range existing {
		if strings.EqualFold(network.Name, body.Name) {
			fail(w, http.StatusConflict, "You already have a network with that name.")
			return
		}
	}
	membership := config.MembershipConfig{
		ID: config.NewID(), Name: body.Name,
		Roles: []config.Role{config.RoleWorker}, AccountRole: store.NetworkOwner,
		ManagementKey: config.NewSecret(), Enabled: true, Policy: s.cfg.Worker,
	}
	var central *accountclient.Client
	var accountToken string
	if s.usesCentralAccounts() {
		accountToken, err = config.LoadAccountToken(s.layout)
		if err != nil || accountToken == "" {
			fail(w, http.StatusConflict, "Sign in again before creating a network.")
			return
		}
		central, err = accountclient.New(s.cfg.Account.Server)
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		if _, err = central.SyncNetwork(r.Context(), accountToken, accountserver.NetworkRegistration{
			ID: membership.ID, Name: membership.Name, ManagementKey: membership.ManagementKey,
			Role: store.NetworkOwner,
		}); err != nil {
			fail(w, http.StatusBadGateway, "The account server could not create the network: "+err.Error())
			return
		}
	}
	s.cfg.Memberships = append(s.cfg.Memberships, membership)
	if s.cfg.ActiveNetwork == "" {
		s.cfg.SetActiveNetwork(membership.ID)
	}
	if err := config.Save(s.layout, s.cfg); err != nil {
		s.cfg.Memberships = s.cfg.Memberships[:len(s.cfg.Memberships)-1]
		if central != nil {
			_ = central.DeleteNetwork(context.Background(), accountToken, membership.ID, membership.ManagementKey)
		}
		fail(w, 500, err.Error())
		return
	}
	if err := s.recordLocalMembership(membership, store.Network{
		ID: membership.ID, Name: membership.Name, OwnerAccountID: s.cfg.AccountID(),
	}); err != nil {
		s.cfg.Memberships = s.cfg.Memberships[:len(s.cfg.Memberships)-1]
		if s.cfg.ActiveNetwork == membership.ID {
			s.cfg.ActiveNetwork = ""
		}
		_ = config.Save(s.layout, s.cfg)
		_ = s.store.DeleteNetwork(membership.ID)
		if central != nil {
			_ = central.DeleteNetwork(context.Background(), accountToken, membership.ID, membership.ManagementKey)
		}
		fail(w, 500, err.Error())
		return
	}
	s.hub.Publish("networks.changed", membership)
	go s.checkInAccountServer(context.Background())
	writeJSON(w, 201, membership)
}

// deleteNetwork removes an owner-created network everywhere. The account
// server writes a tombstone first; local projects, history and files survive.
func (s *Server) deleteNetwork(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.membershipMu.Lock()
	defer s.membershipMu.Unlock()
	index := -1
	var membership config.MembershipConfig
	for i, item := range s.cfg.Memberships {
		if item.ID == id {
			index, membership = i, item
			break
		}
	}
	if index < 0 {
		fail(w, http.StatusNotFound, "This machine does not belong to that network.")
		return
	}
	if membership.AccountRole != store.NetworkOwner {
		fail(w, http.StatusForbidden, "Only the network owner can delete it.")
		return
	}
	if s.usesCentralAccounts() {
		token, err := config.LoadAccountToken(s.layout)
		if err != nil || token == "" {
			fail(w, http.StatusConflict, "Sign in again before deleting the network.")
			return
		}
		client, err := accountclient.New(s.cfg.Account.Server)
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		if err := client.DeleteNetwork(r.Context(), token, id, membership.ManagementKey); err != nil {
			fail(w, http.StatusBadGateway, "The account server could not delete the network: "+err.Error())
			return
		}
	}
	if err := s.store.DeleteNetwork(id); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.cfg.Memberships = append(s.cfg.Memberships[:index], s.cfg.Memberships[index+1:]...)
	if s.cfg.ActiveNetwork == id {
		s.cfg.ActiveNetwork = ""
	}
	if err := config.Save(s.layout, s.cfg); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.hub.Publish("networks.changed", map[string]string{"deleted": id})
	go s.reconcileRay(context.Background())
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "project_files_preserved": true})
}

// activateNetwork selects the network this machine works in.
//
// A machine runs one Ray process, and it belongs to one network at a time, so
// selecting another network necessarily takes this machine out of the old
// network's Ray cluster and puts it into the new one's. The reconciler already
// does exactly that; leaving it to the next tick meant up to fifteen seconds of
// a machine that had visibly switched while its compute had not, so the switch
// starts the move itself.
func (s *Server) activateNetwork(w http.ResponseWriter, r *http.Request) {
	// The reconciler reads and acts on ActiveNetwork while holding this same
	// lock. Serialising the assignment prevents a second request from rewriting
	// it underneath a Ray move that the first request just started.
	s.rayActionMu.Lock()
	defer s.rayActionMu.Unlock()
	id := r.PathValue("id")
	if _, err := s.executionNetwork(id); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	previous := s.cfg.ActiveNetwork
	if !s.cfg.SetActiveNetwork(id) {
		fail(w, 404, "This device does not belong to that network.")
		return
	}
	if err := config.Save(s.layout, s.cfg); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.hub.Publish("network.active", s.cfg.ActiveMembership())

	moved := previous != "" && previous != id
	if moved {
		// Detached from the request: the browser should not wait on a Ray
		// restart, and cancelling the request must not abandon it half done.
		go s.reconcileRay(context.Background())
	}
	writeJSON(w, 200, map[string]any{
		"membership": s.cfg.ActiveMembership(),
		"previous":   previous,
		"ray_moving": moved,
	})
}

func (s *Server) networkNodes(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !hasMembership(s.cfg, id) {
		fail(w, 404, "This device does not belong to that network.")
		return
	}
	nodes, err := s.liveNodes(id)
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

func (s *Server) updateNetworkMember(w http.ResponseWriter, r *http.Request) {
	s.changeNetworkMember(w, r, false)
}

func (s *Server) removeNetworkMember(w http.ResponseWriter, r *http.Request) {
	s.changeNetworkMember(w, r, true)
}

func (s *Server) changeNetworkMember(w http.ResponseWriter, r *http.Request, remove bool) {
	networkID, accountID := r.PathValue("id"), r.PathValue("account")
	actor, err := s.store.NetworkMember(networkID, s.cfg.AccountID())
	if err != nil || !actor.Permissions.ManageMembers {
		fail(w, http.StatusForbidden, "Your account cannot manage this network's members.")
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if !remove && (!store.ValidNetworkRole(body.Role) || body.Role == store.NetworkOwner) {
		fail(w, http.StatusBadRequest, "Choose admin, operator, member or viewer.")
		return
	}
	managementKey := ""
	for _, membership := range s.cfg.Memberships {
		if membership.ID == networkID {
			managementKey = membership.ManagementKey
			break
		}
	}
	if s.usesCentralAccounts() {
		token, tokenErr := config.LoadAccountToken(s.layout)
		client, clientErr := accountclient.New(s.cfg.Account.Server)
		if tokenErr != nil || clientErr != nil || token == "" {
			fail(w, http.StatusServiceUnavailable, "The account service is unavailable; member changes cannot be saved globally.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if remove {
			err = client.RemoveNetworkMember(ctx, token, networkID, managementKey, accountID)
		} else {
			err = client.SetNetworkMemberRole(ctx, token, networkID, managementKey, accountID, body.Role)
		}
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	if remove {
		err = s.store.RemoveNetworkMember(networkID, accountID)
	} else {
		err = s.store.AddNetworkMember(networkID, accountID, body.Role)
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.hub.Publish("networks.changed", map[string]any{"id": networkID})
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
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
