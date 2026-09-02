package tunnel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/huggan360/plainshow-cluster/internal/identity"
	"github.com/huggan360/plainshow-cluster/internal/mesh"
)

// pair stands a coordinator and a worker up over a real WebSocket, so these
// tests exercise the wire rather than a stubbed interface.
func pair(t *testing.T, workerHandler http.Handler) (*Registry, func()) {
	t.Helper()

	device, err := identity.LoadOrCreate(filepath.Join(t.TempDir(), "device.key"))
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registry.Accept(w, r, nil)
	}))

	header := http.Header{}
	signHeaders(header, "net-1", device, http.MethodGet, Path)
	header.Set("X-Plainshow-Network", "net-1")
	header.Set("X-Plainshow-Device", device.ID)

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	worker := newTunnel(conn, "net-1", device.ID)
	go worker.writePump()
	go worker.serve(context.Background(), workerHandler)

	// Wait for the coordinator to register it.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := registry.Get("net-1", device.ID); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := registry.Get("net-1", device.ID); !ok {
		t.Fatal("the tunnel never registered")
	}
	return registry, func() { worker.Close(); server.Close() }
}

func deviceOf(t *testing.T, r *Registry) string {
	t.Helper()
	devices := r.Devices()
	if len(devices) != 1 {
		t.Fatalf("expected one tunnel, got %d", len(devices))
	}
	return devices[0]
}

// TestRequestCrossesTheTunnel is the whole point: the coordinator calls a path
// and the worker's own handler answers it, with no inbound connection to the
// worker at any stage.
func TestRequestCrossesTheTunnel(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("POST /mesh/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"ran": body["command"]})
	})

	registry, stop := pair(t, handler)
	defer stop()

	open, ok := registry.Get("net-1", deviceOf(t, registry))
	if !ok {
		t.Fatal("no tunnel")
	}
	var out struct {
		Ran string `json:"ran"`
	}
	if err := open.JSON("POST", "/mesh/v1/jobs",
		map[string]string{"command": "python train.py"}, &out, true); err != nil {
		t.Fatalf("call over the tunnel: %v", err)
	}
	if out.Ran != "python train.py" {
		t.Errorf("worker answered %q", out.Ran)
	}
}

// TestErrorsCrossIntact: a failure on the worker has to reach the coordinator
// as that failure, not as a transport error.
func TestErrorsCrossIntact(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("POST /mesh/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "this machine does not allow terminal access",
		})
	})
	registry, stop := pair(t, handler)
	defer stop()

	open, _ := registry.Get("net-1", deviceOf(t, registry))
	err := open.JSON("POST", "/mesh/v1/jobs", map[string]string{}, nil, true)
	if err == nil {
		t.Fatal("a refusal came back as success")
	}
	if !strings.Contains(err.Error(), "does not allow terminal access") {
		t.Errorf("the worker's reason was lost: %v", err)
	}
}

// TestConcurrentCallsDoNotCross: replies are matched to their own request.
func TestConcurrentCallsDoNotCross(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("POST /mesh/v1/echo", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		// Stagger the replies so they cannot come back in request order.
		if body["n"] == "0" {
			time.Sleep(120 * time.Millisecond)
		}
		_ = json.NewEncoder(w).Encode(body)
	})
	registry, stop := pair(t, handler)
	defer stop()
	open, _ := registry.Get("net-1", deviceOf(t, registry))

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			var out map[string]string
			want := map[string]string{"n": string(rune('0' + n))}
			if err := open.JSON("POST", "/mesh/v1/echo", want, &out, true); err != nil {
				errs[n] = err
				return
			}
			if out["n"] != want["n"] {
				errs[n] = errNotMine(want["n"], out["n"])
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d: %v", i, err)
		}
	}
}

type mismatch struct{ want, got string }

func (m mismatch) Error() string        { return "wanted reply " + m.want + " but got " + m.got }
func errNotMine(want, got string) error { return mismatch{want, got} }

// TestClosedTunnelFailsCallsAtOnce: a machine that went away must not leave
// callers waiting out the full timeout.
func TestClosedTunnelFailsCallsAtOnce(t *testing.T) {
	registry, stop := pair(t, http.NewServeMux())
	open, _ := registry.Get("net-1", deviceOf(t, registry))
	stop()

	done := make(chan error, 1)
	go func() { done <- open.JSON("GET", "/mesh/v1/ping", nil, nil, true) }()

	select {
	case err := <-done:
		if err == nil {
			t.Error("a call on a closed tunnel reported success")
		}
	case <-time.After(10 * time.Second):
		t.Error("a call on a closed tunnel hung instead of failing")
	}
}

// TestTunnelMarkerIsStrippedFromNetworkRequests is the security property the
// whole design rests on. The marker says "this request already proved which
// device it is". If a peer could set it on a request to the open port, anyone
// able to reach that port could impersonate an enrolled machine.
func TestTunnelMarkerIsStrippedFromNetworkRequests(t *testing.T) {
	var saw string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = r.Header.Get(mesh.TrustedTunnelHeader)
		w.WriteHeader(http.StatusOK)
	})
	guarded := mesh.StripTunnelMarker(inner)

	req := httptest.NewRequest(http.MethodPost, "/mesh/v1/jobs", nil)
	req.Header.Set(mesh.TrustedTunnelHeader, "some-other-device")
	guarded.ServeHTTP(httptest.NewRecorder(), req)

	if saw != "" {
		t.Errorf("a forged tunnel marker survived: %q", saw)
	}
}
