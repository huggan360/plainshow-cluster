package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/accountclient"
	"github.com/huggan360/plainshow-cluster/internal/accountserver"
	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/ray"
)

// A daemon collects summaries even when its desktop window is closed. Peers
// can report the same Ray observation; the account store deduplicates it and
// wakes all member accounts only on a real change. No logs/files are uploaded.
// Keep unsuccessful reports eligible for retry, and never infer failure from
// an unreachable head. Ray remains the authority on execution.
func (s *Server) startJobHistorySync(ctx context.Context) {
	go func() {
		previous := map[string][32]byte{}
		for {
			if s.usesCentralAccounts() && s.cfg.Account.ID != "" {
				client, err := accountclient.New(s.cfg.Account.Server)
				token, tokenErr := config.LoadAccountToken(s.layout)
				if err == nil && tokenErr == nil && token != "" {
					scope := s.cfg.Account.Server + "/" + s.cfg.Account.ID
					for _, membership := range s.cfg.Memberships {
						head := s.rayHead(membership.ID)
						var jobs []ray.Job
						var readErr error
						if head != "" {
							jobs, readErr = ray.Jobs(ctx, ray.DashboardURL(hostOf(head), ray.DefaultDashboard))
						}
						// Bound summaries, not workload data. A long Ray exception
						// remains in its logs rather than growing the account DB.
						for i := range jobs {
							jobs[i].Message = trimJobText(jobs[i].Message)
							jobs[i].Entrypoint = trimJobText(jobs[i].Entrypoint)
						}
						sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
						raw, _ := json.Marshal(jobs)
						hash := sha256.Sum256(raw)
						key := scope + "/" + membership.ID
						if old, ok := previous[key]; readErr == nil && head != "" && (!ok || old != hash) {
							if err := s.store.QueueRayJobs(scope, membership.ID, jobs); err == nil {
								previous[key] = hash
								s.hub.Publish("jobs.changed", map[string]string{"network_id": membership.ID})
							}
						}
						// Flush even when the head is gone. Bound work per cycle.
						for batch := 0; batch < 10; batch++ {
							pending, err := s.store.PendingRayJobs(scope, membership.ID)
							if err != nil || len(pending) == 0 {
								break
							}
							requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
							err = client.ReportJobs(requestCtx, token, membership.ID, accountserver.JobReport{NodeID: s.cfg.Node.ID, Jobs: pending})
							cancel()
							if err != nil {
								break
							}
							if err := s.store.AckRayJobs(scope, membership.ID, pending); err != nil {
								break
							}
						}
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
}

func trimJobText(text string) string {
	if len(text) <= 4096 {
		return text
	}
	return strings.ToValidUTF8(text[:4093], "") + "…"
}

type listedRayJob struct {
	ray.Job
	NetworkID   string `json:"network_id"`
	NetworkName string `json:"network_name"`
	Archived    bool   `json:"archived"`
}

func mergeJobHistory(live []ray.Job, history []ray.Job, networkID, name string) []listedRayJob {
	items := make([]listedRayJob, 0, len(live)+len(history))
	indices := map[string]int{}
	for _, job := range live {
		indices[rayHistoryKey(job)] = len(items)
		items = append(items, listedRayJob{Job: job, NetworkID: networkID, NetworkName: name})
	}
	for _, job := range history {
		if index, ok := indices[rayHistoryKey(job)]; ok {
			// A delayed live read cannot make a completed job run again.
			if !job.Running() && items[index].Running() {
				items[index].Job = job
			}
			continue
		}
		items = append(items, listedRayJob{Job: job, NetworkID: networkID, NetworkName: name, Archived: true})
	}
	return items
}

func rayHistoryKey(job ray.Job) string { return job.ID + ":" + strconv.FormatInt(job.StartedAt, 10) }
