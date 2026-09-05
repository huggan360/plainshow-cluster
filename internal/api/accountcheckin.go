package api

import (
	"context"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/accountclient"
	"github.com/huggan360/plainshow-cluster/internal/accountserver"
	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/store"
	"github.com/huggan360/plainshow-cluster/internal/sysinfo"
	"github.com/huggan360/plainshow-cluster/internal/version"
)

// StartAccountCheckIn periodically reports aggregate health to the configured
// global admin service. Failure is deliberately non-fatal: account-server
// downtime must not interrupt jobs, git work, or peer communication.
func (s *Server) StartAccountCheckIn(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = time.Minute
	}
	go func() {
		s.checkInAccountServer(ctx)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.checkInAccountServer(ctx)
			}
		}
	}()
}

func (s *Server) checkInAccountServer(ctx context.Context) {
	if !s.usesCentralAccounts() || s.cfg.Account.ID == "" {
		return
	}
	token, err := config.LoadAccountToken(s.layout)
	if err != nil || token == "" {
		return
	}
	client, err := accountclient.New(s.cfg.Account.Server)
	if err != nil {
		return
	}
	_, _ = s.ensureManagedTailnet(ctx, client, token, false)
	// Pull before pushing. A machine somebody has just signed into has nothing
	// to report yet, and everything to learn.
	_, _ = s.AdoptAccountNetworks(ctx, client, token)
	projects, err := s.store.Projects()
	if err != nil {
		return
	}
	jobs, err := s.store.ActiveJobs()
	if err != nil {
		return
	}
	networks, err := s.store.Networks(s.cfg.Account.ID)
	if err != nil {
		return
	}
	refs := make([]accountserver.NetworkRef, 0, len(networks))
	roles := make(map[string]string, len(networks))
	for _, network := range networks {
		refs = append(refs, accountserver.NetworkRef{ID: network.ID, Name: network.Name})
		roles[network.ID] = network.Role
	}
	for _, membership := range s.cfg.Memberships {
		role := roles[membership.ID]
		if role == "" {
			role = "member"
		}
		access, syncErr := client.SyncNetwork(ctx, token, accountserver.NetworkRegistration{
			ID: membership.ID, Name: membership.Name,
			ManagementKey: membership.ManagementKey, Role: role,
		})
		if syncErr != nil {
			continue
		}
		s.syncEnterpriseMembers(membership.ID, access.Members)
		var selected *store.NetworkController
		if access.Controller.Address != "" {
			selected = &store.NetworkController{NetworkID: membership.ID, ID: access.Controller.ID,
				Name: access.Controller.Name, Address: access.Controller.Address,
				CollabToken: access.Controller.CollabToken, LastSeen: store.Now()}
		}
		_ = s.store.SetNetworkController(membership.ID, selected)
	}
	info := sysinfo.Probe(s.layout.Root)
	checkIn := accountserver.NodeCheckIn{
		ID: s.cfg.Node.ID, Name: s.cfg.Node.Name, Version: version.Version,
		OS: info.OS, Arch: info.Arch, GPUCount: len(info.GPUs),
		ProjectCount: len(projects), RunningJobs: len(jobs), Networks: refs,
	}
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_ = client.CheckIn(requestCtx, token, checkIn)
}

// AdoptAccountNetworks materialises every network this account belongs to but
// this device has never heard of, and returns how many were new.
//
// This is the half of the account relationship that was missing. Membership
// belongs to the account, not to whichever machine happened to create the
// network, so signing in on a new laptop has to bring the networks with it —
// otherwise the sign-in succeeds and the workspace is empty, which reads as a
// broken query rather than an absent feature.
//
// Adopting a network makes it visible and available; it does not select it and
// it does not widen what this machine will do. The device-wide policy in
// Settings still decides whether any network gets to run anything here.
func (s *Server) AdoptAccountNetworks(ctx context.Context, client *accountclient.Client, token string) (int, error) {
	remote, err := client.MyNetworks(ctx, token)
	if err != nil {
		return 0, err
	}

	s.membershipMu.Lock()
	defer s.membershipMu.Unlock()

	known := make(map[string]bool, len(s.cfg.Memberships))
	for _, membership := range s.cfg.Memberships {
		known[membership.ID] = true
	}

	adopted := 0
	for _, network := range remote {
		if network.ID == "" || network.Name == "" || known[network.ID] {
			continue
		}
		// Without the key this device cannot prove membership on its next
		// sync, so a network it could not participate in is not worth
		// pretending to have.
		if len(network.ManagementKey) < 32 {
			continue
		}
		role := network.Role
		if !store.ValidNetworkRole(role) {
			role = store.NetworkMember
		}
		membership := config.MembershipConfig{
			ID: network.ID, Name: network.Name,
			Roles: []config.Role{config.RoleWorker}, AccountRole: role,
			ManagementKey: network.ManagementKey, Enabled: true, Policy: s.cfg.Worker,
		}
		s.cfg.Memberships = append(s.cfg.Memberships, membership)
		if err := s.recordLocalMembership(membership, store.Network{
			ID: network.ID, Name: network.Name, OwnerAccountID: ownerOf(network, s.cfg.AccountID()),
		}); err != nil {
			// Leave the membership out of the config rather than saving one
			// the database does not back.
			s.cfg.Memberships = s.cfg.Memberships[:len(s.cfg.Memberships)-1]
			continue
		}
		known[network.ID] = true
		adopted++
	}
	if adopted == 0 {
		return 0, nil
	}
	// A machine with no network selected should land in one rather than in an
	// empty workspace it has to fix by hand.
	if s.cfg.ActiveNetwork == "" && len(s.cfg.Memberships) > 0 {
		s.cfg.SetActiveNetwork(s.cfg.Memberships[0].ID)
	}
	if err := config.Save(s.layout, s.cfg); err != nil {
		return adopted, err
	}
	s.hub.Publish("networks.changed", map[string]any{"adopted": adopted})
	return adopted, nil
}

// ownerOf keeps the local owner column meaningful without the account service
// having to disclose another account's id.
func ownerOf(network accountserver.AccountNetwork, self string) string {
	if network.Owner {
		return self
	}
	return ""
}

func (s *Server) syncEnterpriseMembers(networkID string, members []accountserver.EnterpriseMember) {
	seen := make(map[string]bool, len(members))
	configurationChanged := false
	for _, member := range members {
		if member.AccountID == "" || !store.ValidNetworkRole(member.Role) {
			continue
		}
		seen[member.AccountID] = true
		_ = s.store.UpsertAccount(store.Account{ID: member.AccountID, Username: member.Username,
			DisplayName: member.DisplayName})
		_ = s.store.AddNetworkMember(networkID, member.AccountID, member.Role)
		if member.AccountID == s.cfg.Account.ID {
			for index := range s.cfg.Memberships {
				membership := &s.cfg.Memberships[index]
				if membership.ID == networkID && membership.AccountRole != member.Role {
					membership.AccountRole = member.Role
					configurationChanged = true
				}
			}
		}
	}
	current, err := s.store.NetworkMembers(networkID)
	if err != nil {
		return
	}
	for _, member := range current {
		if !seen[member.Account.ID] {
			_ = s.store.RemoveNetworkMember(networkID, member.Account.ID)
		}
	}
	if configurationChanged {
		_ = config.Save(s.layout, s.cfg)
	}
}
