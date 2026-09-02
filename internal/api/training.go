package api

import (
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/jobs"
	"github.com/huggan360/plainshow-cluster/internal/mesh"
	"github.com/huggan360/plainshow-cluster/internal/store"
	"github.com/huggan360/plainshow-cluster/internal/training"
)

type trainingRequest struct {
	Project             string   `json:"project"`
	Framework           string   `json:"framework"`
	Entry               string   `json:"entry"`
	Machines            []string `json:"machines"`
	ProcessesPerNode    int      `json:"processes_per_node"`
	Dataset             string   `json:"dataset"`
	Parameters          int64    `json:"parameters"`
	BandwidthMbps       float64  `json:"bandwidth_mbps"`
	ObservedStepSeconds float64  `json:"observed_step_seconds"`
}

func (s *Server) listTrainingRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.store.TrainingRuns(s.cfg.ActiveNetwork)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, runs)
}

func (s *Server) trainingAdvice(w http.ResponseWriter, r *http.Request) {
	var body trainingRequest
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, training.Advise(body.Parameters, 4, body.BandwidthMbps, body.ObservedStepSeconds, len(body.Machines)))
}

func (s *Server) selectedTrainingNodes(ids []string) ([]store.NetworkNode, error) {
	if len(ids) == 0 {
		ids = []string{s.cfg.Node.ID}
	}
	nodes := make([]store.NetworkNode, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		node, err := s.store.NetworkNode(s.cfg.ActiveNetwork, id)
		if err != nil {
			return nil, errors.New("a selected machine is not in this network")
		}
		worker := false
		for _, role := range node.Roles {
			if role == string(config.RoleWorker) {
				worker = true
			}
		}
		if !worker {
			return nil, errors.New(node.Name + " is not configured as a worker")
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func (s *Server) trainingPreflight(w http.ResponseWriter, r *http.Request) {
	var body trainingRequest
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	nodes, err := s.selectedTrainingNodes(body.Machines)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	issues := []string{}
	if body.Framework == "pytorch" {
		for _, node := range nodes {
			if node.IsSelf {
				if _, err := exec.LookPath("torchrun"); err != nil {
					issues = append(issues, node.Name+": torchrun is not installed")
				}
			}
		}
	}
	plan, planErr := training.Build("preflight", body.Framework, body.Entry, body.ProcessesPerNode, nodes, filepath.Join(s.layout.Artifacts(), "preflight"))
	if planErr != nil {
		issues = append(issues, planErr.Error())
	}
	writeJSON(w, 200, map[string]any{"ready": len(issues) == 0, "issues": issues, "plan": plan, "advice": training.Advise(body.Parameters, 4, body.BandwidthMbps, body.ObservedStepSeconds, len(nodes))})
}

func (s *Server) startTraining(w http.ResponseWriter, r *http.Request) {
	var body trainingRequest
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	project, _, err := s.project(body.Project)
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	nodes, err := s.selectedTrainingNodes(body.Machines)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	runID := config.NewID()
	nodeIDs := make([]string, len(nodes))
	for i, node := range nodes {
		nodeIDs[i] = node.NodeID
	}
	if err := s.reservations.Reserve(runID, nodeIDs); err != nil {
		fail(w, 409, err.Error())
		return
	}
	plan, err := training.Build(runID, body.Framework, body.Entry, body.ProcessesPerNode, nodes, filepath.Join(s.layout.Artifacts(), s.cfg.ActiveNetwork, runID))
	if err != nil {
		s.reservations.Release(runID)
		fail(w, 400, err.Error())
		return
	}
	run := store.TrainingRun{ID: runID, NetworkID: s.cfg.ActiveNetwork, ProjectID: project.ID, Framework: body.Framework, State: "preparing", Ranks: []store.TrainingRank{}}
	if err := s.store.CreateTrainingRun(&run); err != nil {
		s.reservations.Release(runID)
		fail(w, 500, err.Error())
		return
	}
	archive, err := mesh.ArchiveDir(s.projectDir(project))
	if err != nil {
		s.reservations.Release(runID)
		fail(w, 500, err.Error())
		return
	}
	started := []store.Job{}
	for _, rank := range plan.Ranks {
		env := rank.Environment
		if body.Dataset != "" {
			dataset, err := s.store.Dataset(body.Dataset)
			if err != nil || dataset.NetworkID != s.cfg.ActiveNetwork {
				s.stopTrainingJobs(started)
				s.reservations.Release(runID)
				fail(w, 404, "No such dataset.")
				return
			}
			if rank.Node.IsSelf {
				target := filepath.Join(s.layout.Datasets(), "materialized", dataset.ID)
				if err := s.datasets.Materialize(dataset, target); err != nil {
					s.stopTrainingJobs(started)
					s.reservations.Release(runID)
					fail(w, 500, err.Error())
					return
				}
				env["PLAINSHOW_DATASET_DIR"] = target
			} else {
				client, err := s.clientForNode(s.cfg.ActiveNetwork, rank.Node.NodeID)
				if err != nil {
					s.stopTrainingJobs(started)
					s.reservations.Release(runID)
					fail(w, 409, err.Error())
					return
				}
				chunks, err := s.datasets.Export(dataset)
				if err != nil {
					s.stopTrainingJobs(started)
					s.reservations.Release(runID)
					fail(w, 500, err.Error())
					return
				}
				var ready struct {
					Path string `json:"path"`
				}
				if err := client.JSON("POST", "/mesh/v1/datasets/sync", datasetSyncRequest{Dataset: dataset, Manifest: dataset.Manifest, Chunks: chunks}, &ready, true); err != nil {
					s.stopTrainingJobs(started)
					s.reservations.Release(runID)
					fail(w, 502, err.Error())
					return
				}
				env["PLAINSHOW_DATASET_DIR"] = ready.Path
			}
		}
		var job store.Job
		if rank.Node.IsSelf {
			job, err = s.sup.Start(jobs.Request{ProjectID: project.ID, Project: project.Name, Kind: "training", Title: "rank " + rank.Node.Name, Command: rank.Command, Workdir: s.projectDir(project), Env: env})
		} else {
			client, clientErr := s.clientForNode(s.cfg.ActiveNetwork, rank.Node.NodeID)
			if clientErr != nil {
				err = clientErr
			} else {
				err = client.JSON("POST", "/mesh/v1/jobs", remoteJobRequest{ProjectID: project.ID, Project: project.Name, Description: project.Description, Kind: "training", Title: "rank " + rank.Node.Name, Command: rank.Command, Archive: archive, Environment: env}, &job, true)
				if err == nil {
					job.ProjectID = project.ID
					job.Project = project.Name
					job.MachineID = rank.Node.NodeID
					job.Machine = rank.Node.Name
					_ = s.store.UpsertMachine(store.Machine{ID: rank.Node.NodeID, Name: rank.Node.Name, Roles: rank.Node.Roles, OS: rank.Node.OS, Arch: rank.Node.Arch, Address: rank.Node.Address, LastSeen: store.Now()})
					err = s.store.CreateRemoteJob(job)
					if err == nil {
						s.remoteMu.Lock()
						s.remoteClients[job.ID] = client
						s.remoteLogs[job.ID] = []jobs.LogLine{}
						s.remoteMu.Unlock()
						go s.monitorRemoteJob(s.cfg.ActiveNetwork, rank.Node.NodeID, client, job)
					}
				}
			}
		}
		if err != nil {
			s.stopTrainingJobs(started)
			run.State = "failed"
			_ = s.store.UpdateTrainingRun(run)
			s.reservations.Release(runID)
			fail(w, 502, "Could not launch every rank: "+err.Error())
			return
		}
		started = append(started, job)
		run.Ranks = append(run.Ranks, store.TrainingRank{Rank: rank.Index, NodeID: rank.Node.NodeID, Node: rank.Node.Name, JobID: job.ID})
	}
	run.State = "running"
	_ = s.store.UpdateTrainingRun(run)
	go s.watchTraining(run, started)
	writeJSON(w, 201, run)
}

func (s *Server) stopTrainingJobs(list []store.Job) {
	for _, job := range list {
		if client, ok := s.remoteClient(job.ID); ok {
			_ = client.JSON("POST", "/mesh/v1/jobs/"+job.ID+"/stop", nil, nil, true)
		} else {
			_ = s.sup.Stop(job.ID)
		}
	}
}
func (s *Server) watchTraining(run store.TrainingRun, jobsList []store.Job) {
	defer s.reservations.Release(run.ID)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		finished := 0
		failed := false
		for _, item := range jobsList {
			job, err := s.store.Job(item.ID)
			if err != nil {
				continue
			}
			if terminalJobState(job.State) {
				finished++
				if job.State != store.JobSucceeded {
					failed = true
				}
			}
		}
		if finished == len(jobsList) {
			if failed {
				run.State = "failed"
			} else {
				run.State = "succeeded"
			}
			_ = s.store.UpdateTrainingRun(run)
			s.hub.Publish("training.state", run)
			return
		}
	}
}

var _ = strings.TrimSpace
