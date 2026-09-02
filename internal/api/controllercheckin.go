package api

import (
	"context"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/controller"
	"github.com/huggan360/plainshow-cluster/internal/mesh"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

// StartControllerCheckIn feeds the optional controller a read-only overview.
// Failure never affects compute or git: the controller adds live presence and
// overview, it is not a coordinator.
func (s *Server) StartControllerCheckIn(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 15 * time.Second
	}
	go func() {
		s.controllerCheckIn(ctx)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.controllerCheckIn(ctx)
			}
		}
	}()
}

func (s *Server) controllerCheckIn(ctx context.Context) {
	for _, membership := range s.cfg.Memberships {
		controllers, err := s.store.NetworkControllers(membership.ID)
		if err != nil || len(controllers) == 0 {
			continue
		}
		nodes, _ := s.store.NetworkNodes(membership.ID)
		projects, _ := s.store.ProjectsInNetwork(membership.ID)
		allJobs, _ := s.store.Jobs(100)
		projectIDs := map[string]bool{}
		for _, project := range projects {
			projectIDs[project.ID] = true
		}
		jobs := []store.Job{}
		for _, job := range allJobs {
			if projectIDs[job.ProjectID] {
				// The overview deliberately excludes commands and errors.
				job.Command, job.Error, job.Workdir = "", "", ""
				jobs = append(jobs, job)
			}
		}
		snapshot := controller.Snapshot{NetworkID: membership.ID,
			SourceID: s.cfg.Node.ID, Nodes: nodes, Projects: projects, Jobs: jobs}
		for _, item := range controllers {
			client := mesh.NewClient(item.Address, item.Fingerprint, membership.ID, s.device)
			_ = client.JSON("POST", "/mesh/v1/snapshot/"+membership.ID, snapshot, nil, true)
		}
	}
}
