// Package config owns the on-disk layout and settings of a Plainshow Cluster
// node.
//
// Everything a node keeps lives under a single root directory. Nothing is
// written anywhere else on the machine: no /etc, no /var, no scattered dot
// directories. The root is chosen at init time and can be moved by moving the
// directory. Two optional integration files (a systemd unit and a symlink onto
// PATH) are the only things that may ever sit outside it, and the node runs
// correctly without either.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// EnvRoot names the environment variable that overrides the install root.
const EnvRoot = "PSCLUSTER_ROOT"

// DefaultPort is where the web interface listens when nothing else is asked
// for. Init probes upward from here for a free port rather than assuming it.
const DefaultPort = 9999

// DefaultUpdateRepository is where a node looks for new releases until it is
// told otherwise. It is a default, not a constant of the system: point
// update.repository at a fork or a mirror and nothing else changes.
const DefaultUpdateRepository = "huggan360/plainshow-cluster"

// Role is a capability a machine carries in the cluster.
type Role string

const (
	// RoleController runs discovery and relay for the cluster. It holds no
	// project data and executes no jobs.
	RoleController Role = "controller"
	// RoleMaster holds the canonical copy of cluster state and projects, and
	// serves the web interface.
	RoleMaster Role = "master"
	// RoleWorker contributes CPU, GPU and disk to jobs.
	RoleWorker Role = "worker"
)

// AllRoles lists every valid role, in presentation order.
var AllRoles = []Role{RoleController, RoleMaster, RoleWorker}

// Valid reports whether r is a role this build understands.
func (r Role) Valid() bool {
	for _, k := range AllRoles {
		if k == r {
			return true
		}
	}
	return false
}

// Config is the full settings document for a node. Every field has a working
// default; an empty file is a valid configuration.
type Config struct {
	Node    NodeConfig    `yaml:"node" json:"node"`
	Cluster ClusterConfig `yaml:"cluster" json:"cluster"`
	Network NetworkConfig `yaml:"network" json:"network"`
	Worker  WorkerConfig  `yaml:"worker" json:"worker"`
	Update  UpdateConfig  `yaml:"update" json:"update"`
}

// UpdateConfig controls how this node keeps itself current.
//
// Nothing here names a particular repository at build time: the source is a
// setting like everything else, so a fork or a private mirror works without a
// code change.
type UpdateConfig struct {
	Enabled    bool   `yaml:"enabled" json:"enabled"`
	Repository string `yaml:"repository" json:"repository"`
	Channel    string `yaml:"channel" json:"channel"`
	// CheckEvery is a Go duration such as "6h". Zero disables the timer while
	// leaving manual checks available.
	CheckEvery string `yaml:"check_every" json:"check_every"`
	// Automatic applies a found update without being asked. Off by default:
	// replacing the binary under a running training job is the machine owner's
	// decision, not the cluster's.
	Automatic bool `yaml:"automatic" json:"automatic"`
}

// CheckInterval parses CheckEvery, falling back to six hours.
func (u UpdateConfig) CheckInterval() time.Duration {
	if u.CheckEvery == "" {
		return 6 * time.Hour
	}
	d, err := time.ParseDuration(u.CheckEvery)
	if err != nil || d < time.Minute {
		return 6 * time.Hour
	}
	return d
}

// NodeConfig identifies this machine within the cluster.
type NodeConfig struct {
	ID    string `yaml:"id" json:"id"`
	Name  string `yaml:"name" json:"name"`
	Roles []Role `yaml:"roles" json:"roles"`
}

// ClusterConfig identifies the cluster this node belongs to.
type ClusterConfig struct {
	ID   string `yaml:"id" json:"id"`
	Name string `yaml:"name" json:"name"`
}

// NetworkConfig controls where the node listens. Nothing here is compiled in:
// a zero Port means "probe for a free one starting at DefaultPort".
type NetworkConfig struct {
	Bind      string `yaml:"bind" json:"bind"`
	Port      int    `yaml:"port" json:"port"`
	Advertise string `yaml:"advertise" json:"advertise"`
}

// WorkerConfig is the machine owner's policy. It is authoritative and local:
// no remote party can widen it, and every job request is intersected with it
// before anything runs.
type WorkerConfig struct {
	Enabled       bool `yaml:"enabled" json:"enabled"`
	AllowJobs     bool `yaml:"allow_jobs" json:"allow_jobs"`
	AllowGPU      bool `yaml:"allow_gpu" json:"allow_gpu"`
	AllowTerminal bool `yaml:"allow_terminal" json:"allow_terminal"`
	MaxCPU        int  `yaml:"max_cpu" json:"max_cpu"`
	MaxRAMMB      int  `yaml:"max_ram_mb" json:"max_ram_mb"`
}

// HasRole reports whether the node carries role r.
func (c *Config) HasRole(r Role) bool {
	for _, k := range c.Node.Roles {
		if k == r {
			return true
		}
	}
	return false
}

// RoleNames renders the node's roles as plain strings.
func (c *Config) RoleNames() []string {
	out := make([]string, 0, len(c.Node.Roles))
	for _, r := range c.Node.Roles {
		out = append(out, string(r))
	}
	return out
}

// Defaults returns a configuration with every field populated for this host.
func Defaults() *Config {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "plainshow-node"
	}
	return &Config{
		Node: NodeConfig{
			ID:    NewID(),
			Name:  host,
			Roles: []Role{RoleMaster, RoleWorker},
		},
		Cluster: ClusterConfig{ID: NewID(), Name: "cluster"},
		Network: NetworkConfig{Bind: "127.0.0.1", Port: 0},
		Worker: WorkerConfig{
			Enabled:       true,
			AllowJobs:     true,
			AllowGPU:      true,
			AllowTerminal: false,
			MaxCPU:        0,
			MaxRAMMB:      0,
		},
		Update: UpdateConfig{
			Enabled:    true,
			Repository: DefaultUpdateRepository,
			Channel:    "stable",
			CheckEvery: "6h",
			Automatic:  false,
		},
	}
}

// NewID returns a short random identifier suitable for a node, cluster or row.
func NewID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(b)
}

// Layout resolves every path a node uses from a single root.
type Layout struct{ Root string }

// NewLayout builds a layout for root, resolved to an absolute path.
func NewLayout(root string) (Layout, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Layout{}, fmt.Errorf("resolve root: %w", err)
	}
	return Layout{Root: abs}, nil
}

func (l Layout) ConfigFile() string { return filepath.Join(l.Root, "config.yaml") }
func (l Layout) Database() string   { return filepath.Join(l.Root, "cluster.db") }
func (l Layout) Keys() string       { return filepath.Join(l.Root, "keys") }
func (l Layout) Projects() string   { return filepath.Join(l.Root, "projects") }
func (l Layout) Datasets() string   { return filepath.Join(l.Root, "datasets") }
func (l Layout) Artifacts() string  { return filepath.Join(l.Root, "artifacts") }
func (l Layout) Logs() string       { return filepath.Join(l.Root, "logs") }
func (l Layout) JobLogs() string    { return filepath.Join(l.Root, "logs", "jobs") }
func (l Layout) Run() string        { return filepath.Join(l.Root, "run") }
func (l Layout) Bin() string        { return filepath.Join(l.Root, "bin") }
func (l Layout) Binary() string     { return filepath.Join(l.Root, "bin", "pscluster") }

// GitHubToken is where this node keeps its GitHub credential. It is inside the
// install root like everything else, and readable only by the owner.
func (l Layout) GitHubToken() string { return filepath.Join(l.Root, "keys", "github.token") }
func (l Layout) PIDFile() string     { return filepath.Join(l.Root, "run", "pscluster.pid") }

// Dirs lists every directory the node expects to exist.
func (l Layout) Dirs() []string {
	return []string{
		l.Root, l.Keys(), l.Projects(), l.Datasets(),
		l.Artifacts(), l.Logs(), l.JobLogs(), l.Run(), l.Bin(),
	}
}

// EnsureDirs creates the layout, leaving anything already present alone.
func (l Layout) EnsureDirs() error {
	for _, d := range l.Dirs() {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	return nil
}

// Initialised reports whether root already holds a node.
func (l Layout) Initialised() bool {
	_, err := os.Stat(l.ConfigFile())
	return err == nil
}

// DefaultRoot picks the install root when the user names none. It honours
// PSCLUSTER_ROOT, then falls back to a system path for root and a path under
// the user's home otherwise, so an unprivileged install works without sudo.
func DefaultRoot() string {
	if v := strings.TrimSpace(os.Getenv(EnvRoot)); v != "" {
		return v
	}
	if os.Geteuid() == 0 {
		return "/opt/plainshow-cluster"
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "plainshow-cluster")
	}
	return filepath.Join(home, ".plainshow-cluster")
}

// Load reads the configuration at the layout's root, applying defaults for any
// field the file leaves unset.
func Load(l Layout) (*Config, error) {
	raw, err := os.ReadFile(l.ConfigFile())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no node at %s (run: pscluster init)", l.Root)
		}
		return nil, err
	}
	cfg := Defaults()
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", l.ConfigFile(), err)
	}
	cfg.applyFallbacks()
	return cfg, nil
}

// applyFallbacks fills in anything a hand-edited file left empty.
func (c *Config) applyFallbacks() {
	d := Defaults()
	if c.Node.ID == "" {
		c.Node.ID = d.Node.ID
	}
	if c.Node.Name == "" {
		c.Node.Name = d.Node.Name
	}
	if len(c.Node.Roles) == 0 {
		c.Node.Roles = d.Node.Roles
	}
	if c.Cluster.ID == "" {
		c.Cluster.ID = d.Cluster.ID
	}
	if c.Cluster.Name == "" {
		c.Cluster.Name = d.Cluster.Name
	}
	if c.Network.Bind == "" {
		c.Network.Bind = d.Network.Bind
	}
	if c.Update.Repository == "" {
		c.Update.Repository = d.Update.Repository
	}
	if c.Update.Channel == "" {
		c.Update.Channel = d.Update.Channel
	}
	if c.Update.CheckEvery == "" {
		c.Update.CheckEvery = d.Update.CheckEvery
	}
}

// Save writes the configuration atomically, so an interrupted write can never
// leave a node with a truncated config.
func Save(l Layout, c *Config) error {
	out, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	header := fmt.Sprintf(
		"# Plainshow Cluster node configuration\n"+
			"# Everything this node stores lives under: %s\n"+
			"# Edit and restart, or use: pscluster config set <key> <value>\n\n",
		l.Root)
	tmp := l.ConfigFile() + ".tmp"
	if err := os.WriteFile(tmp, append([]byte(header), out...), 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, l.ConfigFile())
}

// Platform describes the host in the form the UI shows.
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }
