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
		l.ConfigFile(), l.Database(), l.PIDFile())

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
	want.Cluster.Name = "HomeLab"
	want.Node.Roles = []Role{RoleMaster, RoleWorker}
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
	if !got.HasRole(RoleMaster) || !got.HasRole(RoleWorker) || got.HasRole(RoleController) {
		t.Errorf("roles round-tripped wrongly: %v", got.RoleNames())
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
