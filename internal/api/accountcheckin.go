package api

import (
	"context"
	"encoding/base64"
	"errors"
	"log"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/accountclient"
	"github.com/huggan360/plainshow-cluster/internal/accountserver"
	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/store"
	"github.com/huggan360/plainshow-cluster/internal/sysinfo"
	"github.com/huggan360/plainshow-cluster/internal/tailnet"
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
	tailnetStatus := tailnet.Probe(ctx)
	checkIn := accountserver.NodeCheckIn{
		ID: s.cfg.Node.ID, Name: s.cfg.Node.Name, Version: version.Version,
		OS: info.OS, Arch: info.Arch, GPUCount: len(info.GPUs),
		ProjectCount: len(projects), RunningJobs: len(jobs), Networks: refs,
		ActiveNetwork: s.cfg.ActiveNetwork,
		Address:       advertisedEndpointFor(s.cfg, tailnetStatus),
		PublicKey:     base64.RawURLEncoding.EncodeToString(s.device.Public),
		Fingerprint:   s.fingerprint,
	}
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	instructions, err := client.CheckIn(requestCtx, token, checkIn)
	cancel()
	if err != nil {
		return
	}
	s.applyInstructions(ctx, instructions)
}

// applyInstructions carries out what the account service asked for on the last
// heartbeat.
//
// Nothing here is the account service reaching into the machine. Each of these
// is the machine reading a request and deciding to honour it, which is what
// keeps the local policy in Settings the last word about what happens here.
func (s *Server) applyInstructions(ctx context.Context, instructions accountserver.NodeInstructions) {
	if instructions.SignOut {
		s.signOutThisMachine()
		return
	}
	if instructions.DesiredNetwork == "" || instructions.DesiredNetwork == s.cfg.ActiveNetwork {
		return
	}
	// Adoption runs before this on every check-in, so a network somebody has
	// just moved this machine into is normally already present. If it is not,
	// the machine cannot participate and the request waits rather than failing.
	if !hasMembership(s.cfg, instructions.DesiredNetwork) {
		return
	}
	if !s.cfg.SetActiveNetwork(instructions.DesiredNetwork) {
		return
	}
	if err := config.Save(s.layout, s.cfg); err != nil {
		log.Printf("check-in: could not record the network move: %v", err)
		return
	}
	s.hub.Publish("network.active", s.cfg.ActiveMembership())
	// The machine runs one Ray process for one network, so moving it means
	// moving that too, now rather than on the reconciler's next tick.
	go s.reconcileRay(context.Background())
}

// signOutThisMachine drops the account credential and every browser session.
//
// This is the same thing the Sign out button does, arriving from another
// machine. It deliberately leaves networks, projects and settings alone: it
// signs somebody out of a computer, it does not wipe it.
func (s *Server) signOutThisMachine() {
	if err := config.ForgetAccountToken(s.layout); err != nil {
		log.Printf("sign-out: could not remove the account credential: %v", err)
	}
	if err := s.store.DeleteSessionsForAccount(s.cfg.Account.ID); err != nil {
		log.Printf("sign-out: could not clear browser sessions: %v", err)
	}
	s.hub.Publish("auth.signed_out", map[string]string{"reason": "requested from another device"})
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
	state, err := client.MyNetworkState(ctx, token)
	if err != nil {
		return 0, err
	}
	remote := state.Networks

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
	// Membership alone is not connectivity. Seed each local directory with the
	// private address and pinned identity of the other devices so the first mesh
	// exchange can happen; subsequent state travels directly peer-to-peer.
	for _, network := range remote {
		for _, device := range network.Devices {
			if device.ID == "" || device.ID == s.cfg.Node.ID || device.Address == "" || device.Fingerprint == "" {
				continue
			}
			_ = s.store.UpsertNetworkBootstrap(store.NetworkNode{
				NetworkID: network.ID, NodeID: device.ID, Name: device.Name,
				Roles: []string{"worker"}, OS: device.OS, Arch: device.Arch,
				Address: device.Address, PublicKey: device.PublicKey,
				Fingerprint: device.Fingerprint, LastSeen: device.LastSeen,
			})
		}
	}
	deleted := make(map[string]bool, len(state.DeletedNetworkIDs))
	for _, id := range state.DeletedNetworkIDs {
		deleted[id] = true
	}
	removed := 0
	kept := make([]config.MembershipConfig, 0, len(s.cfg.Memberships))
	for _, membership := range s.cfg.Memberships {
		if !deleted[membership.ID] {
			kept = append(kept, membership)
			continue
		}
		if err := s.store.DeleteNetwork(membership.ID); err != nil {
			kept = append(kept, membership)
			continue
		}
		removed++
	}
	s.cfg.Memberships = kept
	if deleted[s.cfg.ActiveNetwork] {
		s.cfg.ActiveNetwork = ""
	}
	if adopted == 0 && removed == 0 {
		return 0, nil
	}
	// Never choose a different run target implicitly after deletion/adoption.
	if err := config.Save(s.layout, s.cfg); err != nil {
		return adopted, err
	}
	s.hub.Publish("networks.changed", map[string]any{"adopted": adopted, "removed": removed})
	if removed > 0 {
		go s.reconcileRay(context.Background())
	}
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

// StartAccountWatch keeps a live connection to the account service so a
// sign-out, a device move or an invitation arrives now rather than on the next
// heartbeat.
//
// It is deliberately additive. Everything it triggers is something the
// heartbeat already does on its own schedule, so a machine where this never
// connects — no socket through a proxy, a captive network, an old account
// service — behaves exactly as it did before, only a minute slower.
func (s *Server) StartAccountWatch(ctx context.Context) {
	go func() {
		// Backoff exists so a service that is down, or one that refuses this
		// endpoint entirely, is not hammered once a second forever.
		delay := 2 * time.Second
		const maxDelay = 2 * time.Minute
		for {
			if ctx.Err() != nil {
				return
			}
			connected := s.watchAccountServer(ctx)
			if connected {
				delay = 2 * time.Second
			} else if delay < maxDelay {
				delay *= 2
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
	}()
}

// watchAccountServer holds one connection open, and reports whether it was ever
// established — which is what tells the caller to reset its backoff.
func (s *Server) watchAccountServer(ctx context.Context) bool {
	if !s.usesCentralAccounts() || s.cfg.Account.ID == "" {
		return false
	}
	token, err := config.LoadAccountToken(s.layout)
	if err != nil || token == "" {
		return false
	}
	client, err := accountclient.New(s.cfg.Account.Server)
	if err != nil {
		return false
	}
	connected := false
	watchErr := client.Watch(ctx, token, func(event accountclient.WatchEvent) {
		connected = true
		s.handleAccountEvent(ctx, event.Topic)
	})
	// A connection that carried nothing still counts as established: an idle
	// control channel is the normal case, and treating silence as failure would
	// back a healthy device off to two minutes.
	var closed *accountclient.WatchClosedError
	if watchErr == nil || errors.Is(watchErr, context.Canceled) || errors.As(watchErr, &closed) {
		connected = true
	}
	return connected
}

// handleAccountEvent turns a wake-up into the question it stands for.
//
// The event carries no data on purpose, so there is nothing here to trust: the
// device re-asks the account service over its own authenticated call and the
// answer goes through exactly the same code the heartbeat uses.
func (s *Server) handleAccountEvent(ctx context.Context, topic string) {
	switch topic {
	case accountserver.TopicDevices:
		s.checkInAccountServer(ctx)
		s.hub.Publish("devices.changed", map[string]string{"reason": "account"})
	case accountserver.TopicInvitations:
		s.hub.Publish("invitations.changed", map[string]string{"reason": "account"})
	case accountserver.TopicNetworks:
		s.checkInAccountServer(ctx)
		s.hub.Publish("networks.changed", map[string]string{"reason": "account"})
	}
}
