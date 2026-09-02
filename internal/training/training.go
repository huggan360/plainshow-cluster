// Package training plans all-or-nothing distributed launches. Framework data
// traffic stays between framework processes; Plainshow only prepares ranks,
// establishes a barrier, and supervises their lifecycles.
package training

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/huggan360/plainshow-cluster/internal/store"
)

type Reservations struct {
	mu    sync.Mutex
	nodes map[string]string
}

func NewReservations() *Reservations { return &Reservations{nodes: map[string]string{}} }
func (r *Reservations) Reserve(runID string, nodeIDs []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range nodeIDs {
		if owner := r.nodes[id]; owner != "" && owner != runID {
			return fmt.Errorf("machine %s is reserved by another training run", id)
		}
	}
	for _, id := range nodeIDs {
		r.nodes[id] = runID
	}
	return nil
}
func (r *Reservations) Release(runID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, owner := range r.nodes {
		if owner == runID {
			delete(r.nodes, id)
		}
	}
}

type Rank struct {
	Index       int               `json:"index"`
	Node        store.NetworkNode `json:"node"`
	Command     string            `json:"command"`
	Environment map[string]string `json:"environment"`
}
type Plan struct {
	RunID         string `json:"run_id"`
	Framework     string `json:"framework"`
	MasterAddress string `json:"master_address"`
	MasterPort    int    `json:"master_port"`
	Ranks         []Rank `json:"ranks"`
}

func Build(runID, framework, entry string, nproc int, nodes []store.NetworkNode, checkpointBase string) (Plan, error) {
	if len(nodes) == 0 {
		return Plan{}, errors.New("select at least one worker")
	}
	if nproc < 1 {
		nproc = 1
	}
	if strings.TrimSpace(entry) == "" {
		return Plan{}, errors.New("training entry point is required")
	}
	master, err := url.Parse(nodes[0].Address)
	if err != nil || master.Hostname() == "" {
		return Plan{}, errors.New("rank 0 has no reachable address")
	}
	plan := Plan{RunID: runID, Framework: framework, MasterAddress: master.Hostname(), MasterPort: 29500, Ranks: []Rank{}}
	for index, node := range nodes {
		env := map[string]string{"PLAINSHOW_TRAINING_RUN": runID, "PLAINSHOW_RANK": strconv.Itoa(index), "PLAINSHOW_WORLD_SIZE": strconv.Itoa(len(nodes)), "PLAINSHOW_CHECKPOINT_DIR": checkpointBase, "MASTER_ADDR": plan.MasterAddress, "MASTER_PORT": strconv.Itoa(plan.MasterPort)}
		var command string
		switch framework {
		case "pytorch":
			command = fmt.Sprintf("torchrun --nnodes=%d --nproc-per-node=%d --node-rank=%d --master-addr=%s --master-port=%d %s", len(nodes), nproc, index, shellQuote(plan.MasterAddress), plan.MasterPort, entry)
			env["NCCL_SOCKET_IFNAME"] = "^lo,docker0"
		case "shell":
			command = entry
		default:
			return Plan{}, errors.New("framework must be pytorch or shell")
		}
		plan.Ranks = append(plan.Ranks, Rank{Index: index, Node: node, Command: command, Environment: env})
	}
	return plan, nil
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

type Advice struct {
	GradientMB           float64 `json:"gradient_mb"`
	MinimumStepSeconds   float64 `json:"minimum_step_seconds"`
	CommunicationPercent float64 `json:"communication_percent"`
	Verdict              string  `json:"verdict"`
}

func Advise(parameters int64, bytesPerParameter, bandwidthMbps, observedStepSeconds float64, nodes int) Advice {
	if bytesPerParameter <= 0 {
		bytesPerParameter = 4
	}
	if nodes < 2 {
		nodes = 2
	}
	gradient := float64(parameters) * bytesPerParameter / (1024 * 1024)
	networkBytes := 2 * gradient * 1024 * 1024 * float64(nodes-1) / float64(nodes)
	minimum := networkBytes / (math.Max(bandwidthMbps, 0.001) * 1e6 / 8)
	percent := 100.0
	if observedStepSeconds > 0 {
		percent = math.Min(100, minimum/observedStepSeconds*100)
	}
	verdict := "distributed training should scale"
	if percent > 50 {
		verdict = "communication dominates; prefer one machine or independent jobs"
	} else if percent > 25 {
		verdict = "borderline; benchmark against one machine"
	}
	return Advice{gradient, minimum, percent, verdict}
}
