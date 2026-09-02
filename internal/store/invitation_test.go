package store

import (
	"path/filepath"
	"testing"
	"time"
)

// TestHasPendingInvitation exercises the query itself. SQL is not checked by
// the compiler, so a column that does not exist builds happily and fails only
// when the code runs — which for this query is at node startup.
func TestHasPendingInvitation(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "invite.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	pending, err := st.HasPendingInvitation("net-1")
	if err != nil {
		t.Fatalf("query failed on an empty database: %v", err)
	}
	if pending {
		t.Error("an empty database reported a pending invitation")
	}

	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)

	cases := []struct {
		name    string
		invite  Invitation
		consume bool // spend it, since CreateInvitation always starts at zero uses
		pending bool
	}{
		{"unused and current", Invitation{ID: "a", NetworkID: "net-1", Role: NetworkMember,
			TokenHash: "h-a", Expires: future, MaxUses: 1}, false, true},
		{"already used", Invitation{ID: "b", NetworkID: "net-2", Role: NetworkMember,
			TokenHash: "h-b", Expires: future, MaxUses: 1}, true, false},
		{"expired", Invitation{ID: "c", NetworkID: "net-3", Role: NetworkMember,
			TokenHash: "h-c", Expires: past, MaxUses: 1}, false, false},
		{"multi-use with room left", Invitation{ID: "d", NetworkID: "net-4", Role: NetworkMember,
			TokenHash: "h-d", Expires: future, MaxUses: 5}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := st.CreateInvitation(tc.invite); err != nil {
				t.Fatalf("CreateInvitation: %v", err)
			}
			if tc.consume {
				if _, err := st.ConsumeInvitation(tc.invite.NetworkID, tc.invite.TokenHash); err != nil {
					t.Fatalf("ConsumeInvitation: %v", err)
				}
			}
			got, err := st.HasPendingInvitation(tc.invite.NetworkID)
			if err != nil {
				t.Fatalf("HasPendingInvitation: %v", err)
			}
			if got != tc.pending {
				t.Errorf("HasPendingInvitation(%s) = %v, want %v",
					tc.invite.NetworkID, got, tc.pending)
			}
		})
	}
}
