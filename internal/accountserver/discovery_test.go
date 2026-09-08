package accountserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestCheckInWakesDiscoveryWithoutHeartbeatFeedback(t *testing.T) {
	store, owner, peer := twoAccounts(t)
	if err := store.GrantNetworkMember(owner.ID, "net-lab", testKey, peer.ID, "member"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession("owner-token", owner.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	s := NewServer(&Config{}, store, fstest.MapFS{"index.html": {Data: []byte("admin")}})
	local, shared, unrelated := s.watchers.add(owner.ID), s.watchers.add(peer.ID), s.watchers.add("stranger")
	defer s.watchers.remove(local)
	defer s.watchers.remove(shared)
	defer s.watchers.remove(unrelated)
	input := NodeCheckIn{ID: "n1", Name: "Desktop", Address: "https://100.64.0.1:9443",
		Networks: []NetworkRef{{ID: "net-lab", Name: "Research lab"}}}
	checkIn := func(changed bool) {
		t.Helper()
		raw, _ := json.Marshal(input)
		r := httptest.NewRequest(http.MethodPost, "/api/nodes/check-in", strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer owner-token")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("check-in: %d %s", w.Code, w.Body)
		}
		for watcher, topic := range map[*watcher]string{local: TopicDevices, shared: TopicNetworks, unrelated: ""} {
			want := topic
			if !changed {
				want = ""
			}
			got := ""
			select {
			case got = <-watcher.events:
			default:
			}
			if got != want || len(watcher.events) != 0 {
				t.Fatalf("account %s: wake=%q want=%q queued=%d", watcher.account, got, want, len(watcher.events))
			}
		}
	}
	checkIn(true) // New device is discovered immediately, even by another owner.
	checkIn(false)
	input.RunningJobs = 3
	checkIn(false) // Telemetry is not a directory change.
	input.Address = "https://100.64.0.2:9443"
	checkIn(true)
	if _, err := store.db.Exec(`UPDATE node SET last_seen=? WHERE id=?`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), input.ID); err != nil {
		t.Fatal(err)
	}
	checkIn(true) // A returning device also wakes discovery.
	input.Networks = nil
	checkIn(true) // Former peers learn that it left.
	checkIn(false)
}
