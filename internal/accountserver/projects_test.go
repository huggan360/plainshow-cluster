package accountserver

import "testing"

// TestProjectsFollowTheAccountButFilesDoNot is the whole point of this table.
// A second machine has to learn that a project exists in order to offer to
// fetch it — and must learn nothing else, because data never passes through
// this service.
func TestProjectsFollowTheAccountButFilesDoNot(t *testing.T) {
	store, hugo, albin := twoAccounts(t)

	made, err := store.RegisterProject(hugo.ID, ProjectRegistration{
		ID: "p1", Name: "vision", Repository: "huggan360/vision",
		Branch: "main", SizeKB: 4096, Members: []string{"albin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if made.Role != "owner" || made.SizeKB != 4096 {
		t.Fatalf("registered = %+v", made)
	}

	mine, err := store.ProjectsForAccount(hugo.ID)
	if err != nil || len(mine) != 1 {
		t.Fatalf("hugo's projects = %+v, %v", mine, err)
	}
	// The size is what somebody is told before agreeing to a download, so it
	// has to survive the trip.
	if mine[0].SizeKB != 4096 || mine[0].Repository != "huggan360/vision" {
		t.Errorf("metadata did not survive: %+v", mine[0])
	}

	// A member named by the owner sees it on their own machines.
	theirs, err := store.ProjectsForAccount(albin.ID)
	if err != nil || len(theirs) != 1 || theirs[0].Role != "member" {
		t.Fatalf("albin's projects = %+v, %v", theirs, err)
	}
}

// TestAProjectBelongsToWhoeverMadeIt: two people can hold the same project, and
// a report from the one who did not make it must not rewrite the owner's row.
func TestAProjectBelongsToWhoeverMadeIt(t *testing.T) {
	store, hugo, albin := twoAccounts(t)
	if _, err := store.RegisterProject(hugo.ID, ProjectRegistration{
		ID: "p1", Name: "vision", SizeKB: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterProject(albin.ID, ProjectRegistration{
		ID: "p1", Name: "hijacked", SizeKB: 999,
	}); err != nil {
		t.Fatal(err)
	}
	mine, _ := store.ProjectsForAccount(hugo.ID)
	if len(mine) != 1 || mine[0].Name != "vision" || mine[0].SizeKB != 100 {
		t.Fatalf("somebody else's report changed the owner's project: %+v", mine)
	}
	// And it did not make them a member by reporting it.
	if theirs, _ := store.ProjectsForAccount(albin.ID); len(theirs) != 0 {
		t.Errorf("reporting a project granted access to it: %+v", theirs)
	}
}

func TestForgettingAProjectDeletesForOwnerAndLeavesForMember(t *testing.T) {
	store, hugo, albin := twoAccounts(t)
	if _, err := store.RegisterProject(hugo.ID, ProjectRegistration{
		ID: "p1", Name: "vision", Members: []string{"albin"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ForgetProject(albin.ID, "p1"); err != nil {
		t.Fatalf("member could not leave project: %v", err)
	}
	if theirs, _ := store.ProjectsForAccount(albin.ID); len(theirs) != 0 {
		t.Fatalf("member still sees project after leaving: %+v", theirs)
	}
	if mine, _ := store.ProjectsForAccount(hugo.ID); len(mine) != 1 {
		t.Fatal("member leaving deleted the owner's project")
	}
	if _, err := store.ForgetProject(hugo.ID, "p1"); err != nil {
		t.Fatal(err)
	}
	if mine, _ := store.ProjectsForAccount(hugo.ID); len(mine) != 0 {
		t.Error("the project survived being forgotten")
	}
	if _, err := store.ForgetProject(hugo.ID, "p1"); err != nil {
		t.Fatalf("repeated delete was not idempotent: %v", err)
	}
}

// TestTheGitHubTokenIsTheAccountsNotTheMachines: connecting on one machine has
// to reach the others, and disconnecting has to clear it rather than leaving a
// copy that the next check-in puts straight back.
func TestTheGitHubTokenIsTheAccountsNotTheMachines(t *testing.T) {
	store, hugo, albin := twoAccounts(t)

	if token, err := store.GitHubToken(hugo.ID); err != nil || token != "" {
		t.Fatalf("a fresh account had a token: %q, %v", token, err)
	}
	if err := store.SetGitHubToken(hugo.ID, "ghp_example"); err != nil {
		t.Fatal(err)
	}
	token, err := store.GitHubToken(hugo.ID)
	if err != nil || token != "ghp_example" {
		t.Fatalf("token = %q, %v", token, err)
	}
	// Strictly one account's own.
	if other, _ := store.GitHubToken(albin.ID); other != "" {
		t.Errorf("another account could read the token: %q", other)
	}
	if err := store.SetGitHubToken(hugo.ID, ""); err != nil {
		t.Fatal(err)
	}
	if token, _ := store.GitHubToken(hugo.ID); token != "" {
		t.Error("disconnecting left the token behind")
	}
}
