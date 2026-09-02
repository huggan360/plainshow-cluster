package store

import "testing"

func TestMemberLifecycle(t *testing.T) {
	store := open(t)
	project := Project{ID: "project-1", Name: "demo"}
	if err := store.CreateProject(&project); err != nil {
		t.Fatal(err)
	}

	owner := Member{
		ProjectID: project.ID, Username: "owner", Owner: true,
		Capabilities: OwnerCapabilities(),
	}
	if err := store.UpsertMember(owner); err != nil {
		t.Fatal(err)
	}
	member := Member{
		ProjectID: project.ID, Username: "albin", GitHubLogin: "Albin",
		Capabilities: DefaultCapabilities(), GitHubRole: "push",
	}
	if err := store.UpsertMember(member); err != nil {
		t.Fatal(err)
	}

	members, err := store.Members(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 || !members[0].Owner {
		t.Fatalf("members not returned owner-first: %#v", members)
	}
	byLogin, err := store.MemberByGitHubLogin(project.ID, "albin")
	if err != nil || byLogin.Username != "albin" || !byLogin.Can(CapTrain) {
		t.Fatalf("GitHub lookup failed: %#v, %v", byLogin, err)
	}
	if err := store.RemoveMember(project.ID, "owner"); err == nil {
		t.Fatal("owner removal succeeded")
	}
	if err := store.RemoveMember(project.ID, "albin"); err != nil {
		t.Fatal(err)
	}
}
