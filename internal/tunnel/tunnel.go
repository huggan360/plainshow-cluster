// Package tunnel carries mesh requests to machines that cannot be dialled.
//
// The mesh normally works by one node connecting to another's peer port. That
// needs the target to be reachable, which two ordinary home networks are not:
// behind NAT — and certainly behind carrier-grade NAT — a desktop has no
// address anybody outside can open a connection to, and asking people to
// forward ports is the thing this product exists to avoid.
//
// So the direction is inverted. A worker dials *out* to its coordinator, which
// every NAT permits, and holds that connection open. The coordinator then sends
// requests back down it and reads the replies. Outbound is the only direction
// that reliably works, so it is the only direction used.
//
// What travels over the tunnel is exactly what would have travelled over the
// peer port: the same paths, the same JSON, served by the same handler. The
// tunnel is a transport swap, not a second protocol — one implementation of
// "run this job", not two that drift apart.
package tunnel

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/huggan360/plainshow-cluster/internal/identity"
	"github.com/huggan360/plainshow-cluster/internal/mesh"
)

// Path is where a coordinator accepts tunnels. It sits behind the same
// signature check as every other mesh route.
const Path = "/mesh/v1/tunnel"

// callTimeout bounds one request sent down a tunnel. It is generous because
// preparing a job can mean materialising a project.
const callTimeout = 2 * time.Minute

// pingEvery keeps the connection alive through NATs that drop idle flows, and
// is how a half-open connection is noticed at all.
const pingEvery = 25 * time.Second

// ErrNoTunnel is returned when a device has no live tunnel.
var ErrNoTunnel = errors.New("no open tunnel to that machine")

// frame is one message in either direction.
type frame struct {
	Kind   string          `json:"kind"` // "request" or "response"
	ID     string          `json:"id"`
	Method string          `json:"method,omitempty"`
	Path   string          `json:"path,omitempty"`
	Status int             `json:"status,omitempty"`
	Body   json.RawMessage `json:"body,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Tunnel is one live connection to a machine that dialled in.
type Tunnel struct {
	DeviceID  string
	NetworkID string

	conn *websocket.Conn

	// Gorilla permits one writer at a time, so every write goes through this
	// channel and a single pump goroutine owns the connection.
	writes chan frame

	mu      sync.Mutex
	pending map[string]chan frame
	closed  bool
	done    chan struct{}
}

func newTunnel(conn *websocket.Conn, networkID, deviceID string) *Tunnel {
	return &Tunnel{
		DeviceID: deviceID, NetworkID: networkID, conn: conn,
		writes:  make(chan frame, 32),
		pending: make(map[string]chan frame),
		done:    make(chan struct{}),
	}
}

// Close shuts the tunnel and fails anything waiting on it.
func (t *Tunnel) Close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	waiting := t.pending
	t.pending = map[string]chan frame{}
	t.mu.Unlock()

	close(t.done)
	_ = t.conn.Close()
	// Anything mid-flight fails now rather than waiting out its timeout.
	for _, ch := range waiting {
		select {
		case ch <- frame{Error: "the tunnel closed"}:
		default:
		}
	}
}

// writePump owns the connection's write side.
func (t *Tunnel) writePump() {
	ticker := time.NewTicker(pingEvery)
	defer ticker.Stop()
	for {
		select {
		case <-t.done:
			return
		case f := <-t.writes:
			_ = t.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if err := t.conn.WriteJSON(f); err != nil {
				t.Close()
				return
			}
		case <-ticker.C:
			_ = t.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := t.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				t.Close()
				return
			}
		}
	}
}

// send queues a frame, giving up if the tunnel dies first.
func (t *Tunnel) send(f frame) error {
	select {
	case <-t.done:
		return ErrNoTunnel
	case t.writes <- f:
		return nil
	case <-time.After(30 * time.Second):
		return errors.New("the tunnel is not accepting writes")
	}
}

// JSON performs one mesh call over the tunnel.
//
// The signature matches mesh.Client.JSON so the two are interchangeable at the
// call site. Requests are already authenticated by the tunnel itself — the
// connection was signature-checked when it was accepted and belongs to exactly
// one enrolled device — so the per-request signature is redundant here.
func (t *Tunnel) JSON(method, path string, input, output any, _ bool) error {
	body := json.RawMessage(nil)
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = encoded
	}

	id, err := newID()
	if err != nil {
		return err
	}
	reply := make(chan frame, 1)

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return ErrNoTunnel
	}
	t.pending[id] = reply
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
	}()

	if err := t.send(frame{Kind: "request", ID: id, Method: method, Path: path, Body: body}); err != nil {
		return err
	}

	select {
	case <-time.After(callTimeout):
		return fmt.Errorf("the machine did not answer %s %s in time", method, path)
	case <-t.done:
		return ErrNoTunnel
	case answer := <-reply:
		if answer.Error != "" {
			return errors.New(answer.Error)
		}
		if answer.Status < 200 || answer.Status >= 300 {
			var failure struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(answer.Body, &failure)
			if failure.Error == "" {
				failure.Error = strings.TrimSpace(string(answer.Body))
			}
			return fmt.Errorf("peer returned %d: %s", answer.Status, failure.Error)
		}
		if output != nil && len(answer.Body) > 0 {
			return json.Unmarshal(answer.Body, output)
		}
		return nil
	}
}

// deliver routes a response back to whoever is waiting for it.
func (t *Tunnel) deliver(f frame) {
	t.mu.Lock()
	ch, ok := t.pending[f.ID]
	t.mu.Unlock()
	if !ok {
		return // a reply to a call that already gave up
	}
	select {
	case ch <- f:
	default:
	}
}

func newID() (string, error) {
	raw := make([]byte, 12)
	if _, err := io.ReadFull(randReader, raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// signHeaders stamps a request the way mesh.Client does, so a tunnel dial is
// checked by exactly the same middleware as every other mesh request.
func signHeaders(header http.Header, networkID string, device *identity.Device, method, path string) {
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	sum := sha256.Sum256(nil)
	message := method + "\n" + path + "\n" + stamp + "\n" + hex.EncodeToString(sum[:])
	header.Set("X-Plainshow-Network", networkID)
	header.Set("X-Plainshow-Device", device.ID)
	header.Set("X-Plainshow-Time", stamp)
	header.Set("X-Plainshow-Signature",
		base64.RawURLEncoding.EncodeToString(device.Sign([]byte(message))))
}

// serve handles frames arriving on a tunnel: requests get dispatched to the
// local handler, responses get routed to their caller.
func (t *Tunnel) serve(ctx context.Context, handler http.Handler) {
	for {
		var f frame
		_ = t.conn.SetReadDeadline(time.Now().Add(pingEvery * 3))
		if err := t.conn.ReadJSON(&f); err != nil {
			t.Close()
			return
		}
		switch f.Kind {
		case "response":
			t.deliver(f)
		case "request":
			if handler == nil {
				_ = t.send(frame{Kind: "response", ID: f.ID,
					Error: "this machine does not accept tunnelled requests"})
				continue
			}
			// One goroutine per request: a slow job must not stop the tunnel
			// answering anything else, including its own health checks.
			go t.dispatch(ctx, handler, f)
		}
	}
}

// dispatch runs one tunnelled request against the local mesh handler.
func (t *Tunnel) dispatch(ctx context.Context, handler http.Handler, f frame) {
	req, err := http.NewRequestWithContext(ctx, f.Method, f.Path, strings.NewReader(string(f.Body)))
	if err != nil {
		_ = t.send(frame{Kind: "response", ID: f.ID, Error: err.Error()})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// The tunnel already proved which device this is, so the handler is reached
	// with the marker its middleware looks for rather than a second signature.
	req.Header.Set(TrustedHeader, t.DeviceID)
	req.Header.Set("X-Plainshow-Network", t.NetworkID)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	_ = t.send(frame{
		Kind: "response", ID: f.ID,
		Status: recorder.Code, Body: json.RawMessage(recorder.Body.Bytes()),
	})
}

// TrustedHeader marks a request that arrived over an already-authenticated
// tunnel. It is set by the tunnel itself and stripped from anything arriving
// over the network, so it cannot be presented by a peer.
const TrustedHeader = mesh.TrustedTunnelHeader
