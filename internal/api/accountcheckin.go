package api

import (
	"context"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/accountclient"
	"github.com/huggan360/plainshow-cluster/internal/accountserver"
	"github.com/huggan360/plainshow-cluster/internal/config"
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
	for _, network := range networks {
		refs = append(refs, accountserver.NetworkRef{ID: network.ID, Name: network.Name})
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
