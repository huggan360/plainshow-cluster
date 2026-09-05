package accountserver

// Live updates to a device.
//
// This does not make the management plane a relay. It carries one word — "the
// devices you own changed", "an invitation is waiting" — and never the change
// itself, never project bytes, never anything a peer should be sending another
// peer directly. A device that is woken asks the same authenticated question it
// would have asked a minute later anyway.
//
// The heartbeat stays. It carries telemetry this cannot, and it is the floor
// when a socket is down or half-open, so losing this connection costs latency
// and nothing else.

import (
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Topics a device may be woken for.
const (
	TopicDevices     = "devices"
	TopicInvitations = "invitations"
)

// pingEvery keeps the connection alive through the reverse proxy. Cloudflare
// closes an idle WebSocket at around 100 seconds, and a control channel is idle
// almost all the time by design.
const pingEvery = 30 * time.Second

type watcher struct {
	account string
	events  chan string
}

// watchers routes wake-ups to the devices of one account.
type watchers struct {
	mu  sync.RWMutex
	all map[*watcher]struct{}
}

func newWatchers() *watchers { return &watchers{all: map[*watcher]struct{}{}} }

func (w *watchers) add(account string) *watcher {
	// Buffered: a wake-up is idempotent, so a device that is slow to read wants
	// the newest one rather than a queue of identical ones.
	item := &watcher{account: account, events: make(chan string, 8)}
	w.mu.Lock()
	w.all[item] = struct{}{}
	w.mu.Unlock()
	return item
}

func (w *watchers) remove(item *watcher) {
	w.mu.Lock()
	if _, ok := w.all[item]; ok {
		delete(w.all, item)
		close(item.events)
	}
	w.mu.Unlock()
}

// notify wakes every device of one account.
//
// The send never blocks. A device that has stopped reading must not be able to
// wedge the write path of whoever changed something, and dropping a wake-up
// costs at most one heartbeat of latency.
func (w *watchers) notify(account, topic string) {
	if account == "" {
		return
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	for item := range w.all {
		if item.account != account {
			continue
		}
		select {
		case item.events <- topic:
		default:
		}
	}
}

// count reports how many devices are listening for an account.
func (w *watchers) count(account string) int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	total := 0
	for item := range w.all {
		if item.account == account {
			total++
		}
	}
	return total
}

var eventUpgrader = websocket.Upgrader{
	ReadBufferSize:  512,
	WriteBufferSize: 512,
	// Devices connect from their own machines, not from a page on another
	// site, and every connection is authenticated by the account bearer token
	// before it reaches here.
	CheckOrigin: func(*http.Request) bool { return true },
}

// serveEvents wakes a signed-in device when something it owns changes.
func (s *Server) serveEvents(w http.ResponseWriter, r *http.Request) {
	account, ok := s.currentAccount(r)
	if !ok {
		fail(w, http.StatusUnauthorized, "Sign in to continue.")
		return
	}
	conn, err := eventUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	item := s.watchers.add(account.ID)
	defer s.watchers.remove(item)

	// A dead peer is only discovered by writing to it, so the read side exists
	// to notice a close and to answer pongs, not to carry anything.
	go func() {
		for {
			if _, _, readErr := conn.ReadMessage(); readErr != nil {
				conn.Close()
				return
			}
		}
	}()

	ticker := time.NewTicker(pingEvery)
	defer ticker.Stop()
	for {
		select {
		case topic, open := <-item.events:
			if !open {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteJSON(map[string]string{"topic": topic}); err != nil {
				return
			}
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}
