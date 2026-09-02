// Package events fans node activity out to connected browsers.
//
// One multiplexed stream carries everything: job state, log lines, file tree
// changes and machine telemetry. One socket means one reconnect path and one
// place where a slow client is dealt with, rather than one per feature.
package events

import (
	"encoding/json"
	"sync"
	"time"
)

// Event is a single message on the stream.
type Event struct {
	Topic string `json:"topic"`
	Data  any    `json:"data"`
	At    string `json:"at"`
}

// Subscriber receives events until it is closed.
type Subscriber struct {
	C    chan Event
	hub  *Hub
	once sync.Once
}

// Close unsubscribes. It is safe to call more than once.
func (s *Subscriber) Close() {
	s.once.Do(func() {
		s.hub.remove(s)
		close(s.C)
	})
}

// Hub broadcasts events to every subscriber.
type Hub struct {
	mu   sync.RWMutex
	subs map[*Subscriber]struct{}
}

// NewHub returns an empty hub.
func NewHub() *Hub { return &Hub{subs: make(map[*Subscriber]struct{})} }

// Subscribe registers a new listener with a buffered channel.
func (h *Hub) Subscribe() *Subscriber {
	s := &Subscriber{C: make(chan Event, 256), hub: h}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

func (h *Hub) remove(s *Subscriber) {
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
}

// Publish delivers an event to every subscriber.
//
// Delivery is non-blocking: a subscriber whose buffer is full misses the
// message rather than stalling the job that produced it. Log output must never
// be able to block a running process because a browser tab stopped reading.
func (h *Hub) Publish(topic string, data any) {
	ev := Event{Topic: topic, Data: data, At: time.Now().UTC().Format(time.RFC3339Nano)}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for s := range h.subs {
		select {
		case s.C <- ev:
		default:
		}
	}
}

// Count reports how many subscribers are attached.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}

// Encode renders an event as JSON for the wire.
func (e Event) Encode() ([]byte, error) { return json.Marshal(e) }
