package github

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/store"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(body))}
}

func TestRoleForCapabilities(t *testing.T) {
	tests := []struct {
		name      string
		caps      map[string]bool
		supported []string
		want      string
	}{
		{"viewer", store.CapabilitySet(store.CapView), organisationRoles, "pull"},
		{"editor", store.CapabilitySet(store.CapView, store.CapCode), organisationRoles, "triage"},
		{"runner", store.CapabilitySet(store.CapView, store.CapRun), organisationRoles, "push"},
		{"trainer", store.CapabilitySet(store.CapView, store.CapTrain), organisationRoles, "maintain"},
		{"manager", store.OwnerCapabilities(), organisationRoles, "admin"},
		{"personal repository", store.CapabilitySet(store.CapView), personalRoles, "push"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RoleForCapabilities(tt.caps, tt.supported); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCapabilitiesForRole(t *testing.T) {
	admin := CapabilitiesForRole("admin")
	for _, capability := range store.Capabilities {
		if !admin[capability] {
			t.Errorf("admin is missing %s", capability)
		}
	}
	unknown := CapabilitiesForRole("unknown")
	if !unknown[store.CapView] || unknown[store.CapCode] {
		t.Fatalf("unknown role should degrade to view-only: %#v", unknown)
	}
}

func TestRepositoryValidation(t *testing.T) {
	for _, valid := range []string{"owner/repo", "a-b/c_d", "org.name/project.js"} {
		if !ValidRepository(valid) {
			t.Errorf("rejected valid repository %q", valid)
		}
	}
	for _, invalid := range []string{"repo", "/repo", "owner/", "a/b/c", "a b/c"} {
		if ValidRepository(invalid) {
			t.Errorf("accepted invalid repository %q", invalid)
		}
	}
}

func TestSyncKeepsMemberWithPendingGitHubInvitation(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/cluster.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	project := store.Project{ID: "p1", Name: "vision"}
	if err := st.CreateProject(&project); err != nil {
		t.Fatal(err)
	}
	member := store.Member{ProjectID: project.ID, Username: "albInc",
		GitHubLogin: "AlbinFlankLeon", GitHubRole: "push",
		Capabilities: store.DefaultCapabilities()}
	if err := st.UpsertMember(member); err != nil {
		t.Fatal(err)
	}

	client := &Client{Token: "test", HTTP: &http.Client{Transport: roundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			switch {
			case request.URL.Path == "/repos/huggan360/vision":
				return jsonResponse(`{"owner":{"type":"User"}}`), nil
			case request.URL.Path == "/repos/huggan360/vision/collaborators":
				return jsonResponse(`[]`), nil
			case request.URL.Path == "/repos/huggan360/vision/invitations":
				return jsonResponse(`[{"invitee":{"login":"AlbinFlankLeon"},"permissions":"write"}]`), nil
			default:
				t.Fatalf("unexpected GitHub request: %s %s", request.Method, request.URL)
				return nil, nil
			}
		})}}

	result, err := Sync(context.Background(), client, st, project, "huggan360/vision", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Member(project.ID, member.Username); err != nil {
		t.Fatalf("pending invitee was removed: %v", err)
	}
	if len(result.Warnings) != 2 || !strings.Contains(result.Warnings[1], "pending GitHub invitation") {
		t.Fatalf("pending invitation was not explained: %+v", result.Warnings)
	}
}
