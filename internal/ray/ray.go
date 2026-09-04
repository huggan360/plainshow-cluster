// Package ray drives a Ray cluster.
//
// Ray distributes the work. Plainshow's part is to make `ray.init()` in
// somebody's ordinary Python find the other machines in their network, which
// means starting a head on one of them and attaching the rest over their
// tailnet addresses. Nothing here is a scheduler, a launcher, or a job model of
// our own — Ray already has all three and is better at them.
//
// Start and stop go through the `ray` command because that is its interface.
// Everything read back comes from the dashboard's HTTP API, which returns JSON
// rather than text meant for a person.
package ray

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ErrNotInstalled is returned when the ray command is absent.
var ErrNotInstalled = errors.New("ray is not installed on this machine")

// DefaultPort is where a head listens for workers, and DefaultDashboard is the
// port its HTTP API answers on. Both are Ray's defaults, kept so a cluster
// somebody started by hand behaves the same as one Plainshow started.
const (
	DefaultPort      = 6379
	DefaultDashboard = 8265
)

// runner executes the ray command. A variable so tests need no Ray installed.
var runner = func(ctx context.Context, args ...string) ([]byte, error) {
	path, err := exec.LookPath("ray")
	if err != nil {
		return nil, ErrNotInstalled
	}
	out, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = err.Error()
		}
		return out, fmt.Errorf("ray %s: %s", strings.Join(args, " "), message)
	}
	return out, nil
}

// fetch reads a dashboard endpoint. Also a variable, for the same reason.
var fetch = func(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("ray dashboard returned %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, 8<<20))
}

// Node is one machine in the Ray cluster.
type Node struct {
	Address string `json:"address"`
	Alive   bool   `json:"alive"`
	CPU     int    `json:"cpu"`
	GPU     int    `json:"gpu"`
}

// Job is one Ray job. The shape is Ray's, not ours.
type Job struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Entrypoint string `json:"entrypoint"`
	Message    string `json:"message,omitempty"`
	StartedAt  int64  `json:"started_at,omitempty"`
	EndedAt    int64  `json:"ended_at,omitempty"`
}

// Running reports whether a job is still going.
func (j Job) Running() bool {
	switch strings.ToUpper(j.Status) {
	case "PENDING", "RUNNING":
		return true
	}
	return false
}

// Status is what this machine can say about its Ray cluster.
type Status struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Head      bool   `json:"head"`
	Address   string `json:"address"`
	Dashboard string `json:"dashboard"`
	Nodes     []Node `json:"nodes"`
	TotalCPU  int    `json:"total_cpu"`
	TotalGPU  int    `json:"total_gpu"`
	Detail    string `json:"detail,omitempty"`
}

// Installed reports whether the ray command exists.
func Installed(ctx context.Context) bool {
	_, err := runner(ctx, "--version")
	return !errors.Is(err, ErrNotInstalled)
}

// Probe asks the dashboard what the cluster looks like.
//
// Not being installed, and being installed but not started, are ordinary
// states rather than errors: a network is useful before anybody runs anything.
func Probe(ctx context.Context, dashboard string) Status {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	status := Status{Dashboard: strings.TrimRight(dashboard, "/")}
	if !Installed(ctx) {
		status.Detail = "ray is not installed"
		return status
	}
	status.Installed = true
	if status.Dashboard == "" {
		status.Detail = "no Ray cluster is running for this network"
		return status
	}

	raw, err := fetch(ctx, status.Dashboard+"/nodes?view=summary")
	if err != nil {
		status.Detail = "ray is installed but its cluster is not reachable"
		return status
	}

	var payload struct {
		Data struct {
			Summary []struct {
				Raylet struct {
					State              string         `json:"state"`
					NodeManagerAddress string         `json:"nodeManagerAddress"`
					IsHeadNode         bool           `json:"isHeadNode"`
					Resources          map[string]any `json:"resourcesTotal"`
				} `json:"raylet"`
				IP string `json:"ip"`
			} `json:"summary"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		status.Detail = "could not read what the Ray dashboard reported"
		return status
	}

	for _, entry := range payload.Data.Summary {
		node := Node{
			Address: firstNonEmpty(entry.Raylet.NodeManagerAddress, entry.IP),
			Alive:   strings.EqualFold(entry.Raylet.State, "ALIVE"),
			CPU:     resourceCount(entry.Raylet.Resources, "CPU"),
			GPU:     resourceCount(entry.Raylet.Resources, "GPU"),
		}
		if entry.Raylet.IsHeadNode {
			status.Head = true
		}
		if node.Alive {
			status.TotalCPU += node.CPU
			status.TotalGPU += node.GPU
		}
		status.Nodes = append(status.Nodes, node)
	}
	status.Running = len(status.Nodes) > 0
	if !status.Running {
		status.Detail = "the Ray cluster reported no machines"
	}
	return status
}

// resourceCount reads an integer resource, which Ray reports as a float.
func resourceCount(resources map[string]any, name string) int {
	value, ok := resources[name]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case string:
		n, _ := strconv.Atoi(typed)
		return n
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// Jobs lists what Ray is running. These are Ray's jobs, reported as Ray sees
// them; Plainshow keeps no job table of its own for work Ray owns.
func Jobs(ctx context.Context, dashboard string) ([]Job, error) {
	if strings.TrimSpace(dashboard) == "" {
		return nil, errors.New("no Ray cluster is running for this network")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	raw, err := fetch(ctx, strings.TrimRight(dashboard, "/")+"/api/jobs/")
	if err != nil {
		return nil, err
	}
	var entries []struct {
		SubmissionID string `json:"submission_id"`
		JobID        string `json:"job_id"`
		Status       string `json:"status"`
		Entrypoint   string `json:"entrypoint"`
		Message      string `json:"message"`
		StartTime    int64  `json:"start_time"`
		EndTime      int64  `json:"end_time"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, errors.New("could not read the Ray job list")
	}

	jobs := make([]Job, 0, len(entries))
	for _, entry := range entries {
		jobs = append(jobs, Job{
			ID:         firstNonEmpty(entry.SubmissionID, entry.JobID),
			Status:     strings.ToUpper(entry.Status),
			Entrypoint: entry.Entrypoint,
			Message:    entry.Message,
			StartedAt:  entry.StartTime,
			EndedAt:    entry.EndTime,
		})
	}
	return jobs, nil
}

// StartHead brings up the head of a network's cluster on this machine.
//
// It binds to the machine's tailnet address so the other machines in the
// network can attach, and to nothing else.
func StartHead(ctx context.Context, address string, port, dashboardPort int) error {
	if strings.TrimSpace(address) == "" {
		return errors.New("this machine has no address to start a Ray head on")
	}
	if port <= 0 {
		port = DefaultPort
	}
	if dashboardPort <= 0 {
		dashboardPort = DefaultDashboard
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	_, err := runner(ctx, "start", "--head",
		"--node-ip-address="+address,
		"--port="+strconv.Itoa(port),
		"--dashboard-host="+address,
		"--dashboard-port="+strconv.Itoa(dashboardPort),
	)
	return err
}

// StartWorker attaches this machine to an existing head.
func StartWorker(ctx context.Context, address, head string) error {
	if strings.TrimSpace(head) == "" {
		return errors.New("no Ray head address to attach to")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	args := []string{"start", "--address=" + head}
	if strings.TrimSpace(address) != "" {
		args = append(args, "--node-ip-address="+address)
	}
	_, err := runner(ctx, args...)
	return err
}

// Stop leaves the cluster. Ray is left installed; only this machine's
// participation ends.
func Stop(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	_, err := runner(ctx, "stop")
	return err
}

// DashboardURL builds the address of a head's dashboard.
func DashboardURL(head string, port int) string {
	if strings.TrimSpace(head) == "" {
		return ""
	}
	if port <= 0 {
		port = DefaultDashboard
	}
	return "http://" + head + ":" + strconv.Itoa(port)
}
