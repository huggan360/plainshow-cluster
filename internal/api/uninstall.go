package api

// Removing Plainshow Cluster from a machine.
//
// Anything that installs itself should be able to take itself off again, and
// say exactly what it will touch first. Two rules shape this:
//
// Everything Plainshow put here goes without asking — the root directory, the
// systemd unit, the PATH symlink, the desktop entry and its icons. That is our
// mess and we know where it is.
//
// Everything Plainshow merely *installed* is asked about one item at a time.
// Tailscale may be carrying somebody's other traffic; git and python are half
// the machine. Removing those by default would be a program deciding, on its
// way out, what else the computer no longer needs.

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// removable is a dependency the installer may have added, and how to take it
// off again on whichever package manager this machine uses.
type removable struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Detail  string `json:"detail"`
	Present bool   `json:"present"`
	// Warn is shown next to the checkbox when removing it reaches beyond
	// Plainshow.
	Warn string `json:"warn,omitempty"`
}

// packageManager is how this machine installs things, discovered rather than
// assumed: the same node binary runs on Arch, Debian, Fedora and openSUSE.
func packageManager() (name string, remove []string) {
	switch {
	case lookPath("pacman"):
		return "pacman", []string{"pacman", "-Rns", "--noconfirm"}
	case lookPath("apt-get"):
		return "apt", []string{"apt-get", "remove", "--purge", "-y"}
	case lookPath("dnf"):
		return "dnf", []string{"dnf", "remove", "-y"}
	case lookPath("zypper"):
		return "zypper", []string{"zypper", "--non-interactive", "remove"}
	}
	return "", nil
}

func lookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// getUninstall describes what removal would do, so the dialog states facts
// about this machine rather than a general story about installations.
func (s *Server) getUninstall(w http.ResponseWriter, r *http.Request) {
	manager, _ := packageManager()
	state := s.serviceState(r.Context())

	options := []removable{
		{
			ID: "ray", Name: "Ray",
			Detail:  "The runtime that runs your work. Installed inside this node's own directory.",
			Present: fileExists(filepath.Join(s.layout.Root, "runtime")),
		},
		{
			ID: "tailscale", Name: "Tailscale",
			Detail:  "The private network client.",
			Present: lookPath("tailscale"),
			Warn:    "System-wide. Other things on this machine may be using it.",
		},
		{
			ID: "package", Name: "The plainshow-cluster package",
			Detail:  "Removes it through " + orNone(manager) + ", the way it was installed.",
			Present: manager != "" && state.Managed,
		},
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"root":            s.layout.Root,
		"unit":            state.Unit,
		"managed":         state.Managed,
		"package_manager": manager,
		"can_remove":      os.Geteuid() == 0 || !state.Managed,
		"options":         options,
		"always": []string{
			s.layout.Root,
			"the systemd unit, if there is one",
			"/usr/local/bin/pscluster",
			"the desktop entry and its icons",
		},
	})
}

func orNone(name string) string {
	if name == "" {
		return "no package manager this node recognises"
	}
	return name
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// postUninstall takes this installation off the machine.
func (s *Server) postUninstall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		// Confirm must be the word "remove". A destructive endpoint reachable
		// by an empty POST is one stray request away from ruining somebody's
		// afternoon.
		Confirm string   `json:"confirm"`
		Also    []string `json:"also"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if !strings.EqualFold(strings.TrimSpace(body.Confirm), "remove") {
		fail(w, http.StatusBadRequest, `Type "remove" to confirm.`)
		return
	}
	state := s.serviceState(r.Context())
	if state.Managed && os.Geteuid() != 0 {
		fail(w, http.StatusForbidden,
			"Removing a packaged installation needs root. Run: sudo pscluster uninstall")
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"removing": true, "root": s.layout.Root, "also": body.Also,
	})
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	go s.removeInstallation(state, body.Also)
}

// removeInstallation stops everything, then deletes it.
//
// Order matters and is the reason this is not a shell one-liner: deleting the
// root out from under a running Ray leaves its processes holding a directory
// that no longer exists, and the node's own binary lives in the directory it is
// being asked to remove.
func (s *Server) removeInstallation(state serviceState, also []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	wanted := map[string]bool{}
	for _, item := range also {
		wanted[item] = true
	}

	s.hub.Publish("service.removing", map[string]any{"also": also})
	s.sup.StopAll()
	s.rayActionMu.Lock()
	_ = stopLocalRay(ctx)
	s.rayActionMu.Unlock()

	if wanted["tailscale"] {
		removePackage(ctx, "tailscale")
	}

	// The unit has to go before the directory it points into, or systemd is
	// left with a unit file that is a dangling symlink.
	if state.Managed && state.Unit != "" {
		_, _ = systemctl(ctx, "disable", "--now", state.Unit)
		_ = os.Remove(filepath.Join("/etc/systemd/system", state.Unit))
		_, _ = systemctl(ctx, "daemon-reload")
	}
	for _, path := range []string{
		"/usr/local/bin/pscluster",
		"/usr/local/bin/pscluster-admin",
		"/usr/share/applications/plainshow-cluster.desktop",
	} {
		_ = os.Remove(path)
	}
	for _, size := range []string{"16", "32", "48", "64", "128", "256", "512"} {
		_ = os.Remove(filepath.Join("/usr/share/icons/hicolor",
			size+"x"+size, "apps", "plainshow-cluster.png"))
	}

	// Ray lives inside the root, so removing the root removes it. Asking to
	// keep Ray means keeping that one directory back.
	root := s.layout.Root
	if wanted["ray"] || !fileExists(filepath.Join(root, "runtime")) {
		if err := os.RemoveAll(root); err != nil {
			log.Printf("uninstall: could not remove %s: %v", root, err)
		}
	} else {
		removeAllExcept(root, "runtime")
	}

	if wanted["package"] {
		removePackage(ctx, "plainshow-cluster")
	}

	// Last: this process lives in what has just been deleted.
	if state.Managed && os.Geteuid() == 0 {
		if _, err := systemctl(ctx, "stop", "--no-block", state.Unit); err == nil {
			return
		}
	}
	stopThisProcess()
}

// removeAllExcept deletes a directory's contents but keeps one entry, which is
// how "remove Plainshow but leave Ray" is honoured without a second install
// path for it.
func removeAllExcept(root, keep string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.Name() == keep {
			continue
		}
		_ = os.RemoveAll(filepath.Join(root, entry.Name()))
	}
}

func removePackage(ctx context.Context, name string) {
	manager, command := packageManager()
	if manager == "" || len(command) == 0 {
		return
	}
	arguments := append(append([]string{}, command[1:]...), name)
	if output, err := exec.CommandContext(ctx, command[0], arguments...).CombinedOutput(); err != nil {
		log.Printf("uninstall: %s %s: %v: %s", manager, name, err, strings.TrimSpace(string(output)))
	}
}
