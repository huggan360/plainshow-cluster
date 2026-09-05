package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/config"
)

// swapSystemctl installs a fake systemctl for one test and restores the real
// one afterwards.
func swapSystemctl(t *testing.T, fake func(ctx context.Context, args ...string) ([]byte, error)) {
	t.Helper()
	original := systemctl
	systemctl = fake
	t.Cleanup(func() { systemctl = original })
}

func swapCgroup(t *testing.T, contents string) {
	t.Helper()
	original := cgroupFile
	path := filepath.Join(t.TempDir(), "cgroup")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cgroupFile = path
	t.Cleanup(func() { cgroupFile = original })
}

// TestDetectUnitReadsTheKernelsAnswer: the unit name is asked for rather than
// assumed, because the packaged name is not the only way this runs.
func TestDetectUnitReadsTheKernelsAnswer(t *testing.T) {
	swapCgroup(t, "0::/system.slice/plainshow-cluster.service\n")
	if got := detectUnit(); got != "plainshow-cluster.service" {
		t.Fatalf("detectUnit() = %q, want plainshow-cluster.service", got)
	}
}

// TestDetectUnitOnAHandStartedNode: no unit is a normal answer, not an error.
// A node run from a terminal has no boot setting to offer, and the interface
// has to be able to say that rather than showing a switch that does nothing.
func TestDetectUnitOnAHandStartedNode(t *testing.T) {
	swapCgroup(t, "0::/user.slice/user-1000.slice/session-3.scope\n")
	if got := detectUnit(); got != "" {
		t.Fatalf("detectUnit() = %q, want empty for a session scope", got)
	}
}

func TestServiceStateReadsEnabled(t *testing.T) {
	swapCgroup(t, "0::/system.slice/plainshow-cluster.service\n")
	swapSystemctl(t, func(ctx context.Context, args ...string) ([]byte, error) {
		return []byte("enabled\n"), nil
	})
	state := (&Server{}).serviceState(context.Background())
	if !state.Managed || !state.BootEnabled {
		t.Fatalf("state = %+v, want managed and enabled", state)
	}
}

// TestServiceStateDisabledIsNotAnError is the trap this test exists for:
// `systemctl is-enabled` reports "disabled" through a non-zero exit code, so
// reading only the error turns a perfectly healthy answer into a failure.
func TestServiceStateDisabledIsNotAnError(t *testing.T) {
	swapCgroup(t, "0::/system.slice/plainshow-cluster.service\n")
	swapSystemctl(t, func(ctx context.Context, args ...string) ([]byte, error) {
		return []byte("disabled\n"), os.ErrPermission // any non-nil exit
	})
	state := (&Server{}).serviceState(context.Background())
	if !state.Managed {
		t.Fatalf("state = %+v, want a managed service", state)
	}
	if state.BootEnabled {
		t.Errorf("a disabled unit read as enabled: %+v", state)
	}
}

func TestServiceStateWithoutSystemd(t *testing.T) {
	swapCgroup(t, "0::/system.slice/plainshow-cluster.service\n")
	swapSystemctl(t, func(ctx context.Context, args ...string) ([]byte, error) {
		return nil, errSystemdAbsent
	})
	state := (&Server{}).serviceState(context.Background())
	if state.Managed {
		t.Fatalf("state = %+v, want unmanaged when systemctl is missing", state)
	}
	if state.Detail == "" {
		t.Error("an unmanaged node was given no explanation to show")
	}
}

// TestServiceStateSaysWhatToDo: every unmanaged answer has to name why, or the
// interface shows a switch that silently refuses.
func TestServiceStateSaysWhatToDo(t *testing.T) {
	swapCgroup(t, "0::/user.slice/session-3.scope\n")
	state := (&Server{}).serviceState(context.Background())
	if state.Managed || state.Detail == "" {
		t.Fatalf("state = %+v, want unmanaged with a reason", state)
	}
}

// TestSwitchingNetworkMovesRay locks the answer to "what happens to Ray when I
// change network?": a machine runs one Ray process for one network, so the
// switch takes it out of the old cluster then and there. Leaving that to the
// next reconciler tick made a machine that had visibly switched keep serving
// the network it had just left.
func TestSwitchingNetworkMovesRay(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.cfg.Memberships = []config.MembershipConfig{
		{ID: "net-a", Name: "A", Enabled: true},
		{ID: "net-b", Name: "B", Enabled: true},
	}
	srv.cfg.ActiveNetwork = "net-a"

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/networks/{id}/active", srv.activateNetwork)

	switchTo := func(id string) map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/networks/"+id+"/active", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("switching to %s returned %d: %s", id, rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	if body := switchTo("net-b"); body["ray_moving"] != true {
		t.Errorf("changing network reported ray_moving=%v, want true", body["ray_moving"])
	}
	if srv.cfg.ActiveNetwork != "net-b" {
		t.Fatalf("active network is %q, want net-b", srv.cfg.ActiveNetwork)
	}
	// Re-selecting the network already in use moves nothing, and must not tell
	// somebody their cluster is being torn down when it is not.
	if body := switchTo("net-b"); body["ray_moving"] != false {
		t.Errorf("re-selecting the active network reported ray_moving=%v, want false",
			body["ray_moving"])
	}
}
