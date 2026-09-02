// Command pscluster is the Plainshow Cluster node: daemon and CLI in one
// binary.
//
// Everything a node stores lives under a single root directory chosen at init
// time. Nothing is written elsewhere on the machine.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/accountclient"
	"github.com/huggan360/plainshow-cluster/internal/api"
	"github.com/huggan360/plainshow-cluster/internal/collab"
	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/dataset"
	"github.com/huggan360/plainshow-cluster/internal/events"
	"github.com/huggan360/plainshow-cluster/internal/gitrepo"
	"github.com/huggan360/plainshow-cluster/internal/identity"
	"github.com/huggan360/plainshow-cluster/internal/jobs"
	"github.com/huggan360/plainshow-cluster/internal/notebook"
	"github.com/huggan360/plainshow-cluster/internal/store"
	"github.com/huggan360/plainshow-cluster/internal/sysinfo"
	"github.com/huggan360/plainshow-cluster/internal/updater"
	"github.com/huggan360/plainshow-cluster/internal/version"
	"github.com/huggan360/plainshow-cluster/web"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("")

	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "init":
		err = cmdInit(rest)
	case "serve", "daemon":
		err = cmdServe(rest)
	case "status":
		err = cmdStatus(rest)
	case "run":
		err = cmdRun(rest)
	case "config":
		err = cmdConfig(rest)
	case "network", "networks":
		err = cmdNetwork(rest)
	case "invite":
		err = cmdInvite(rest)
	case "join":
		err = cmdJoin(rest)
	case "controller":
		err = cmdController(rest)
	case "github":
		err = cmdGitHub(rest)
	case "update":
		err = cmdUpdate(rest)
	case "version", "--version", "-v":
		fmt.Printf("%s %s (%s, %s)\n", version.Product, version.Version, version.Commit, config.Platform())
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "pscluster: unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "pscluster: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Printf(`%s %s

  pscluster init [--root DIR] [--name NAME] [--cluster NAME]
                 [--port N] [--peer-port N]
	         [--bind ADDR] [--advertise HTTPS_URL]
	         [--account-server HTTPS_URL]
      Create a node. Everything it stores lives under one directory.

  pscluster serve [--root DIR]
      Run the node and serve the web interface.

  pscluster status [--root DIR]
      Show what this node is and what it holds.

  pscluster run [--root DIR] <command...>
      Run a command as a job and stream its output.

  pscluster config [--root DIR] [get KEY | set KEY VALUE | path]
      Read or change settings.

  pscluster network [list | use ID]
      Show the networks this machine belongs to, or switch the active one.

  pscluster invite [--role member] [--network ID]
                   [--tailnet-auth-key KEY] [--tailnet-login-server URL]
      Create a single-use join code for another machine.

  pscluster join CODE [--endpoint URL]
      Join a network with a code from another machine.

  pscluster controller invite [--network ID]
      Mint a single-use enrollment code for a controller server.

  pscluster github [status | connect | disconnect]
      Connect a GitHub account. connect reads the token from the terminal,
      or from standard input when piped.

  pscluster update [check | status | apply]
      See whether a newer build is published, and install it.

  pscluster version

The install root is taken from --root, then %s, then a default
(/opt/plainshow-cluster as root, ~/.plainshow-cluster otherwise).
`, version.Product, version.Version, config.EnvRoot)
}

// flags is a tiny argument parser: enough for --key value and --flag, without
// pulling in a dependency for six commands.
type flags struct {
	vals map[string]string
	rest []string
}

func parseFlags(args []string, boolFlags ...string) flags {
	isBool := map[string]bool{}
	for _, b := range boolFlags {
		isBool[b] = true
	}
	f := flags{vals: map[string]string{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			f.rest = append(f.rest, a)
			continue
		}
		key := strings.TrimPrefix(a, "--")
		if k, v, ok := strings.Cut(key, "="); ok {
			f.vals[k] = v
			continue
		}
		if isBool[key] {
			f.vals[key] = "true"
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			f.vals[key] = args[i+1]
			i++
			continue
		}
		f.vals[key] = "true"
	}
	return f
}

func (f flags) get(key, def string) string {
	if v, ok := f.vals[key]; ok && v != "" {
		return v
	}
	return def
}

func (f flags) has(key string) bool { _, ok := f.vals[key]; return ok }

// layoutFrom resolves the install root for a command.
func layoutFrom(f flags) (config.Layout, error) {
	return config.NewLayout(f.get("root", config.DefaultRoot()))
}

// openNode loads an initialised node's layout and configuration.
func openNode(f flags) (config.Layout, *config.Config, error) {
	l, err := layoutFrom(f)
	if err != nil {
		return l, nil, err
	}
	if !l.Initialised() {
		return l, nil, fmt.Errorf("no node at %s\n\n  create one:  pscluster init --root %s", l.Root, l.Root)
	}
	cfg, err := config.Load(l)
	if err != nil {
		return l, nil, err
	}
	device, err := identity.LoadOrCreate(l.DeviceKey())
	if err != nil {
		return l, nil, fmt.Errorf("load device identity: %w", err)
	}
	if cfg.Node.ID != device.ID {
		cfg.Node.ID = device.ID
	}
	if err := config.Save(l, cfg); err != nil {
		return l, nil, err
	}
	return l, cfg, nil
}

// ----------------------------------------------------------------- init ----

func cmdInit(args []string) error {
	f := parseFlags(args, "force")
	l, err := layoutFrom(f)
	if err != nil {
		return err
	}
	if l.Initialised() && !f.has("force") {
		return fmt.Errorf("a node already exists at %s\n\n  run it:  pscluster serve --root %s", l.Root, l.Root)
	}

	cfg := config.Defaults()
	cfg.Node.Name = f.get("name", cfg.Node.Name)
	cfg.Cluster.Name = f.get("cluster", cfg.Node.Name+"-cluster")
	cfg.Network.Bind = f.get("bind", cfg.Network.Bind)
	if p := f.get("port", ""); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("--port must be a number between 1 and 65535, got %q", p)
		}
		cfg.Network.Port = n
	}
	if p := f.get("peer-port", ""); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("--peer-port must be a number between 1 and 65535, got %q", p)
		}
		cfg.Network.PeerPort = n
	}
	cfg.Network.Advertise = strings.TrimRight(f.get("advertise", cfg.Network.Advertise), "/")
	if server := strings.TrimRight(f.get("account-server", cfg.Account.Server), "/"); server != "" {
		if _, err := accountclient.New(server); err != nil {
			return err
		}
		cfg.Account.Server = server
	}
	if err := l.EnsureDirs(); err != nil {
		return err
	}
	device, err := identity.LoadOrCreate(l.DeviceKey())
	if err != nil {
		return fmt.Errorf("create device identity: %w", err)
	}
	cfg.Node.ID = device.ID
	cfg.EnsureMemberships()
	_, fingerprint, err := identity.TLSCertificate(device, l.DeviceCert())
	if err != nil {
		return fmt.Errorf("create mesh certificate: %w", err)
	}
	if err := config.Save(l, cfg); err != nil {
		return err
	}

	st, err := store.Open(l.Database())
	if err != nil {
		return err
	}
	defer st.Close()
	info := sysinfo.Probe(l.Root)
	if err := syncNetworks(st, cfg, device, fingerprint, info); err != nil {
		return err
	}

	if err := st.UpsertMachine(store.Machine{
		ID: cfg.Node.ID, Name: cfg.Node.Name, Roles: cfg.RoleNames(),
		OS: info.OS, Arch: info.Arch, IsSelf: true, LastSeen: store.Now(),
	}); err != nil {
		return err
	}

	fmt.Printf("\n  %s\n\n", version.Product)
	fmt.Printf("  Cluster   %s\n", cfg.Cluster.Name)
	fmt.Printf("  Node      %s   %s\n", cfg.Node.Name, strings.Join(cfg.RoleNames(), " · "))
	fmt.Printf("  Root      %s\n", l.Root)
	fmt.Printf("  Machine   %d cores · %s RAM · %s\n",
		info.CPUCores, humanMB(info.RAMTotalMB), gpuSummary(info))
	fmt.Printf("  Disk      %.0f GB free of %.0f GB\n", info.DiskFreeGB, info.DiskTotalGB)
	if !gitrepo.Available() {
		fmt.Printf("\n  Note      git is not installed — project history is unavailable\n" +
			"            until you install it (apt install git).\n")
	}
	fmt.Printf("\n  Everything this node stores is under that one directory.\n")
	fmt.Printf("\n  Start it:  pscluster serve --root %s\n\n", l.Root)
	return nil
}

// syncNetworks projects config memberships into the relational model. Config
// is the local machine's authority; the database is the queryable directory
// shared with the interface and, later, remote peers.
func syncNetworks(st *store.Store, cfg *config.Config, device *identity.Device, fingerprint string, info sysinfo.Info) error {
	account := store.Account{ID: cfg.AccountID(), Username: cfg.Account.Username,
		DisplayName: cfg.Account.DisplayName}
	if account.Username == "" {
		account.Username, account.DisplayName = cfg.Node.Name, cfg.Node.Name
		account.PublicKey = base64.RawURLEncoding.EncodeToString(device.Public)
	}
	if err := st.UpsertAccount(account); err != nil {
		return err
	}
	for _, membership := range cfg.Memberships {
		if _, err := st.NetworkByID(membership.ID); errors.Is(err, store.ErrNotFound) {
			if err := st.UpsertNetwork(store.Network{
				ID: membership.ID, Name: membership.Name, OwnerAccountID: account.ID,
			}); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if _, err := st.NetworkMember(membership.ID, account.ID); errors.Is(err, store.ErrNotFound) {
			role := membership.AccountRole
			if role == "" {
				role = store.NetworkOwner
			}
			if err := st.AddNetworkMember(membership.ID, account.ID, role); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		policyRaw, _ := json.Marshal(membership.Policy)
		policy := map[string]any{}
		_ = json.Unmarshal(policyRaw, &policy)
		roles := make([]string, 0, len(membership.Roles))
		for _, role := range membership.Roles {
			roles = append(roles, string(role))
		}
		if err := st.UpsertNetworkNode(store.NetworkNode{
			NetworkID: membership.ID, NodeID: cfg.Node.ID, Name: cfg.Node.Name,
			Roles: roles, OS: info.OS, Arch: info.Arch,
			PublicKey: account.PublicKey, Fingerprint: fingerprint,
			Address: meshEndpoint(cfg), Policy: policy,
			Capacity: map[string]any{
				"cpu_cores": info.CPUCores, "ram_total_mb": info.RAMTotalMB,
				"disk_total_gb": info.DiskTotalGB, "gpus": info.GPUs,
			},
			IsSelf: true, LastSeen: store.Now(),
		}); err != nil {
			return err
		}
	}
	return st.AssignProjectsToNetwork(cfg.ActiveNetwork)
}

func meshEndpoint(cfg *config.Config) string {
	if endpoint := strings.TrimSpace(cfg.Network.Advertise); endpoint != "" {
		return strings.TrimRight(endpoint, "/")
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "localhost"
	}
	return "https://" + host + ":" + strconv.Itoa(cfg.Network.PeerPort)
}

func humanMB(mb int) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.0f GB", float64(mb)/1024)
	}
	return fmt.Sprintf("%d MB", mb)
}

func gpuSummary(i sysinfo.Info) string {
	if len(i.GPUs) == 0 {
		return "no GPU detected"
	}
	names := make([]string, 0, len(i.GPUs))
	for _, g := range i.GPUs {
		names = append(names, fmt.Sprintf("%s %s", g.Name, humanMB(g.VRAMTotalMB)))
	}
	return strings.Join(names, ", ")
}

// ---------------------------------------------------------------- serve ----

func cmdServe(args []string) error {
	f := parseFlags(args)
	l, cfg, err := openNode(f)
	if err != nil {
		return err
	}
	if p := f.get("port", ""); p != "" {
		n, convErr := strconv.Atoi(p)
		if convErr != nil {
			return fmt.Errorf("--port must be a number, got %q", p)
		}
		cfg.Network.Port = n
	}
	if b := f.get("bind", ""); b != "" {
		cfg.Network.Bind = b
	}
	if p := f.get("peer-port", ""); p != "" {
		n, convErr := strconv.Atoi(p)
		if convErr != nil || n < 1 || n > 65535 {
			return fmt.Errorf("--peer-port must be a number between 1 and 65535, got %q", p)
		}
		cfg.Network.PeerPort = n
	}
	if err := l.EnsureDirs(); err != nil {
		return err
	}

	st, err := store.Open(l.Database())
	if err != nil {
		return err
	}
	defer st.Close()
	device, err := identity.LoadOrCreate(l.DeviceKey())
	if err != nil {
		return err
	}
	info := sysinfo.Probe(l.Root)
	certificate, fingerprint, err := identity.TLSCertificate(device, l.DeviceCert())
	if err != nil {
		return fmt.Errorf("load mesh certificate: %w", err)
	}
	if err := syncNetworks(st, cfg, device, fingerprint, info); err != nil {
		return err
	}

	// A job cannot still be running if the daemon supervising it is not, so
	// anything left mid-flight by an unclean shutdown is closed out honestly.
	if n, err := st.RecoverRunningJobs(); err == nil && n > 0 {
		log.Printf("recovered %d job(s) interrupted by a previous shutdown", n)
	}

	_ = st.UpsertMachine(store.Machine{
		ID: cfg.Node.ID, Name: cfg.Node.Name, Roles: cfg.RoleNames(),
		OS: info.OS, Arch: info.Arch, IsSelf: true, LastSeen: store.Now(),
	})

	hub := events.NewHub()
	sup := jobs.NewSupervisor(st, hub, l, cfg)
	notebooks := notebook.NewManager()
	defer notebooks.Close()
	collaboration := collab.New(st)
	datasets := dataset.New(l, st)
	up := updater.New(cfg, l, hub)
	srv := api.New(cfg, l, st, hub, sup, notebooks, collaboration, datasets, up, device, fingerprint, web.Assets)

	// The command line reaches this daemon with the install root's local token,
	// which keeps it working once the node has an owner account.
	if token, err := config.EnsureCLIToken(l); err != nil {
		log.Printf("local token unavailable, the command line will not reach this node: %v", err)
	} else {
		srv.UseLocalToken(token)
	}

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv.StartTelemetry(ctx, 3*time.Second)
	srv.StartPeerDiscovery(ctx, 30*time.Second)
	srv.StartAccountCheckIn(ctx, time.Minute)
	srv.StartControllerCheckIn(ctx, 15*time.Second)
	up.Run(ctx)
	writePID(l)
	defer os.Remove(l.PIDFile())

	ready := func(addr string) {
		fmt.Printf("\n  %s  ·  %s\n\n", version.Product, cfg.Cluster.Name)
		fmt.Printf("  %s\n\n", addr)
		fmt.Printf("  node    %s   %s\n", cfg.Node.Name, strings.Join(cfg.RoleNames(), " · "))
		fmt.Printf("  root    %s\n", l.Root)
		fmt.Printf("  host    %d cores · %s RAM · %s\n\n",
			info.CPUCores, humanMB(info.RAMTotalMB), gpuSummary(info))
		fmt.Printf("  Ctrl-C to stop.\n\n")
	}

	errCh := make(chan error, 2)
	go func() { errCh <- srv.ListenAndServe(ctx, ready) }()
	go func() { errCh <- srv.ListenAndServeMesh(ctx, certificate) }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		fmt.Printf("\n  stopping…\n")
		sup.StopAll()
		select {
		case <-errCh:
		case <-time.After(12 * time.Second):
		}
		fmt.Printf("  stopped.\n")
		return nil
	}
}

// writePID records the daemon's pid inside the root, so nothing about a running
// node lives outside its own directory.
func writePID(l config.Layout) {
	_ = os.WriteFile(l.PIDFile(), []byte(strconv.Itoa(os.Getpid())), 0o640)
}

// --------------------------------------------------------------- status ----

func cmdStatus(args []string) error {
	f := parseFlags(args)
	l, cfg, err := openNode(f)
	if err != nil {
		return err
	}
	st, err := store.Open(l.Database())
	if err != nil {
		return err
	}
	defer st.Close()

	projects, _ := st.Projects()
	active, _ := st.ActiveJobs()
	recent, _ := st.Jobs(5)
	info := sysinfo.Probe(l.Root)

	fmt.Printf("\n  %s  ·  %s\n\n", version.Product, cfg.Cluster.Name)
	fmt.Printf("  node      %s   %s\n", cfg.Node.Name, strings.Join(cfg.RoleNames(), " · "))
	fmt.Printf("  root      %s\n", l.Root)
	fmt.Printf("  daemon    %s\n", daemonState(l))
	fmt.Printf("  host      %d cores · %s RAM · load %.2f\n",
		info.CPUCores, humanMB(info.RAMTotalMB), info.LoadAvg1)
	fmt.Printf("  gpu       %s\n", gpuSummary(info))
	fmt.Printf("  disk      %.0f GB free of %.0f GB\n", info.DiskFreeGB, info.DiskTotalGB)
	fmt.Printf("  projects  %d\n", len(projects))
	fmt.Printf("  running   %d\n\n", len(active))

	if len(recent) > 0 {
		fmt.Printf("  recent jobs\n")
		for _, j := range recent {
			fmt.Printf("    %-10s %-9s %s\n", j.ID[:8], j.State, truncate(j.Title, 48))
		}
		fmt.Println()
	}
	return nil
}

// daemonState reports whether a serve process is alive, by checking that the
// recorded pid still exists. Signal 0 tests existence without delivering.
func daemonState(l config.Layout) string {
	raw, err := os.ReadFile(l.PIDFile())
	if err != nil {
		return "not running"
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return "not running"
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return "not running"
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return "not running (stale pid file)"
	}
	return fmt.Sprintf("running (pid %d)", pid)
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// ------------------------------------------------------------------ run ----

func cmdRun(args []string) error {
	f := parseFlags(args)
	if len(f.rest) == 0 {
		return errors.New("nothing to run\n\n  example:  pscluster run python train.py")
	}
	l, cfg, err := openNode(f)
	if err != nil {
		return err
	}
	st, err := store.Open(l.Database())
	if err != nil {
		return err
	}
	defer st.Close()

	hub := events.NewHub()
	sup := jobs.NewSupervisor(st, hub, l, cfg)

	command := strings.Join(f.rest, " ")
	req := jobs.Request{Kind: "script", Command: command, Workdir: l.Root}
	if name := f.get("project", ""); name != "" {
		p, err := st.ProjectByNameInNetwork(cfg.ActiveNetwork, name)
		if err != nil {
			return fmt.Errorf("no project called %q", name)
		}
		req.ProjectID, req.Project = p.ID, p.Name
		req.Workdir = filepath.Join(l.Projects(), p.NetworkID, p.Name)
		if _, statErr := os.Stat(req.Workdir); errors.Is(statErr, os.ErrNotExist) {
			req.Workdir = filepath.Join(l.Projects(), p.Name)
		}
	}

	sub := hub.Subscribe()
	defer sub.Close()

	job, err := sup.Start(req)
	if err != nil {
		return err
	}
	fmt.Printf("job %s  ·  %s\n\n", job.ID[:8], command)

	for ev := range sub.C {
		switch ev.Topic {
		case "job.log":
			if line, ok := ev.Data.(jobs.LogLine); ok && line.JobID == job.ID {
				if line.Stream == "stderr" {
					fmt.Fprintln(os.Stderr, line.Text)
				} else {
					fmt.Println(line.Text)
				}
			}
		case "job.state":
			if j, ok := ev.Data.(store.Job); ok && j.ID == job.ID && j.Terminal() {
				fmt.Printf("\n%s (exit %d)\n", j.State, j.ExitCode)
				if j.State != store.JobSucceeded {
					os.Exit(1)
				}
				return nil
			}
		}
	}
	return nil
}

// --------------------------------------------------------------- config ----

func cmdConfig(args []string) error {
	f := parseFlags(args)
	l, cfg, err := openNode(f)
	if err != nil {
		return err
	}
	if len(f.rest) == 0 || f.rest[0] == "show" {
		raw, err := os.ReadFile(l.ConfigFile())
		if err != nil {
			return err
		}
		fmt.Print(string(raw))
		return nil
	}

	switch f.rest[0] {
	case "path":
		fmt.Println(l.ConfigFile())
		return nil
	case "root":
		fmt.Println(l.Root)
		return nil
	case "get":
		if len(f.rest) < 2 {
			return errors.New("usage: pscluster config get <key>")
		}
		v, err := configGet(cfg, l, f.rest[1])
		if err != nil {
			return err
		}
		fmt.Println(v)
		return nil
	case "set":
		if len(f.rest) < 3 {
			return errors.New("usage: pscluster config set <key> <value>")
		}
		if err := configSet(cfg, f.rest[1], f.rest[2]); err != nil {
			return err
		}
		if err := config.Save(l, cfg); err != nil {
			return err
		}
		fmt.Printf("%s = %s\n", f.rest[1], f.rest[2])
		fmt.Printf("restart the node for this to take effect\n")
		return nil
	default:
		return fmt.Errorf("unknown config command %q (show, get, set, path, root)", f.rest[0])
	}
}

func configGet(c *config.Config, l config.Layout, key string) (string, error) {
	switch key {
	case "node.name":
		return c.Node.Name, nil
	case "node.roles":
		return strings.Join(c.RoleNames(), ","), nil
	case "cluster.name":
		return c.Cluster.Name, nil
	case "network.bind":
		return c.Network.Bind, nil
	case "network.port":
		return strconv.Itoa(c.Network.Port), nil
	case "account.server":
		return c.Account.Server, nil
	case "worker.enabled":
		return strconv.FormatBool(c.Worker.Enabled), nil
	case "worker.allow_terminal":
		return strconv.FormatBool(c.Worker.AllowTerminal), nil
	case "root":
		return l.Root, nil
	default:
		return "", fmt.Errorf("unknown key %q", key)
	}
}

func configSet(c *config.Config, key, value string) error {
	parseBool := func() (bool, error) {
		b, err := strconv.ParseBool(value)
		if err != nil {
			return false, fmt.Errorf("%s expects true or false, got %q", key, value)
		}
		return b, nil
	}
	switch key {
	case "node.name":
		if strings.TrimSpace(value) == "" {
			return errors.New("node.name cannot be empty")
		}
		c.Node.Name = value
	case "cluster.name":
		c.UpdateActiveMembership(func(m *config.MembershipConfig) { m.Name = value })
	case "network.bind":
		c.Network.Bind = value
	case "network.port":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > 65535 {
			return fmt.Errorf("network.port expects 0-65535, got %q", value)
		}
		c.Network.Port = n
	case "account.server":
		value = strings.TrimRight(strings.TrimSpace(value), "/")
		if value != "" {
			if _, err := accountclient.New(value); err != nil {
				return err
			}
		}
		c.Account.Server = value
	case "worker.enabled":
		b, err := parseBool()
		if err != nil {
			return err
		}
		c.Worker.Enabled = b
	case "worker.allow_jobs":
		b, err := parseBool()
		if err != nil {
			return err
		}
		c.Worker.AllowJobs = b
	case "worker.allow_gpu":
		b, err := parseBool()
		if err != nil {
			return err
		}
		c.Worker.AllowGPU = b
	case "worker.allow_terminal":
		b, err := parseBool()
		if err != nil {
			return err
		}
		c.Worker.AllowTerminal = b
	default:
		return fmt.Errorf("unknown key %q", key)
	}
	return nil
}

var _ = runtime.NumCPU
