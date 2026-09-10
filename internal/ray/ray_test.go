package ray

import (
	"context"
	"strings"
	"testing"
	"time"
)

func stubRunner(t *testing.T, out string, err error) *[]string {
	t.Helper()
	original := runner
	captured := &[]string{}
	runner = func(_ context.Context, args ...string) ([]byte, error) {
		*captured = args
		return []byte(out), err
	}
	t.Cleanup(func() { runner = original })
	return captured
}

func stubFetch(t *testing.T, body string, err error) {
	t.Helper()
	original := fetch
	fetch = func(_ context.Context, _ string) ([]byte, error) { return []byte(body), err }
	t.Cleanup(func() { fetch = original })
}

// A trimmed but real-shaped /nodes?view=summary: a head with a GPU and one
// worker with two.
const nodesSample = `{"data":{"summary":[
 {"ip":"100.64.0.1","raylet":{"state":"ALIVE","nodeManagerAddress":"100.64.0.1",
  "isHeadNode":true,"resourcesTotal":{"CPU":8.0,"GPU":1.0}}},
 {"ip":"100.64.0.2","raylet":{"state":"ALIVE","nodeManagerAddress":"100.64.0.2",
  "isHeadNode":false,"resourcesTotal":{"CPU":12.0,"GPU":2.0}}},
 {"ip":"100.64.0.3","raylet":{"state":"DEAD","nodeManagerAddress":"100.64.0.3",
  "isHeadNode":false,"resourcesTotal":{"CPU":4.0,"GPU":1.0}}}]}}`

func TestProbeReadsClusterShape(t *testing.T) {
	stubRunner(t, "ray, version 2.9.0", nil)
	stubFetch(t, nodesSample, nil)

	status := Probe(context.Background(), "http://100.64.0.1:8265")
	if !status.Installed || !status.Running || !status.Head {
		t.Fatalf("status = %+v", status)
	}
	if len(status.Nodes) != 3 {
		t.Fatalf("found %d nodes, want 3", len(status.Nodes))
	}
	// A dead machine's GPUs are not capacity anybody can use.
	if status.TotalGPU != 3 || status.TotalCPU != 20 {
		t.Errorf("totals = %d CPU / %d GPU, want 20 / 3 (the dead node excluded)",
			status.TotalCPU, status.TotalGPU)
	}
}

// Not installed and not started are ordinary states: a network is useful
// before anybody runs anything, so neither may read as broken.
func TestNotInstalledAndNotRunningAreStates(t *testing.T) {
	stubRunner(t, "", ErrNotInstalled)
	status := Probe(context.Background(), "http://100.64.0.1:8265")
	if status.Installed || status.Running || status.Detail == "" {
		t.Errorf("missing ray = %+v", status)
	}

	stubRunner(t, "ray, version 2.9.0", nil)
	status = Probe(context.Background(), "")
	if !status.Installed || status.Running || status.Detail == "" {
		t.Errorf("no cluster = %+v", status)
	}
}

func TestNodeAliveRequiresThisMachinesRaylet(t *testing.T) {
	stubFetch(t, nodesSample, nil)
	if !NodeAlive(context.Background(), "http://100.64.0.1:8265", "100.64.0.2") {
		t.Fatal("an alive local raylet was reported missing")
	}
	if NodeAlive(context.Background(), "http://100.64.0.1:8265", "100.64.0.3") {
		t.Fatal("a dead local raylet was hidden by the reachable head")
	}
	if NodeAlive(context.Background(), "http://100.64.0.1:8265", "100.64.0.99") {
		t.Fatal("a machine absent from Ray was reported attached")
	}
}

func TestWaitForNodeRequiresStableDashboardSightings(t *testing.T) {
	calls := 0
	original := fetch
	fetch = func(_ context.Context, _ string) ([]byte, error) {
		calls++
		return []byte(nodesSample), nil
	}
	t.Cleanup(func() { fetch = original })
	if err := WaitForNode(context.Background(), "http://100.64.0.1:8265",
		"100.64.0.2", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if calls < 3 {
		t.Fatalf("join was accepted after %d dashboard sighting(s), want at least 3", calls)
	}
}

func TestUnreachableDashboardExplainsItself(t *testing.T) {
	stubRunner(t, "ray, version 2.9.0", nil)
	stubFetch(t, "", context.DeadlineExceeded)

	status := Probe(context.Background(), "http://100.64.0.1:8265")
	if status.Running {
		t.Error("an unreachable dashboard reported a running cluster")
	}
	if !strings.Contains(status.Detail, "not reachable") {
		t.Errorf("detail = %q", status.Detail)
	}
}

func TestJobsReadRayShape(t *testing.T) {
	stubFetch(t, `[
	 {"submission_id":"raysubmit_a","status":"RUNNING","entrypoint":"python train.py","start_time":1700000000000},
	 {"submission_id":"raysubmit_b","status":"SUCCEEDED","entrypoint":"python eval.py","start_time":1,"end_time":2}
	]`, nil)

	jobs, err := Jobs(context.Background(), "http://100.64.0.1:8265")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs, want 2", len(jobs))
	}
	if jobs[0].StartedAt != 1700000000000 || jobs[1].EndedAt != 2 {
		t.Fatal("Ray millisecond timestamps were converted or lost")
	}
	if !jobs[0].Running() || jobs[1].Running() {
		t.Errorf("running states wrong: %+v", jobs)
	}
	if jobs[0].Entrypoint != "python train.py" {
		t.Errorf("entrypoint = %q", jobs[0].Entrypoint)
	}
}

func TestJobsWithoutAClusterSaysSo(t *testing.T) {
	if _, err := Jobs(context.Background(), "  "); err == nil {
		t.Error("listing jobs with no cluster reported success")
	}
}

func TestSubmitUsesManagedRayJobProtocol(t *testing.T) {
	args := stubRunner(t, "Job submission server address: ok", nil)
	previousManaged := managed
	UseManaged("/opt/plainshow-cluster/runtime/bin/ray")
	t.Cleanup(func() { UseManaged(previousManaged) })
	if _, err := Submit(context.Background(), "http://100.64.0.1:8265/",
		"/work/my project", "python main.py", "plainshow_1"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*args, "|")
	for _, want := range []string{"job|submit", "--address=http://100.64.0.1:8265",
		"--submission-id=plainshow_1", "--working-dir=/work/my project", "--no-wait",
		`--runtime-env-json={"env_vars":{"PATH":"/opt/plainshow-cluster/runtime/bin:`,
		`"RAY_DEFAULT_PYTHON_VERSION_MATCH_LEVEL":"minor"`,
		"/bin/sh|-lc|python main.py"} {
		if !strings.Contains(joined, want) {
			t.Errorf("ran %q, missing %q", joined, want)
		}
	}
}

func TestManagedEnvironmentAllowsPythonPatchUpdates(t *testing.T) {
	t.Setenv("RAY_DEFAULT_PYTHON_VERSION_MATCH_LEVEL", "patch")
	found := false
	for _, entry := range managedEnvironment() {
		if entry == "RAY_DEFAULT_PYTHON_VERSION_MATCH_LEVEL=minor" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("managed Ray environment did not allow Python patch-version differences")
	}
}

func TestStopJobNamesTheSubmission(t *testing.T) {
	args := stubRunner(t, "", nil)
	if err := StopJob(context.Background(), "http://100.64.0.1:8265", "raysubmit_1"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*args, " "); !strings.Contains(got, "job stop") ||
		!strings.HasSuffix(got, "raysubmit_1") {
		t.Errorf("ran %q", got)
	}
}

// The head must bind to the address other machines can reach it on, not to
// everything and not to loopback.
func TestStartHeadBindsToTheGivenAddress(t *testing.T) {
	args := stubRunner(t, "", nil)
	if err := StartHead(context.Background(), "100.64.0.1", 0, 0,
		ResourcePolicy{MaxCPU: 6, MaxRAMMB: 2048, AllowGPU: false}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*args, " ")
	for _, want := range []string{"start", "--head", "--node-ip-address=100.64.0.1",
		"--port=6379", "--dashboard-host=100.64.0.1", "--dashboard-port=8265",
		"--num-cpus=6", "--memory=2147483648", "--num-gpus=0"} {
		if !strings.Contains(joined, want) {
			t.Errorf("ran %q, missing %q", joined, want)
		}
	}
}

func TestStartHeadRefusesWithoutAnAddress(t *testing.T) {
	stubRunner(t, "", nil)
	if err := StartHead(context.Background(), "  ", 0, 0, ResourcePolicy{}); err == nil {
		t.Error("a head was started with no address to bind to")
	}
}

func TestStartWorkerAttachesToTheHead(t *testing.T) {
	args := stubRunner(t, "", nil)
	if err := StartWorker(context.Background(), "100.64.0.2", "100.64.0.1:6379",
		ResourcePolicy{AllowGPU: true, GPUsKnown: true, GPUCount: 2,
			AMDGPUCount: 1, IntelGPUCount: 1}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*args, " ")
	if !strings.Contains(joined, "--address=100.64.0.1:6379") ||
		!strings.Contains(joined, "--node-ip-address=100.64.0.2") ||
		!strings.Contains(joined, "--num-gpus=2") ||
		!strings.Contains(joined, `"plainshow_gpu_amd":1`) ||
		!strings.Contains(joined, `"plainshow_gpu_intel":1`) {
		t.Errorf("ran %q", joined)
	}
}

func TestStartWorkerRefusesWithoutAHead(t *testing.T) {
	stubRunner(t, "", nil)
	if err := StartWorker(context.Background(), "100.64.0.2", "", ResourcePolicy{}); err == nil {
		t.Error("a worker attached to nothing")
	}
}

func TestDashboardURL(t *testing.T) {
	if got := DashboardURL("100.64.0.1", 0); got != "http://100.64.0.1:8265" {
		t.Errorf("DashboardURL = %q", got)
	}
	if got := DashboardURL("", 8265); got != "" {
		t.Errorf("DashboardURL with no head = %q, want empty", got)
	}
}
