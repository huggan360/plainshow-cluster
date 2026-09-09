package ray

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

//go:embed diagnostic.py
var diagnosticCode string

// CheckResult proves a task executed on a particular worker, not only its head.
type CheckResult struct {
	Address   string             `json:"address"`
	Hostname  string             `json:"hostname"`
	OK        bool               `json:"ok"`
	Error     string             `json:"error,omitempty"`
	Resources map[string]float64 `json:"resources"`
	CPU       *SoftwareCheck     `json:"cpu,omitempty"`
	GPU       *SoftwareCheck     `json:"gpu,omitempty"`
	Software  []SoftwareCheck    `json:"software,omitempty"`
}

// Software checks are informational and never change worker reachability.
type SoftwareCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func jobRequest(ctx context.Context, dashboard, method, path string, body any, result any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(dashboard, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("Ray returned %s: %s", res.Status, strings.TrimSpace(string(data)))
	}
	if result != nil {
		return json.Unmarshal(data, result)
	}
	return nil
}

// Diagnose submits a bounded Ray job and stops it on timeout or cancellation.
func Diagnose(ctx context.Context, dashboard, id string) ([]CheckResult, error) {
	if dashboard == "" {
		return nil, errors.New("Start Ray from this network before testing")
	}
	quote := func(value string) string {
		return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
	}
	command := quote(managedPython()) + " -c " + quote(diagnosticCode)
	err := jobRequest(ctx, dashboard, "POST", "/api/jobs/", map[string]any{
		"entrypoint": command, "submission_id": id,
		"runtime_env": map[string]any{"env_vars": map[string]string{"PATH": managedPath()}},
	}, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = jobRequest(cleanup, dashboard, "POST", "/api/jobs/"+id+"/stop", nil, nil)
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("Ray test timed out or was cancelled: %w", ctx.Err())
		case <-ticker.C:
			var state struct {
				Status  string `json:"status"`
				Message string `json:"message"`
			}
			if err := jobRequest(ctx, dashboard, "GET", "/api/jobs/"+id, nil, &state); err != nil {
				return nil, err
			}
			if state.Status == "RUNNING" || state.Status == "PENDING" {
				continue
			}
			var logs struct {
				Logs string `json:"logs"`
			}
			if err := jobRequest(ctx, dashboard, "GET", "/api/jobs/"+id+"/logs", nil, &logs); err != nil {
				return nil, err
			}
			for _, line := range strings.Split(logs.Logs, "\n") {
				if strings.HasPrefix(line, "PLAINSHOW_TEST=") {
					var results []CheckResult
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "PLAINSHOW_TEST=")), &results); err != nil {
						return nil, err
					}
					return results, nil
				}
			}
			return nil, fmt.Errorf("Ray test %s: %s\n%s", strings.ToLower(state.Status), state.Message, logs.Logs)
		}
	}
}
