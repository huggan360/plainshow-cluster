package controller

import (
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/mesh"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

// Snapshot is aggregate, read-only state reported by one attached device.
// It contains no files, commands, logs or datasets.
type Snapshot struct {
	NetworkID string              `json:"network_id"`
	SourceID  string              `json:"source_id"`
	Nodes     []store.NetworkNode `json:"nodes"`
	Projects  []store.Project     `json:"projects"`
	Jobs      []store.Job         `json:"jobs"`
	Updated   string              `json:"updated_at"`
}

type EnrollmentRequest struct {
	Token       string `json:"token"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
	Address     string `json:"address"`
}

type EnrollmentResponse struct {
	Network     store.Network       `json:"network"`
	Nodes       []store.NetworkNode `json:"nodes"`
	CollabToken string              `json:"collab_token"`
}

type Server struct {
	config       *Config
	fingerprint  string
	overviewPath string
	layout       *config.Layout
	mu           sync.RWMutex
	writeMu      sync.Mutex
	snapshots    map[string]Snapshot
	sockets      map[string]map[*websocket.Conn]bool
}

func NewServer(config *Config, fingerprint string) *Server {
	return &Server{config: config, fingerprint: fingerprint,
		snapshots: map[string]Snapshot{}, sockets: map[string]map[*websocket.Conn]bool{}}
}

// SetOverviewPath enables crash-safe snapshot persistence inside the root.
func (s *Server) SetOverviewPath(path string) {
	s.overviewPath = path
	raw, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(raw, &s.snapshots)
	}
}

// SetLayout enables durable overview and newly discovered device-key updates.
func (s *Server) SetLayout(layout config.Layout) {
	s.layout = &layout
	s.SetOverviewPath(layout.ControllerOverview())
}

func (s *Server) Handler() http.Handler {
	root := http.NewServeMux()
	root.HandleFunc("GET /api/status", s.status)
	root.HandleFunc("GET /api/overview", s.overview)
	root.HandleFunc("GET /ws", s.serveWS)
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	root.HandleFunc("GET /", s.home)
	authed := http.NewServeMux()
	authed.HandleFunc("POST /mesh/v1/snapshot/{network}", s.acceptSnapshot)
	root.Handle("POST /mesh/v1/{path...}", mesh.Authenticate(authed, s.publicKey))
	return root
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, map[string]any{"id": s.config.ID, "name": s.config.Name,
		"fingerprint": s.fingerprint, "attached_networks": len(s.config.Networks)})
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, `{"error":"controller admin token required"}`, http.StatusUnauthorized)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshots := make([]Snapshot, 0, len(s.snapshots))
	for _, snapshot := range s.snapshots {
		snapshots = append(snapshots, snapshot)
	}
	writeJSON(w, map[string]any{"controller": s.config.Name, "networks": s.config.Networks,
		"snapshots": snapshots})
}

func (s *Server) home(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>Plainshow Controller</title>
<style>body{font:15px system-ui;background:#0b1020;color:#e8edf7;max-width:960px;margin:50px auto;padding:20px}pre{background:#141b2d;padding:18px;border-radius:10px;overflow:auto}</style>
<h1>Plainshow Controller</h1><p>Live collaboration and read-only network overview.</p>
<button id="load">Open overview</button><pre id="out">Enter the admin token printed during init.</pre>
<script>load.onclick=()=>{const token=prompt('Controller admin token');if(!token)return;fetch('/api/overview',{headers:{Authorization:'Bearer '+token}}).then(async r=>{const v=await r.json();if(!r.ok)throw Error(v.error||r.statusText);return v}).then(v=>out.textContent=JSON.stringify(v,null,2)).catch(e=>out.textContent=e.message)}</script>`))
}

func (s *Server) authorized(r *http.Request) bool {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if len(header) <= len(prefix) || header[:len(prefix)] != prefix || s.config.AdminTokenHash == "" {
		return false
	}
	want, got := []byte(s.config.AdminTokenHash), []byte(mesh.TokenHash(header[len(prefix):]))
	return len(want) == len(got) && subtle.ConstantTimeCompare(want, got) == 1
}

func (s *Server) publicKey(networkID, deviceID string) (ed25519.PublicKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, network := range s.config.Networks {
		if network.ID != networkID {
			continue
		}
		for _, node := range network.Nodes {
			if node.ID == deviceID {
				raw, err := base64.RawURLEncoding.DecodeString(node.PublicKey)
				if err == nil && len(raw) == ed25519.PublicKeySize {
					return ed25519.PublicKey(raw), nil
				}
			}
		}
	}
	return nil, errors.New("device is not enrolled with this controller")
}

func (s *Server) acceptSnapshot(w http.ResponseWriter, r *http.Request) {
	var snapshot Snapshot
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&snapshot); err != nil {
		http.Error(w, `{"error":"invalid snapshot"}`, 400)
		return
	}
	if snapshot.NetworkID != r.PathValue("network") || snapshot.SourceID == "" ||
		snapshot.SourceID != r.Header.Get("X-Plainshow-Device") {
		http.Error(w, `{"error":"snapshot belongs to another network"}`, 403)
		return
	}
	snapshot.Updated = time.Now().UTC().Format(time.RFC3339)
	key := snapshot.NetworkID + ":" + snapshot.SourceID
	s.mu.Lock()
	s.snapshots[key] = snapshot
	s.learnNodeKeys(snapshot)
	raw, _ := json.MarshalIndent(s.snapshots, "", "  ")
	path := s.overviewPath
	s.mu.Unlock()
	if path != "" {
		_ = os.WriteFile(path+".tmp", raw, 0o640)
		_ = os.Rename(path+".tmp", path)
	}
	writeJSON(w, map[string]string{"status": "recorded"})
}

// learnNodeKeys is safe because the snapshot itself was signed by an already
// enrolled device in this network. This lets devices added after controller
// attachment report directly without reattaching the controller.
func (s *Server) learnNodeKeys(snapshot Snapshot) {
	changed := false
	for i := range s.config.Networks {
		network := &s.config.Networks[i]
		if network.ID != snapshot.NetworkID {
			continue
		}
		known := map[string]int{}
		for index, node := range network.Nodes {
			known[node.ID] = index
		}
		for _, node := range snapshot.Nodes {
			public, err := base64.RawURLEncoding.DecodeString(node.PublicKey)
			if node.NetworkID != snapshot.NetworkID || node.NodeID == "" ||
				err != nil || len(public) != ed25519.PublicKeySize {
				continue
			}
			next := NodeKey{ID: node.NodeID, PublicKey: node.PublicKey}
			if index, ok := known[node.NodeID]; ok {
				if network.Nodes[index] != next {
					network.Nodes[index] = next
					changed = true
				}
			} else {
				network.Nodes = append(network.Nodes, next)
				known[node.NodeID] = len(network.Nodes) - 1
				changed = true
			}
		}
	}
	if changed && s.layout != nil {
		_ = Save(*s.layout, s.config)
	}
}

var wsUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	networkID := r.URL.Query().Get("network")
	protocol := ""
	for _, offered := range websocket.Subprotocols(r) {
		if len(offered) > len("plainshow.") && offered[:len("plainshow.")] == "plainshow." {
			protocol = offered
			break
		}
	}
	token := ""
	if protocol != "" {
		token = protocol[len("plainshow."):]
	}
	if !s.validToken(networkID, token) {
		http.Error(w, "invalid collaboration token", http.StatusUnauthorized)
		return
	}
	header := http.Header{}
	header.Set("Sec-WebSocket-Protocol", protocol)
	conn, err := wsUpgrader.Upgrade(w, r, header)
	if err != nil {
		return
	}
	s.mu.Lock()
	if s.sockets[networkID] == nil {
		s.sockets[networkID] = map[*websocket.Conn]bool{}
	}
	s.sockets[networkID][conn] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.sockets[networkID], conn)
		s.mu.Unlock()
		_ = conn.Close()
	}()
	conn.SetReadLimit(2 << 20)
	for {
		kind, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		s.broadcast(networkID, conn, kind, raw)
	}
}

func (s *Server) validToken(networkID, token string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, network := range s.config.Networks {
		if network.ID == networkID && network.CollabToken != "" &&
			len(network.CollabToken) == len(token) &&
			subtle.ConstantTimeCompare([]byte(network.CollabToken), []byte(token)) == 1 {
			return true
		}
	}
	return false
}

func (s *Server) broadcast(networkID string, sender *websocket.Conn, kind int, raw []byte) {
	s.mu.RLock()
	peers := make([]*websocket.Conn, 0, len(s.sockets[networkID]))
	for peer := range s.sockets[networkID] {
		if peer != sender {
			peers = append(peers, peer)
		}
	}
	s.mu.RUnlock()
	for _, peer := range peers {
		s.writeMu.Lock()
		_ = peer.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_ = peer.WriteMessage(kind, raw)
		s.writeMu.Unlock()
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) ListenAndServe(ctx context.Context, certificate tls.Certificate, onReady func(string)) error {
	address := net.JoinHostPort(s.config.Listen.Bind, strconv.Itoa(s.config.Listen.Port))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("controller address %s is not available: %w", address, err)
	}
	if onReady != nil {
		onReady("https://" + address)
	}
	server := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	err = server.Serve(tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS13}))
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
