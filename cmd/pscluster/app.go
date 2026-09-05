package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/config"
)

// windowClass ties the window to plainshow-cluster.desktop, which is what
// makes the desktop show our icon and name for it.
const windowClass = "plainshow-cluster"

// appBrowsers open a URL in their own window with no tabs, address bar or
// bookmarks — which is what makes this a program rather than a browser tab.
var appBrowsers = []struct{ command, flag string }{
	{"chromium", "--app="},
	{"chromium-browser", "--app="},
	{"google-chrome", "--app="},
	{"google-chrome-stable", "--app="},
	{"brave", "--app="},
	{"brave-browser", "--app="},
	{"microsoft-edge", "--app="},
	{"vivaldi", "--app="},
}

// cmdApp opens Plainshow Cluster as a desktop program.
//
// Installed packages hand off to the native GTK/WebKit application. A source
// build without that optional binary still has a browser-app fallback.
func cmdApp(args []string) error {
	f := parseFlags(args, "browser")
	if !f.has("browser") {
		if launched, err := launchNativeDesktop(f); launched {
			return err
		}
	}
	l, _, err := openNode(f)
	if err != nil {
		return err
	}

	url := ""
	if runtime, runtimeErr := config.LoadRuntime(l); runtimeErr == nil {
		url = runtime.URL
	}

	if !nodeAnswering(url) {
		if err := startNodeInBackground(l); err != nil {
			return err
		}
		if discovered := waitForRuntimeNode(l, url, 30*time.Second); discovered == "" {
			return fmt.Errorf("the node did not start; try: pscluster serve --root %s", l.Root)
		} else {
			url = discovered
		}
	}

	if err := openAppWindow(url); err != nil {
		// A window is a convenience; the node is the product. Say where it is
		// rather than failing outright.
		fmt.Printf("\n  Plainshow Cluster is running at %s\n", url)
		fmt.Printf("  (could not open a window: %v)\n\n", err)
		return nil
	}
	fmt.Printf("\n  Plainshow Cluster  ·  %s\n\n", url)
	return nil
}

func waitForRuntimeNode(l config.Layout, fallback string, within time.Duration) string {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if runtime, err := config.LoadRuntime(l); err == nil && nodeAnswering(runtime.URL) {
			return runtime.URL
		}
		if fallback != "" && nodeAnswering(fallback) {
			return fallback
		}
		time.Sleep(250 * time.Millisecond)
	}
	return ""
}

// launchNativeDesktop hands off to the GTK/WebKit program when the installed
// package includes it. Keeping that program separate preserves the node's
// portable static binary and still makes `pscluster app` the one stable entry
// point. Source builds without desktop libraries retain the browser fallback.
func launchNativeDesktop(f flags) (bool, error) {
	candidates := []string{}
	desktopArgs := []string{}
	if explicit := os.Getenv("PSCLUSTER_DESKTOP"); explicit != "" {
		candidates = append(candidates, explicit)
	}
	if l, err := layoutFrom(f); err == nil {
		candidates = append(candidates, l.Root+"/bin/plainshow-cluster-desktop")
		// A desktop-menu launch runs as the signed-in user, while the packaged
		// node is a root-owned service in /opt. config.DefaultRoot consequently
		// points at the user's home here even though that is not the service the
		// window must open. Only forward a root the caller explicitly selected;
		// otherwise the desktop discovers the system node first and then any
		// personal node.
		if f.has("root") || os.Getenv(config.EnvRoot) != "" {
			desktopArgs = []string{"--root", l.Root}
		}
	}
	candidates = append(candidates,
		"/usr/lib/plainshow-cluster/plainshow-cluster-desktop",
		"/usr/local/lib/plainshow-cluster/plainshow-cluster-desktop")
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		cmd := exec.Command(candidate, desktopArgs...)
		cmd.Stdin, cmd.Stdout = os.Stdin, os.Stdout
		var stderr bytes.Buffer
		cmd.Stderr = ioMultiWriter(os.Stderr, &stderr)
		cmd.Env = desktopEnvironment(os.Environ())
		err = cmd.Run()
		// WebKitGTK can be rejected by Wayland on some NVIDIA/Arch setups.
		// Retry through XWayland automatically instead of crashing the app.
		if err != nil && os.Getenv("WAYLAND_DISPLAY") != "" &&
			os.Getenv("GDK_BACKEND") == "" &&
			(strings.Contains(stderr.String(), "Wayland display") ||
				strings.Contains(stderr.String(), "Protocol error")) {
			retry := exec.Command(candidate, desktopArgs...)
			retry.Stdin, retry.Stdout, retry.Stderr = os.Stdin, os.Stdout, os.Stderr
			retry.Env = append(desktopEnvironment(os.Environ()), "GDK_BACKEND=x11")
			return true, retry.Run()
		}
		return true, err
	}
	return false, nil
}

func desktopEnvironment(env []string) []string {
	for _, value := range env {
		if strings.HasPrefix(value, "WEBKIT_DISABLE_DMABUF_RENDERER=") {
			return env
		}
	}
	return append(env, "WEBKIT_DISABLE_DMABUF_RENDERER=1")
}

// Kept behind a tiny helper so app.go does not expose an io implementation
// detail throughout the launcher.
func ioMultiWriter(writers ...io.Writer) io.Writer { return io.MultiWriter(writers...) }

func nodeAnswering(url string) bool {
	if url == "" {
		return false
	}
	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Get(url + "/api/auth/status")
	if err != nil {
		return false
	}
	res.Body.Close()
	return true
}

// startNodeInBackground launches the daemon so opening the program is one step.
func startNodeInBackground(l config.Layout) error {
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(l.Root+"/logs/node.log",
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		logFile = nil
	}

	cmd := exec.Command(binary, "serve", "--root", l.Root)
	if logFile != nil {
		cmd.Stdout, cmd.Stderr = logFile, logFile
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start the node: %w", err)
	}
	// Released rather than waited on: the window outlives this command.
	go func() {
		_ = cmd.Wait()
		if logFile != nil {
			logFile.Close()
		}
	}()
	return nil
}

// openAppWindow opens a window with no browser furniture, falling back to the
// desktop's default handler when no such browser is installed.
func openAppWindow(url string) error {
	for _, browser := range appBrowsers {
		path, err := exec.LookPath(browser.command)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		// --class makes the window match the desktop entry, so the taskbar
		// shows the PlainShow mark rather than a generic browser icon and
		// groups it under the application rather than under Chromium.
		cmd := exec.CommandContext(ctx, path, browser.flag+url,
			"--class="+windowClass, "--name="+windowClass)
		if err := cmd.Start(); err != nil {
			cancel()
			continue
		}
		go func() { _ = cmd.Wait(); cancel() }()
		return nil
	}
	if path, err := exec.LookPath("xdg-open"); err == nil {
		return exec.Command(path, url).Start()
	}
	return errors.New("no browser was found to open a window with")
}
