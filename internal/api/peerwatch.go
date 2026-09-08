package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/huggan360/plainshow-cluster/internal/mesh"
	"github.com/huggan360/plainshow-cluster/internal/store"
)

// peerPresence is an observation of a direct connection, never a gossiped vote.
type peerPresence struct {
	Online bool
	Seen   time.Time
}

func (s *Server) markPeer(networkID, id string, online bool) {
	s.presenceMu.Lock()
	if s.presence == nil {
		s.presence = make(map[string]peerPresence)
	}
	key := networkID + "/" + id
	old, exists := s.presence[key]
	s.presence[key] = peerPresence{online, time.Now()}
	s.presenceMu.Unlock()
	if !exists || old.Online != online {
		s.hub.Publish("devices.changed", map[string]any{"node_id": id, "online": online})
		s.hub.Publish("peers.changed", map[string]any{"node_id": id, "online": online})
	}
}

func (s *Server) liveNodes(networkID string) ([]store.NetworkNode, error) {
	nodes, err := s.store.NetworkNodes(networkID)
	s.presenceMu.RLock()
	defer s.presenceMu.RUnlock()
	for i := range nodes {
		if nodes[i].IsSelf {
			value := true
			nodes[i].Online = &value
		} else if observed, ok := s.presence[networkID+"/"+nodes[i].NodeID]; ok {
			value := observed.Online && time.Since(observed.Seen) < 12*time.Second
			nodes[i].Online = &value
		}
	}
	return nodes, err
}

// StartPeerWatch keeps a stream per shared network/device, independent of browsers.
// Heartbeat gossip remains the fallback for older peers and missed events.
func (s *Server) StartPeerWatch(ctx context.Context) {
	s.peerContext = ctx
	go func() {
		running := map[string]context.CancelFunc{}
		var wg sync.WaitGroup
		defer func() {
			for _, cancel := range running {
				cancel()
			}
			wg.Wait()
		}()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			nodes := []store.NetworkNode{}
			networks, err := s.store.Networks(s.cfg.AccountID())
			if err != nil {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					continue
				}
			}
			for _, network := range networks {
				members, err := s.store.NetworkNodes(network.ID)
				if err == nil {
					nodes = append(nodes, members...)
				}
			}
			wanted := map[string]bool{}
			for _, node := range nodes {
				if node.IsSelf || node.Address == "" || node.Fingerprint == "" {
					continue
				}
				key := node.NodeID + "/" + node.NetworkID + "/" + node.Address + "/" + node.Fingerprint
				wanted[key] = true
				if _, ok := running[key]; ok {
					continue
				}
				child, cancel := context.WithCancel(ctx)
				running[key] = cancel
				wg.Add(1)
				go func(node store.NetworkNode) {
					defer wg.Done()
					lastSnapshot := ""
					for child.Err() == nil {
						client := mesh.NewClient(node.Address, node.Fingerprint, node.NetworkID, s.device)
						established := false
						_ = client.Watch(child, func(raw []byte) error {
							var exchange peerExchange
							if err := json.Unmarshal(raw, &exchange); err != nil {
								return err
							}
							established = true
							s.markPeer(node.NetworkID, node.NodeID, true)
							s.mergePeerNodes(node.NetworkID, exchange.Nodes)
							s.mergeRayAnnouncement(node.NetworkID, exchange.Ray)
							// A heartbeat changes presence, not the visible hardware inventory.
							// Avoid remounting a page simply because a timestamp ticked.
							for i := range exchange.Nodes {
								exchange.Nodes[i].LastSeen = ""
							}
							snapshot, _ := json.Marshal(exchange)
							if string(snapshot) != lastSnapshot {
								lastSnapshot = string(snapshot)
								s.hub.Publish("peers.changed", map[string]string{"network_id": node.NetworkID})
							}
							return nil
						})
						if established {
							s.markPeer(node.NetworkID, node.NodeID, false)
						}
						select {
						case <-child.Done():
							return
						case <-time.After(2 * time.Second):
						}
					}
				}(node)
			}
			for key, cancel := range running {
				if !wanted[key] {
					cancel()
					delete(running, key)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *Server) peerEvents(w http.ResponseWriter, r *http.Request) {
	networkID := r.Header.Get("X-Plainshow-Network")
	if !hasMembership(s.cfg, networkID) {
		fail(w, 403, "Network membership required.")
		return
	}
	// Browsers never authenticate to this transport; the signed mesh middleware does.
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	if s.peerContext != nil {
		stop := context.AfterFunc(s.peerContext, func() { conn.Close() })
		defer stop()
	}
	sub := s.hub.Subscribe()
	defer sub.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn.SetReadLimit(1024)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	send := func() error {
		if !hasMembership(s.cfg, networkID) {
			return context.Canceled
		}
		nodes, err := s.store.NetworkNodes(networkID)
		if err != nil {
			return err
		}
		for i := range nodes {
			if nodes[i].IsSelf {
				nodes[i].LastSeen = store.Now()
			}
		}
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return conn.WriteJSON(peerExchange{Nodes: nodes, Ray: s.rayAnnouncement(networkID)})
	}
	if send() != nil {
		return
	}
	for {
		select {
		case <-done:
			return
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if send() != nil {
				return
			}
		case ev := <-sub.C:
			if ev.Topic == "settings.changed" || ev.Topic == "network.policy" || ev.Topic == "network.active" {
				s.refreshLocalNodeSnapshot(r.Context(), networkID)
			}
			if ev.Topic == "ray.changed" || ev.Topic == "networks.changed" || ev.Topic == "network.active" || ev.Topic == "settings.changed" || ev.Topic == "network.policy" {
				if send() != nil {
					return
				}
			}
		}
	}
}

// observedDevice combines independent network links for the account Devices page.
func (s *Server) observedDevice(id string) (online, known bool) {
	s.presenceMu.RLock()
	defer s.presenceMu.RUnlock()
	for key, observed := range s.presence {
		if strings.HasSuffix(key, "/"+id) {
			known = true
			online = online || observed.Online && time.Since(observed.Seen) < 12*time.Second
		}
	}
	return
}
