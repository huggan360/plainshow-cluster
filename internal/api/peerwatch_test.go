package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/events"
	"github.com/huggan360/plainshow-cluster/internal/identity"
	"github.com/huggan360/plainshow-cluster/internal/mesh"
	"github.com/huggan360/plainshow-cluster/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestPeerSocketPushesRayAndClosesOnShutdown(t *testing.T) {
	s, _ := newTestServer(t)
	s.cfg.Memberships = []config.MembershipConfig{{ID: "net", Name: "Lab", Enabled: true}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.peerContext = ctx
	device, err := identity.LoadOrCreate(filepath.Join(t.TempDir(), "client.key"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.UpsertNetwork(store.Network{ID: "net", Name: "Lab", OwnerAccountID: s.cfg.Node.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.UpsertNetworkNode(store.NetworkNode{NetworkID: "net", NodeID: device.ID, Name: "peer", PublicKey: base64.RawURLEncoding.EncodeToString(device.Public)}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(s.MeshHandler())
	defer server.Close()
	sum := sha256.Sum256(server.Certificate().Raw)
	client := mesh.NewClient(server.URL, base64.RawURLEncoding.EncodeToString(sum[:]), "net", device)
	received := make(chan peerExchange, 8)
	done := make(chan error, 1)
	clientCtx, clientCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer clientCancel()
	go func() {
		done <- client.Watch(clientCtx, func(raw []byte) error {
			var exchange peerExchange
			if err := json.Unmarshal(raw, &exchange); err != nil {
				return err
			}
			received <- exchange
			return nil
		})
	}()
	select {
	case <-received:
	case <-clientCtx.Done():
		t.Fatal("initial snapshot missing")
	}
	if err := s.announceRayHead("net", "100.64.0.1:6379"); err != nil {
		t.Fatal(err)
	}
	s.hub.Publish("ray.changed", nil)
	select {
	case next := <-received:
		if next.Ray.Head != "100.64.0.1:6379" {
			t.Fatal(next)
		}
	case <-time.After(time.Second):
		t.Fatal("change was not pushed")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown left socket open")
	}
}

func TestAnyMemberCanPushRayOffAcrossTheNetwork(t *testing.T) {
	s, _ := newTestServer(t)
	s.cfg.Memberships = []config.MembershipConfig{{ID: "net", Name: "Lab", Enabled: true}}
	if err := s.announceRayHead("net", "100.64.0.2:6379"); err != nil {
		t.Fatal(err)
	}
	sub := s.hub.Subscribe()
	defer sub.Close()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/ray/stop?network_id=net", nil)
	s.stopRay(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("stop returned %d: %s", recorder.Code, recorder.Body.String())
	}
	if announcement := s.rayAnnouncement("net"); announcement.Head != "" {
		t.Fatalf("Ray head survived network stop: %+v", announcement)
	}
	select {
	case event := <-sub.C:
		if event.Topic != "ray.changed" {
			t.Fatalf("event = %q", event.Topic)
		}
		data, _ := event.Data.(map[string]any)
		if data["network_id"] != "net" || data["running"] != false {
			t.Fatalf("Ray socket event = %+v", data)
		}
	case <-time.After(time.Second):
		t.Fatal("Ray off was not sent to browser sockets")
	}
}

func TestDevicePresenceUsesEverySharedNetwork(t *testing.T) {
	s := &Server{hub: events.NewHub()}
	s.markPeer("one", "peer", true)
	s.markPeer("two", "peer", false)
	if online, known := s.observedDevice("peer"); !known || !online {
		t.Fatal("one failed network hid a connected device")
	}
	s.markPeer("one", "peer", false)
	if online, _ := s.observedDevice("peer"); online {
		t.Fatal("disconnect left device online")
	}
	s.markPeer("two", "peer", true)
	s.presence["two/peer"] = peerPresence{Online: true, Seen: time.Now().Add(-13 * time.Second)}
	if online, _ := s.observedDevice("peer"); online {
		t.Fatal("expired heartbeat remained online")
	}
}
