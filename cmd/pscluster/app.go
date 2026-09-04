package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
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
// It is deliberately not an embedded browser engine. Bundling one would mean
// cgo and GTK development headers, which would cost the single static binary
// that can be cross-compiled for every machine in a cluster — a heavy price for
// a window. Instead it starts the node if it is not already running and opens
// a chromeless window pointing at it.
func cmdApp(args []string) error {
	f := parseFlags(args)
	l, cfg, err := openNode(f)
	if err != nil {
		return err
	}

	host := cfg.Network.Bind
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	port := cfg.Network.Port
	if port == 0 {
		port = config.DefaultPort
	}
	url := "http://" + net.JoinHostPort(host, strconv.Itoa(port))

	if !nodeAnswering(url) {
		if err := startNodeInBackground(l); err != nil {
			return err
		}
		if !waitForNode(url, 30*time.Second) {
			return fmt.Errorf("the node did not start; try: pscluster serve --root %s", l.Root)
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

func nodeAnswering(url string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Get(url + "/api/auth/status")
	if err != nil {
		return false
	}
	res.Body.Close()
	return true
}

func waitForNode(url string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if nodeAnswering(url) {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
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
