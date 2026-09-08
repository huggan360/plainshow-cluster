package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/mesh"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

func TestBadTransferPreservesPreviousFiles(t *testing.T) {
	s := syncingNode(t, true, true)
	project := store.Project{ID: "safe-project", Name: "safe", NetworkID: "net-1"}
	if err := s.store.CreateProject(&project); err != nil {
		t.Fatal(err)
	}
	dir := s.projectDir(project)
	if err := os.MkdirAll(dir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("keep me"), 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := s.materialiseProject("net-1", projectPayload{Project: "safe", Archive: []byte("broken gzip")}); err == nil {
		t.Fatal("accepted broken archive")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "main.py"))
	if err != nil || string(raw) != "keep me" {
		t.Fatalf("previous file lost: %q %v", raw, err)
	}
}

func syncingNode(t *testing.T, device, network bool) *Server {
	t.Helper()
	srv, _ := newTestServer(t)
	srv.cfg.Worker.AllowProjectSync = device
	srv.cfg.Memberships = []config.MembershipConfig{{
		ID: "net-1", Name: "Lab", Enabled: true,
		Policy: config.WorkerConfig{AllowProjectSync: network},
	}}
	srv.cfg.ActiveNetwork = "net-1"
	return srv
}

// TestBothCeilingsApplyToProjectFiles: a network can narrow what a machine
// offers and never widen it, which is what makes it reasonable to join
// somebody else's network at all.
func TestBothCeilingsApplyToProjectFiles(t *testing.T) {
	cases := []struct {
		device, network, allowed bool
	}{
		{true, true, true},
		{true, false, false},
		{false, true, false},
		{false, false, false},
	}
	for _, item := range cases {
		srv := syncingNode(t, item.device, item.network)
		err := srv.projectSyncAllowed("net-1")
		if (err == nil) != item.allowed {
			t.Errorf("device=%v network=%v allowed=%v, want %v (err=%v)",
				item.device, item.network, err == nil, item.allowed, err)
		}
		if err != nil && err.Error() == "" {
			t.Error("a refusal with no explanation")
		}
	}
}

func TestProjectFilesAreRefusedForAnUnknownNetwork(t *testing.T) {
	srv := syncingNode(t, true, true)
	if err := srv.projectSyncAllowed("net-other"); err == nil {
		t.Fatal("a network this device does not belong to was accepted")
	}
}

// TestPausedNetworkRefusesFiles: pausing a network is somebody saying "not
// right now", and quietly still accepting its files would ignore that.
func TestPausedNetworkRefusesFiles(t *testing.T) {
	srv := syncingNode(t, true, true)
	srv.cfg.Memberships[0].Enabled = false
	if err := srv.projectSyncAllowed("net-1"); err == nil {
		t.Fatal("a paused network was still accepted")
	}
}

// TestAPeerThatPredatesTheSettingIsTreatedAsWilling. Every machine used to
// accept project files — that is how remote jobs have always worked — so an
// absent field means the peer is old, not that it refused.
func TestAPeerThatPredatesTheSettingIsTreatedAsWilling(t *testing.T) {
	old := store.NetworkNode{Policy: map[string]any{"allow_jobs": true}}
	if !policyAllowsProjectSync(old) {
		t.Error("a peer with no such field read as refusing")
	}
	no := store.NetworkNode{Policy: map[string]any{"allow_project_sync": false}}
	if policyAllowsProjectSync(no) {
		t.Error("a peer that refused read as willing")
	}
	yes := store.NetworkNode{Policy: map[string]any{"allow_project_sync": true}}
	if !policyAllowsProjectSync(yes) {
		t.Error("a peer that agreed read as refusing")
	}
}

// TestProjectsOnDiskReadsTheDiskNotTheDatabase is the distinction the whole
// feature rests on: a network's project list is not the same as what this
// machine can actually run.
func TestProjectsOnDiskReadsTheDiskNotTheDatabase(t *testing.T) {
	srv := syncingNode(t, true, true)
	if got := srv.projectsOnDisk("net-1"); len(got) != 0 {
		t.Fatalf("a machine with no files reported %v", got)
	}
	// A project row with no directory must still report nothing on disk.
	project := store.Project{ID: "p1", NetworkID: "net-1", Name: "vision"}
	if err := srv.store.CreateProject(&project); err != nil {
		t.Fatal(err)
	}
	if got := srv.projectsOnDisk("net-1"); len(got) != 0 {
		t.Fatalf("a project row with no directory reported as present: %v", got)
	}
	if srv.hasProjectFiles("net-1", "vision") {
		t.Error("hasProjectFiles agreed with the database instead of the disk")
	}
}

// TestReceivingAProjectReplacesTheDirectory: a half-overwritten working tree is
// worse than either version of it, so the sending machine's copy wins whole.
func TestReceivingAProjectReplacesTheDirectory(t *testing.T) {
	srv := syncingNode(t, true, true)
	dir := filepath.Join(srv.layout.Projects(), "net-1", "vision")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Something the sender does not have, which must not survive.
	if err := os.WriteFile(filepath.Join(dir, "stale.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "main.py"), []byte("print(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive, err := mesh.ArchiveDir(source)
	if err != nil {
		t.Fatal(err)
	}

	project, err := srv.materialiseProject("net-1", projectPayload{
		Project: "vision", Description: "from a peer", Archive: archive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if project.Name != "vision" {
		t.Fatalf("project = %+v", project)
	}
	if _, err := os.Stat(filepath.Join(dir, "main.py")); err != nil {
		t.Errorf("the received file is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stale.txt")); err == nil {
		t.Error("a file the sender did not have survived the transfer")
	}
	// And the machine now reports it as held, which is what makes it runnable.
	if !srv.hasProjectFiles("net-1", "vision") {
		t.Error("a project that was just written reports as absent")
	}
	if got := srv.projectsOnDisk("net-1"); len(got) != 1 || got[0] != "vision" {
		t.Errorf("projectsOnDisk = %v", got)
	}
}

// TestOwnershipIsCheckedAgainstEveryNameThisDeviceAnswersTo.
//
// Project membership is keyed by the name somebody is known by on GitHub, and a
// machine with no GitHub connected records its own node name instead. Neither
// is the Plainshow account username, so checking one guess refused the owner
// their own project.
func TestOwnershipIsCheckedAgainstEveryNameThisDeviceAnswersTo(t *testing.T) {
	srv := syncingNode(t, true, true)
	srv.cfg.Node.Name = "box-1"
	srv.cfg.Account.Username = "huggan360"

	project := store.Project{ID: "p1", NetworkID: "net-1", Name: "vision"}
	if err := srv.store.CreateProject(&project); err != nil {
		t.Fatal(err)
	}
	// Recorded under the node name, as a machine with no GitHub connected does.
	if err := srv.store.UpsertMember(store.Member{
		ProjectID: project.ID, Username: "box-1",
		Capabilities: store.OwnerCapabilities(), Owner: true,
	}); err != nil {
		t.Fatal(err)
	}
	if !srv.ownsProject(project.ID) {
		t.Error("the owner was refused their own project")
	}

	other := store.Project{ID: "p2", NetworkID: "net-1", Name: "someone-elses"}
	if err := srv.store.CreateProject(&other); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpsertMember(store.Member{
		ProjectID: other.ID, Username: "albin",
		Capabilities: store.OwnerCapabilities(), Owner: true,
	}); err != nil {
		t.Fatal(err)
	}
	if srv.ownsProject(other.ID) {
		t.Error("somebody else's project read as owned")
	}
	// A collaborator is not an owner, however much they may do.
	if err := srv.store.UpsertMember(store.Member{
		ProjectID: other.ID, Username: "box-1",
		Capabilities: store.OwnerCapabilities(), Owner: false,
	}); err != nil {
		t.Fatal(err)
	}
	if srv.ownsProject(other.ID) {
		t.Error("a collaborator with every capability read as the owner")
	}
}
