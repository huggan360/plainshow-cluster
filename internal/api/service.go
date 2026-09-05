package api

// Background service control.
//
// Plainshow leaves three things running on a machine: this node, the Ray
// process it supervises, and the tailnet client. Anything the interface can
// start it must also be able to stop, from the same window, without asking
// someone to find the right systemctl incantation — otherwise "quit" means
// closing a window while the machine keeps working for other people.

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/tailnet"
)

// errSystemdAbsent separates "this machine has no init system we drive" from a
// systemctl that ran and refused.
var errSystemdAbsent = errors.New("systemctl is not available on this machine")

// systemctl runs one systemctl verb, returning its output even when it exits
// non-zero: is-enabled reports "disabled" through the exit code, so the output
// is the answer rather than an error.
var systemctl = func(ctx context.Context, args ...string) ([]byte, error) {
	path, err := exec.LookPath("systemctl")
	if err != nil {
		return nil, errSystemdAbsent
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
	return out, err
}

// cgroupFile is where the kernel names the unit this process belongs to.
var cgroupFile = "/proc/self/cgroup"

var unitPattern = regexp.MustCompile(`([A-Za-z0-9@:_.\-]+\.service)`)

// detectUnit asks the kernel which systemd unit owns this process rather than
// assuming a name. A node started from a terminal belongs to no unit, and that
// is a normal answer, not a failure.
func detectUnit() string {
	raw, err := os.ReadFile(cgroupFile)
	if err != nil {
		return ""
	}
	match := unitPattern.FindStringSubmatch(string(raw))
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

// serviceState is what the interface needs to describe, and offer to change,
// how this node runs in the background.
type serviceState struct {
	Managed     bool   `json:"managed"`
	Unit        string `json:"unit"`
	BootEnabled bool   `json:"boot_enabled"`
	CanChange   bool   `json:"can_change"`
	Detail      string `json:"detail"`
}

func (s *Server) serviceState(ctx context.Context) serviceState {
	state := serviceState{Unit: detectUnit()}
	if state.Unit == "" {
		state.Detail = "This node was started by hand, so there is no boot setting to " +
			"change. Installing the package puts it under the system service manager."
		return state
	}
	out, err := systemctl(ctx, "is-enabled", state.Unit)
	if errors.Is(err, errSystemdAbsent) {
		state.Detail = "systemctl is not available on this machine."
		return state
	}
	state.Managed = true
	verdict := strings.TrimSpace(string(out))
	if idx := strings.LastIndex(verdict, "\n"); idx >= 0 {
		verdict = strings.TrimSpace(verdict[idx+1:])
	}
	switch verdict {
	case "enabled", "enabled-runtime", "alias", "static", "indirect":
		state.BootEnabled = true
	case "":
		state.Detail = "Could not read whether this service starts at boot."
	}
	state.CanChange = os.Geteuid() == 0
	if !state.CanChange {
		state.Detail = "Changing this needs root. The packaged service runs as root; " +
			"a node started under your own account cannot rewrite system units."
	}
	return state
}

func (s *Server) getService(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.serviceState(r.Context()))
}

// putServiceBoot turns "start with the machine" on or off.
func (s *Server) putServiceBoot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	state := s.serviceState(r.Context())
	if !state.Managed {
		fail(w, http.StatusConflict, state.Detail)
		return
	}
	if !state.CanChange {
		fail(w, http.StatusForbidden, state.Detail)
		return
	}
	verb := "disable"
	if body.Enabled {
		verb = "enable"
	}
	if out, err := systemctl(r.Context(), verb, state.Unit); err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = err.Error()
		}
		fail(w, http.StatusBadGateway, message)
		return
	}
	writeJSON(w, http.StatusOK, s.serviceState(r.Context()))
}

// shutdownService is the off switch. It answers first and stops afterwards,
// because the reply cannot travel down a connection this handler is about to
// close.
func (s *Server) shutdownService(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tailnet bool `json:"tailnet"`
	}
	_ = decode(r, &body)

	state := s.serviceState(r.Context())
	writeJSON(w, http.StatusAccepted, map[string]any{
		"stopping": true, "unit": state.Unit, "managed": state.Managed,
		"tailnet": body.Tailnet,
	})
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	go s.stopEverything(state, body.Tailnet)
}

// stopEverything takes this machine out of the cluster in the order that leaves
// nothing waiting on something already gone: jobs, then Ray, then the tailnet,
// then the node itself.
func (s *Server) stopEverything(state serviceState, leaveTailnet bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	s.hub.Publish("service.stopping", map[string]any{"tailnet": leaveTailnet})
	s.sup.StopAll()

	s.rayActionMu.Lock()
	if err := stopLocalRay(ctx); err != nil {
		log.Printf("stop: ray did not stop cleanly: %v", err)
	}
	_ = s.clearLocalRayState()
	// A head that disappears without leaving a tombstone leaves every peer
	// retrying an address that will never answer again.
	if s.rayAnnouncement(s.cfg.ActiveNetwork).NodeID == s.cfg.Node.ID {
		_ = s.announceRayHead(s.cfg.ActiveNetwork, "")
	}
	s.rayActionMu.Unlock()

	if leaveTailnet {
		if err := tailnet.Down(ctx); err != nil {
			log.Printf("stop: tailnet did not disconnect: %v", err)
		}
	}

	// Prefer systemd's own stop: it records the service as stopped instead of
	// merely exited, so no restart policy brings it straight back. --no-block
	// keeps systemctl from waiting on the very process it is about to kill.
	if state.Managed && os.Geteuid() == 0 {
		if _, err := systemctl(ctx, "stop", "--no-block", state.Unit); err == nil {
			return
		}
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		log.Printf("stop: could not signal this process: %v", err)
	}
}
