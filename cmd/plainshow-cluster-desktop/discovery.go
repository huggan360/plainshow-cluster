package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/config"
)

func findNodeURL(args []string) (string, string) {
	return findNodeURLWithin(args, 8*time.Second)
}

// findNodeURLWithin waits only for runtime descriptors written by a node. It
// deliberately does not scan historical or fixed ports: each installation
// publishes its exact ephemeral address in <root>/run/node.json.
func findNodeURLWithin(args []string, within time.Duration) (string, string) {
	root := "/opt/plainshow-cluster"
	for index := 0; index < len(args); index++ {
		if args[index] == "--url" && index+1 < len(args) {
			return strings.TrimRight(args[index+1], "/"), ""
		}
		if strings.HasPrefix(args[index], "--url=") {
			return strings.TrimRight(strings.TrimPrefix(args[index], "--url="), "/"), ""
		}
		if args[index] == "--root" && index+1 < len(args) {
			root = args[index+1]
		}
		if strings.HasPrefix(args[index], "--root=") {
			root = strings.TrimPrefix(args[index], "--root=")
		}
	}
	if raw := strings.TrimSpace(os.Getenv("PSCLUSTER_URL")); raw != "" {
		return strings.TrimRight(raw, "/"), ""
	}

	roots := []string{root}
	if home, err := os.UserHomeDir(); err == nil {
		personal := filepath.Join(home, ".plainshow-cluster")
		if personal != root {
			roots = append(roots, personal)
		}
	}
	deadline := time.Now().Add(within)
	for {
		for _, candidate := range roots {
			layout, err := config.NewLayout(candidate)
			if err != nil {
				continue
			}
			runtime, err := config.LoadRuntime(layout)
			if err == nil && nodeIsPlainshow(runtime.URL) {
				return strings.TrimRight(runtime.URL, "/"), ""
			}
		}
		if within <= 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", "The Plainshow Cluster node service is not responding on this machine."
}

func nodeIsPlainshow(target string) bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	response, err := client.Get(strings.TrimRight(target, "/") + "/api/auth/status")
	if err != nil {
		return false
	}
	defer response.Body.Close()
	var status map[string]any
	err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&status)
	_, recognised := status["enabled"]
	return err == nil && response.StatusCode == http.StatusOK && recognised
}
