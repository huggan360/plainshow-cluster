package api

import (
	"testing"

	"github.com/huggan360/plainshow-cluster/internal/accountserver"
	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

func TestEnterpriseMemberSyncUpdatesRolesAndRemovesDepartedAccounts(t *testing.T) {
	layout, err := config.NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(layout.Database())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := config.Defaults()
	cfg.Account.ID = "self"
	cfg.Memberships = []config.MembershipConfig{{ID: "network", AccountRole: store.NetworkMember}}
	for _, account := range []store.Account{
		{ID: "self", Username: "old-self"},
		{ID: "departed", Username: "departed"},
	} {
		if err := st.UpsertAccount(account); err != nil {
			t.Fatal(err)
		}
		if err := st.AddNetworkMember("network", account.ID, store.NetworkMember); err != nil {
			t.Fatal(err)
		}
	}
	srv := &Server{cfg: cfg, layout: layout, store: st}
	srv.syncEnterpriseMembers("network", []accountserver.EnterpriseMember{{
		AccountID: "self", Username: "self", DisplayName: "Self", Role: store.NetworkAdmin,
	}})
	member, err := st.NetworkMember("network", "self")
	if err != nil || member.Role != store.NetworkAdmin || cfg.Memberships[0].AccountRole != store.NetworkAdmin {
		t.Fatalf("self role = %+v, config=%q, err=%v", member, cfg.Memberships[0].AccountRole, err)
	}
	if _, err := st.NetworkMember("network", "departed"); err == nil {
		t.Fatal("departed central member remained in local store")
	}
}
