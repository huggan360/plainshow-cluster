package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/config"
)

// daemon talks to this machine's own running node.
//
// The command line drives the same HTTP API the browser does, rather than
// reaching into the database behind the daemon's back. One implementation of
// joining a network, creating a project or launching a job — not two that drift
// apart.
type daemon struct {
	base   string
	token  string
	client *http.Client
}

// connect prepares a client for the node at a layout, and fails with something
// actionable when the daemon is not running.
func connect(l config.Layout, cfg *config.Config) (*daemon, error) {
	token, err := config.EnsureCLIToken(l)
	if err != nil {
		return nil, fmt.Errorf("could not read this node's local token: %w", err)
	}
	host := cfg.Network.Bind
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	port := cfg.Network.Port
	if port == 0 {
		port = config.DefaultPort
	}
	return &daemon{
		base:   "http://" + net.JoinHostPort(host, strconv.Itoa(port)),
		token:  token,
		client: &http.Client{Timeout: 2 * time.Minute},
	}, nil
}

// call performs a request against the daemon and decodes the result.
func (d *daemon) call(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, d.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+d.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("this node is not running\n\n  start it:  pscluster serve")
	}
	defer res.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var detail struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(payload, &detail)
		if detail.Error != "" {
			return errors.New(detail.Error)
		}
		return fmt.Errorf("the node answered %d", res.StatusCode)
	}
	if out == nil || len(payload) == 0 {
		return nil
	}
	return json.Unmarshal(payload, out)
}

// openDaemon is the common opening move for every command that needs a running
// node: resolve the root, load its config, connect.
func openDaemon(f flags) (*daemon, config.Layout, *config.Config, error) {
	l, cfg, err := openNode(f)
	if err != nil {
		return nil, l, nil, err
	}
	d, err := connect(l, cfg)
	return d, l, cfg, err
}
