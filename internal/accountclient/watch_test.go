package accountclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWatchReceivesWakeUpsAfterReconnect(t *testing.T) {
	var connections atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/events" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer node-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		n := connections.Add(1)
		_ = conn.WriteJSON(WatchEvent{Topic: "wake-" + string(rune('0'+n))})
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		var topic string
		err := client.Watch(ctx, "node-token", func(event WatchEvent) { topic = event.Topic })
		cancel()
		var closed *WatchClosedError
		if !errors.As(err, &closed) {
			t.Fatalf("watch %d returned %T %v, want WatchClosedError", attempt, err, err)
		}
		want := "wake-" + string(rune('0'+attempt))
		if topic != want {
			t.Fatalf("watch %d received %q, want %q", attempt, topic, want)
		}
	}
	if got := connections.Load(); got != 2 {
		t.Fatalf("connections = %d, want 2", got)
	}
}
