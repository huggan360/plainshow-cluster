package mesh

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/huggan360/plainshow-cluster/internal/identity"
)

func TestWatchPinsSignsReceivesAndCancels(t *testing.T) {
	device, err := identity.LoadOrCreate(filepath.Join(t.TempDir(), "device.key"))
	if err != nil {
		t.Fatal(err)
	}
	connected := make(chan struct{}, 1)
	handler := Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if err := conn.WriteJSON(map[string]string{"network": "one"}); err != nil {
			return
		}
		connected <- struct{}{}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}), func(network, id string) (ed25519.PublicKey, error) {
		if network != "one" || id != device.ID {
			t.Errorf("wrong signed scope: %s %s", network, id)
		}
		return device.Public, nil
	})
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	digest := sha256.Sum256(server.Certificate().Raw)
	fingerprint := base64.RawURLEncoding.EncodeToString(digest[:])
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	received := make(chan struct{}, 1)
	go func() {
		done <- NewClient(server.URL, fingerprint, "one", device).Watch(ctx, func(raw []byte) error { received <- struct{}{}; return nil })
	}()
	select {
	case <-connected:
	case <-ctx.Done():
		t.Fatal("no authenticated connection")
	}
	select {
	case <-received:
	case <-ctx.Done():
		t.Fatal("no message delivered")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not close socket")
	}
	if err := NewClient(server.URL, "wrong-pin", "one", device).Watch(context.Background(), func([]byte) error { return nil }); err == nil {
		t.Fatal("accepted unpinned peer")
	}
}
