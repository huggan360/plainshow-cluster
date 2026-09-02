package github

import (
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/store"
)

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
