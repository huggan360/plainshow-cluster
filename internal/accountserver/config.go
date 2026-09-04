// Package accountserver implements the one intentional central service in the
// Plainshow environment: global accounts, network-key recovery, private-network
// enrollment and aggregate cluster statistics. Collaboration, compute, project
// files, datasets and task traffic never pass through it.
package accountserver

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

// EnvRoot names the account-server install-root override.
const EnvRoot = "PSCLUSTER_ADMIN_ROOT"

// Config is the complete deploy-time configuration.
type Config struct {
	Listen           ListenConfig  `yaml:"listen" json:"listen"`
	PublicURL        string        `yaml:"public_url" json:"public_url"`
	RegistrationOpen bool          `yaml:"registration_open" json:"registration_open"`
	Tailnet          TailnetConfig `yaml:"tailnet" json:"tailnet"`
}

// ListenConfig is normally loopback because Apache terminates public TLS.
type ListenConfig struct {
	Bind string `yaml:"bind" json:"bind"`
	Port int    `yaml:"port" json:"port"`
}

// TailnetConfig connects PlainShow accounts to the self-hosted Headscale
// control plane. LoginServer being empty disables automatic enrollment, which
// keeps development and independently hosted account servers self-contained.
type TailnetConfig struct {
	LoginServer     string `yaml:"login_server" json:"login_server"`
	HeadscaleBin    string `yaml:"headscale_binary" json:"headscale_binary"`
	HeadscaleConfig string `yaml:"headscale_config" json:"headscale_config"`
	EnrollmentTTL   string `yaml:"enrollment_ttl" json:"enrollment_ttl"`
}

// Defaults keeps the service private until an explicit reverse proxy exposes
// it. The public hostname is always deployment config.
func Defaults() *Config {
	return &Config{Listen: ListenConfig{Bind: "127.0.0.1"}, RegistrationOpen: true,
		Tailnet: TailnetConfig{HeadscaleBin: "/usr/bin/headscale",
			HeadscaleConfig: "/etc/headscale/config.yaml", EnrollmentTTL: "10m"}}
}

// DefaultRoot resolves the service root without sharing state with a node or
// controller process.
func DefaultRoot() string {
	if root := strings.TrimSpace(os.Getenv(EnvRoot)); root != "" {
		return root
	}
	if os.Geteuid() == 0 {
		return "/opt/plainshow-cluster-admin"
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "plainshow-cluster-admin")
	}
	return filepath.Join(home, ".plainshow-cluster-admin")
}

// ProbePort chooses a loopback port during init and writes the result to disk.
func ProbePort(bind string) (int, error) {
	for port := config.DefaultPort + 3; port < config.DefaultPort+43; port++ {
		listener, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(port)))
		if err != nil {
			continue
		}
		_ = listener.Close()
		return port, nil
	}
	return 0, fmt.Errorf("no free port found between %d and %d on %s",
		config.DefaultPort+3, config.DefaultPort+42, bind)
}

// Load reads a config and applies only safe local defaults.
func Load(layout config.Layout) (*Config, error) {
	raw, err := os.ReadFile(layout.AdminConfigFile())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no account server at %s (run: pscluster-admin init)", layout.Root)
		}
		return nil, err
	}
	out := Defaults()
	if err := yaml.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", layout.AdminConfigFile(), err)
	}
	if strings.TrimSpace(out.Listen.Bind) == "" {
		out.Listen.Bind = "127.0.0.1"
	}
	out.PublicURL = strings.TrimRight(strings.TrimSpace(out.PublicURL), "/")
	out.Tailnet.LoginServer = strings.TrimRight(strings.TrimSpace(out.Tailnet.LoginServer), "/")
	if out.Tailnet.HeadscaleBin == "" {
		out.Tailnet.HeadscaleBin = Defaults().Tailnet.HeadscaleBin
	}
	if out.Tailnet.HeadscaleConfig == "" {
		out.Tailnet.HeadscaleConfig = Defaults().Tailnet.HeadscaleConfig
	}
	if out.Tailnet.EnrollmentTTL == "" {
		out.Tailnet.EnrollmentTTL = Defaults().Tailnet.EnrollmentTTL
	}
	return out, nil
}

// Save atomically writes the deployment config.
func Save(layout config.Layout, value *Config) error {
	raw, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	header := fmt.Sprintf(
		"# Plainshow enterprise account, network-key and private-network service\n"+
			"# Everything it stores lives under: %s\n\n", layout.Root)
	temporary := layout.AdminConfigFile() + ".tmp"
	if err := os.WriteFile(temporary, append([]byte(header), raw...), 0o640); err != nil {
		return err
	}
	return os.Rename(temporary, layout.AdminConfigFile())
}
