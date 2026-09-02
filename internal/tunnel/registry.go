package tunnel

import (
	"crypto/rand"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var randReader = rand.Reader

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// The tunnel is authenticated by an Ed25519 signature on the upgrade
	// request, not by where the browser thinks it came from.
	CheckOrigin: func(*http.Request) bool { return true },
}

// Registry holds the tunnels currently open to this coordinator.
type Registry struct {
	mu      sync.RWMutex
	tunnels map[string]*Tunnel // keyed by network and device
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{tunnels: map[string]*Tunnel{}} }

func key(networkID, deviceID string) string { return networkID + "/" + deviceID }

// Get returns the live tunnel to a device, if there is one.
func (r *Registry) Get(networkID, deviceID string) (*Tunnel, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tunnels[key(networkID, deviceID)]
	return t, ok
}

// Devices lists the devices currently reachable through a tunnel.
func (r *Registry) Devices() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.tunnels))
	for _, t := range r.tunnels {
		out = append(out, t.DeviceID)
	}
	return out
}

func (r *Registry) add(t *Tunnel) {
	r.mu.Lock()
	// A machine that reconnected before we noticed the old connection had died
	// would otherwise leave a tunnel nothing can reach behind.
	if existing, ok := r.tunnels[key(t.NetworkID, t.DeviceID)]; ok {
		go existing.Close()
	}
	r.tunnels[key(t.NetworkID, t.DeviceID)] = t
	r.mu.Unlock()
}

func (r *Registry) remove(t *Tunnel) {
	r.mu.Lock()
	if current, ok := r.tunnels[key(t.NetworkID, t.DeviceID)]; ok && current == t {
		delete(r.tunnels, key(t.NetworkID, t.DeviceID))
	}
	r.mu.Unlock()
}

// Accept upgrades an authenticated request into a tunnel and serves it until it
// closes. onChange is called when a machine arrives or leaves, so the interface
// can show reachability without polling.
func (r *Registry) Accept(w http.ResponseWriter, req *http.Request, onChange func()) {
	networkID := req.Header.Get("X-Plainshow-Network")
	deviceID := req.Header.Get("X-Plainshow-Device")
	if networkID == "" || deviceID == "" {
		http.Error(w, `{"error":"tunnel request is not identified"}`, http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	t := newTunnel(conn, networkID, deviceID)
	conn.SetPongHandler(func(string) error { return nil })

	r.add(t)
	if onChange != nil {
		onChange()
	}
	go t.writePump()

	// This node is the coordinator here, so it sends requests and reads
	// replies; it does not serve requests back down the tunnel.
	t.serve(req.Context(), nil)

	r.remove(t)
	if onChange != nil {
		onChange()
	}
}
