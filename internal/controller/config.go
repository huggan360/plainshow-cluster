// Package controller implements the optional always-reachable collaboration
// service. It is deliberately separate from a node: it has no worker policy,
// project tree, dataset cache or job supervisor.
package controller

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"gopkg.in/yaml.v3"
)

// EnvRoot names the controller install-root override.
const EnvRoot = "PSCLUSTER_CONTROLLER_ROOT"

// Config is the controller's complete durable configuration.
type Config struct {
	ID       string          `yaml:"id" json:"id"`
	Name     string          `yaml:"name" json:"name"`
	Listen   ListenConfig    `yaml:"listen" json:"listen"`
	Networks []NetworkConfig `yaml:"networks" json:"networks"`
}

// ListenConfig controls the HTTPS listener. Init resolves Port rather than
// leaving a machine-specific assumption compiled into the running service.
type ListenConfig struct {
	Bind      string `yaml:"bind" json:"bind"`
	Port      int    `yaml:"port" json:"port"`
	Advertise string `yaml:"advertise,omitempty" json:"advertise"`
}

// NetworkConfig identifies a network this controller serves. Enrollment fills
// these entries; an empty list is a valid unattached controller.
type NetworkConfig struct {
	ID   string `yaml:"id" json:"id"`
	Name string `yaml:"name" json:"name"`
}

// Defaults returns a host-neutral controller configuration.
func Defaults() *Config {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "plainshow-controller"
	}
	return &Config{Name: host, Listen: ListenConfig{Bind: "127.0.0.1"}, Networks: []NetworkConfig{}}
}

// DefaultRoot follows the same root-selection rule as a node while keeping the
// two programs' state distinct.
func DefaultRoot() string {
	if root := strings.TrimSpace(os.Getenv(EnvRoot)); root != "" {
		return root
	}
	if os.Geteuid() == 0 {
		return "/opt/plainshow-controller"
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "plainshow-controller")
	}
	return filepath.Join(home, ".plainshow-controller")
}

// ProbePort finds a free HTTPS port and immediately releases it. Serve still
// reports a useful error if another process wins the small race before bind.
func ProbePort(bind string) (int, error) {
	for port := config.DefaultPort + 2; port < config.DefaultPort+42; port++ {
		listener, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(port)))
		if err != nil {
			continue
		}
		_ = listener.Close()
		return port, nil
	}
	return 0, fmt.Errorf("no free port found between %d and %d on %s",
		config.DefaultPort+2, config.DefaultPort+41, bind)
}

// Load reads a controller configuration and fills safe omitted defaults.
func Load(layout config.Layout) (*Config, error) {
	raw, err := os.ReadFile(layout.ControllerConfigFile())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no controller at %s (run: pscluster-controller init)", layout.Root)
		}
		return nil, err
	}
	out := Defaults()
	if err := yaml.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", layout.ControllerConfigFile(), err)
	}
	if strings.TrimSpace(out.Name) == "" {
		out.Name = Defaults().Name
	}
	if strings.TrimSpace(out.Listen.Bind) == "" {
		out.Listen.Bind = "127.0.0.1"
	}
	if out.Networks == nil {
		out.Networks = []NetworkConfig{}
	}
	return out, nil
}

// Save atomically writes the configuration inside the controller root.
func Save(layout config.Layout, value *Config) error {
	raw, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	header := fmt.Sprintf(
		"# Plainshow Controller Server configuration\n"+
			"# Everything this controller stores lives under: %s\n\n", layout.Root)
	temporary := layout.ControllerConfigFile() + ".tmp"
	if err := os.WriteFile(temporary, append([]byte(header), raw...), 0o640); err != nil {
		return err
	}
	return os.Rename(temporary, layout.ControllerConfigFile())
}
