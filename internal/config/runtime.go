package config

import (
	"encoding/json"
	"errors"
	"os"
)

// Runtime describes the deliberately non-secret information a local desktop
// or CLI needs to reach the running node.
type Runtime struct {
	URL string `json:"url"`
	PID int    `json:"pid"`
}

// SaveRuntime atomically publishes the node's ephemeral loopback address.
func SaveRuntime(l Layout, runtime Runtime) error {
	raw, err := json.Marshal(runtime)
	if err != nil {
		return err
	}
	temporary := l.RuntimeFile() + ".tmp"
	if err := os.WriteFile(temporary, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(temporary, l.RuntimeFile()); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

// LoadRuntime returns the address published by a running node.
func LoadRuntime(l Layout) (Runtime, error) {
	var runtime Runtime
	raw, err := os.ReadFile(l.RuntimeFile())
	if err != nil {
		return runtime, err
	}
	if err := json.Unmarshal(raw, &runtime); err != nil {
		return Runtime{}, err
	}
	if runtime.URL == "" {
		return Runtime{}, errors.New("node runtime address is empty")
	}
	return runtime, nil
}
