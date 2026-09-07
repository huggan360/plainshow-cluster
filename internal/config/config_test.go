package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestLayoutStaysUnderOneRoot is the layout promise the product makes: a node
// writes nothing outside the directory it was given.
func TestLayoutStaysUnderOneRoot(t *testing.T) {
	root := t.TempDir()
	l, err := NewLayout(root)
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	paths := append(l.Dirs(),
		l.ConfigFile(), l.Database(), l.DeviceKey(), l.PIDFile(), l.RuntimeFile(), l.RayState())

	for _, p := range paths {
		if p != l.Root && !strings.HasPrefix(p, l.Root+string(filepath.Separator)) {
			t.Errorf("%q is outside the install root %q", p, l.Root)
		}
	}
}

func TestEnsureDirsIsRepeatable(t *testing.T) {
	l, err := NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := l.EnsureDirs(); err != nil {
			t.Fatalf("EnsureDirs (pass %d): %v", i+1, err)
		}
	}
	for _, d := range l.Dirs() {
		if info, err := os.Stat(d); err != nil || !info.IsDir() {
			t.Errorf("%q was not created", d)
		}
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	l, err := NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	want := Defaults()
	want.Node.Name = "fredrik-pc"
	want.Memberships = []MembershipConfig{{
		ID: "home", Name: "HomeLab", Roles: []Role{RoleWorker},
		AccountRole: "owner", ManagementKey: NewSecret(), Enabled: true, Policy: want.Worker,
	}}
	want.SetActiveNetwork("home")
	want.Node.Roles = []Role{RoleWorker}
	want.Network.Port = 9999
	want.Worker.AllowTerminal = true

	if err := Save(l, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(l)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Node.Name != want.Node.Name || got.Cluster.Name != want.Cluster.Name ||
		got.Network.Port != want.Network.Port ||
		got.Worker.AllowTerminal != want.Worker.AllowTerminal {
		t.Errorf("round trip lost settings: %+v", got)
	}
	// Every device carries exactly one role now; master and controller are
	// retired and normalise away on load.
	if !got.HasRole(RoleWorker) || len(got.Node.Roles) != 1 {
		t.Errorf("roles round-tripped wrongly: %v", got.RoleNames())
	}
	if len(got.Memberships) != 1 || got.ActiveNetwork != got.Cluster.ID {
		t.Errorf("single-cluster config did not migrate to memberships: %+v", got.Memberships)
	}
}

func TestMultipleNetworkMemberships(t *testing.T) {
	cfg := Defaults()
	cfg.Memberships = []MembershipConfig{{
		ID: "first", Name: "First", Roles: []Role{RoleWorker},
		AccountRole: "owner", ManagementKey: NewSecret(), Enabled: true, Policy: cfg.Worker,
	}}
	cfg.SetActiveNetwork("first")
	first := cfg.Memberships[0]
	second := MembershipConfig{
		ID: "friends", Name: "Friends", Roles: []Role{RoleWorker},
		Enabled: true, Policy: cfg.Worker,
	}
	cfg.Memberships = append(cfg.Memberships, second)
	if !cfg.SetActiveNetwork(second.ID) {
		t.Fatal("could not select second network")
	}
	if cfg.Cluster.Name != "Friends" || cfg.HasRole(RoleMaster) || !cfg.HasRole(RoleWorker) {
		t.Fatalf("wrong active membership: %+v", cfg.ActiveMembership())
	}
	cfg.UpdateActiveMembership(func(m *MembershipConfig) { m.Name = "Lab" })
	if cfg.ActiveMembership().Name != "Lab" || cfg.Memberships[0].ID != first.ID {
		t.Fatalf("membership update escaped its network: %+v", cfg.Memberships)
	}
}

func TestNoNetworksSurvivesSaveAndLoad(t *testing.T) {
	l, err := NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	cfg := Defaults()
	cfg.Memberships = nil
	cfg.ActiveNetwork = ""
	if err := Save(l, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(l)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Memberships) != 0 || loaded.ActiveNetwork != "" || loaded.Cluster.ID != "" {
		t.Fatalf("empty network state was regenerated: %+v", loaded)
	}
}

// TestLoadAppliesDefaults covers a hand-edited config that omits fields.
func TestLoadAppliesDefaults(t *testing.T) {
	l, err := NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.ConfigFile(), []byte("node:\n  name: solo\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	got, err := Load(l)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Node.Name != "solo" {
		t.Errorf("Node.Name = %q, want solo", got.Node.Name)
	}
	if got.Node.ID == "" || got.Cluster.ID == "" || got.Network.Bind == "" ||
		len(got.Node.Roles) == 0 {
		t.Errorf("defaults were not applied: %+v", got)
	}
}

func TestLoadWithoutNodeExplains(t *testing.T) {
	l, err := NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = Load(l)
	if err == nil {
		t.Fatal("Load succeeded with no node present")
	}
	if !strings.Contains(err.Error(), "pscluster init") {
		t.Errorf("error should tell the user how to fix it, got: %v", err)
	}
}

func TestRoleValidation(t *testing.T) {
	for _, r := range AllRoles {
		if !r.Valid() {
			t.Errorf("%q should be valid", r)
		}
	}
	for _, r := range []Role{"", "Master", "gpu", "web"} {
		if r.Valid() {
			t.Errorf("%q should not be valid", r)
		}
	}
}

func TestDefaultRootHonoursEnv(t *testing.T) {
	t.Setenv(EnvRoot, "/srv/somewhere")
	if got := DefaultRoot(); got != "/srv/somewhere" {
		t.Errorf("DefaultRoot() = %q, want /srv/somewhere", got)
	}
}

// TestJSONFieldNames locks the wire contract the interface reads.
//
// These structs are sent to the browser as JSON. They carried only yaml tags
// once, so the API emitted Go field names ("Enabled", "AllowGPU") while the
// interface read snake_case — every toggle silently read as undefined, and a
// settings save turned unmentioned permissions off. A shape test is cheap
// insurance against that returning.
func TestJSONFieldNames(t *testing.T) {
	raw, err := json.Marshal(Defaults())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	want := map[string][]string{
		"node":    {"id", "name", "roles"},
		"cluster": {"id", "name"},
		"network": {"bind", "port", "advertise"},
		"worker": {"enabled", "allow_jobs", "allow_gpu", "allow_terminal",
			"max_cpu", "max_ram_mb"},
		"update": {"enabled", "repository", "channel", "check_every", "automatic"},
	}
	for block, keys := range want {
		body, ok := got[block]
		if !ok {
			t.Errorf("config JSON has no %q block; keys are %v", block, mapKeys(got))
			continue
		}
		var fields map[string]any
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Fatalf("unmarshal %s: %v", block, err)
		}
		for _, key := range keys {
			if _, ok := fields[key]; !ok {
				t.Errorf("%s JSON has no %q (got %v)", block, key, mapKeys(fields))
			}
		}
	}
}

// TestWorkerPolicyPartialUpdate covers the merge behaviour the settings
// endpoint relies on: decoding a partial object must not clear what it omits.
func TestWorkerPolicyPartialUpdate(t *testing.T) {
	current := WorkerConfig{
		Enabled: true, AllowJobs: true, AllowGPU: true,
		AllowTerminal: true, MaxCPU: 8, MaxRAMMB: 16384,
	}
	if err := json.Unmarshal([]byte(`{"enabled":false}`), &current); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if current.Enabled {
		t.Error("enabled should have been turned off")
	}
	if !current.AllowJobs || !current.AllowGPU || !current.AllowTerminal ||
		current.MaxCPU != 8 || current.MaxRAMMB != 16384 {
		t.Errorf("a partial update cleared fields it did not mention: %+v", current)
	}
}

func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestRetiredRolesBecomeOrdinaryDevices: an existing install has "master" and
// "controller" written into its config. Upgrading must not refuse to start, and
// must not leave a machine still believing it is special.
func TestRetiredRolesBecomeOrdinaryDevices(t *testing.T) {
	cases := []struct {
		name string
		in   []Role
		want []Role
	}{
		{"old master node", []Role{RoleMaster, RoleWorker}, []Role{RoleWorker}},
		{"old controller", []Role{RoleController}, []Role{RoleWorker}},
		{"all three", []Role{RoleController, RoleMaster, RoleWorker}, []Role{RoleWorker}},
		{"already current", []Role{RoleWorker}, []Role{RoleWorker}},
		{"empty", nil, []Role{RoleWorker}},
		{"unknown dropped", []Role{"gpu", RoleWorker}, []Role{RoleWorker}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Normalise(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("Normalise(%v) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("Normalise(%v) = %v, want %v", tc.in, got, tc.want)
				}
			}
		})
	}
}

// TestLoadNormalisesAnOldConfig covers the upgrade path end to end.
func TestLoadNormalisesAnOldConfig(t *testing.T) {
	l, err := NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	old := "node:\n  name: fredrik-pc\n  roles:\n    - master\n    - worker\n"
	if err := os.WriteFile(l.ConfigFile(), []byte(old), 0o640); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(l)
	if err != nil {
		t.Fatalf("an old config would not load: %v", err)
	}
	if cfg.HasRole(RoleMaster) {
		t.Error("the machine still believes it is a master")
	}
	if !cfg.HasRole(RoleWorker) {
		t.Error("the machine is not a device at all")
	}
}

// TestUpgradingFillsInPerNetworkProjectSync is the trap this version number
// exists for.
//
// Load unmarshals onto Defaults(), so a missing top-level key keeps its
// default. A slice element does not work that way: every membership is built
// from zero, so a field added after those memberships were written reads as
// false. For project sync that would silently take working machines out of
// every network they belong to.
func TestUpgradingFillsInPerNetworkProjectSync(t *testing.T) {
	dir := t.TempDir()
	layout, err := NewLayout(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	// A settings document as an older build wrote it: no version, no
	// allow_project_sync anywhere.
	old := `node:
    id: node-1
    name: box
    roles: [worker]
cluster: {id: c1, name: lab}
active_network: net-1
memberships:
    - id: net-1
      name: Lab
      enabled: true
      policy: {enabled: true, allow_jobs: true, allow_gpu: true, allow_terminal: false}
worker: {enabled: true, allow_jobs: true, allow_gpu: true, allow_terminal: false}
`
	if err := os.WriteFile(layout.ConfigFile(), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(layout)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Worker.AllowProjectSync {
		t.Error("the device-wide setting did not survive the upgrade")
	}
	if len(cfg.Memberships) != 1 || !cfg.Memberships[0].Policy.AllowProjectSync {
		t.Errorf("membership policy = %+v, want project sync on", cfg.Memberships)
	}
	if cfg.Version != CurrentVersion {
		t.Errorf("version = %d, want %d", cfg.Version, CurrentVersion)
	}
}

// TestAnExplicitNoIsKept: once the document carries the current version, a
// machine owner who turned project files off keeps them off.
func TestAnExplicitNoIsKept(t *testing.T) {
	dir := t.TempDir()
	layout, err := NewLayout(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	current := `version: 2
node: {id: node-1, name: box, roles: [worker]}
cluster: {id: c1, name: lab}
memberships:
    - id: net-1
      name: Lab
      enabled: true
      policy: {enabled: true, allow_jobs: true, allow_project_sync: false}
worker: {enabled: true, allow_jobs: true, allow_project_sync: false}
`
	if err := os.WriteFile(layout.ConfigFile(), []byte(current), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(layout)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Worker.AllowProjectSync || cfg.Memberships[0].Policy.AllowProjectSync {
		t.Error("an explicit refusal was overwritten on load")
	}
}
