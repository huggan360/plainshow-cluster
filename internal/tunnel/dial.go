package tunnel

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/huggan360/plainshow-cluster/internal/identity"
)

// Dialer keeps an outbound tunnel to a coordinator open.
//
// This is the worker's side: it connects out, which any NAT allows, and then
// serves the coordinator's requests against its own mesh handler. It reconnects
// for as long as the node runs, because the connection is the machine's only
// route back when it has no reachable address of its own.
type Dialer struct {
	Endpoint    string
	Fingerprint string
	NetworkID   string
	Device      *identity.Device
	Handler     http.Handler

	// OnState reports whether the tunnel is currently up, for the interface.
	OnState func(up bool)
}

// Run maintains the tunnel until ctx is cancelled.
func (d *Dialer) Run(ctx context.Context) {
	backoff := time.Second
	for {
		if err := d.once(ctx); err != nil && ctx.Err() == nil {
			// Reconnecting is the normal case, not an incident: a laptop
			// closing its lid produces this. Only say something when the
			// interval has grown enough that it is not just a blip.
			if backoff >= 16*time.Second {
				log.Printf("tunnel to %s: %v", d.Endpoint, err)
			}
		}
		if d.OnState != nil {
			d.OnState(false)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 60*time.Second {
			backoff *= 2
		}
		if ctx.Err() != nil {
			return
		}
		// A connection that lasted is not evidence of trouble; reset so a
		// long-lived tunnel that drops once reconnects immediately.
		if backoff > time.Second && d.lastLasted() {
			backoff = time.Second
		}
	}
}

var connectedFor time.Duration

func (d *Dialer) lastLasted() bool { return connectedFor > 2*time.Minute }

// once opens the tunnel and serves it until it closes.
func (d *Dialer) once(ctx context.Context) error {
	if d.Device == nil {
		return errors.New("this node has no device identity")
	}
	endpoint, err := url.Parse(strings.TrimRight(d.Endpoint, "/") + Path)
	if err != nil {
		return err
	}
	switch endpoint.Scheme {
	case "https":
		endpoint.Scheme = "wss"
	case "http":
		endpoint.Scheme = "ws"
	}

	// The coordinator's certificate is pinned by fingerprint, exactly as the
	// direct mesh client pins it. There is no certificate authority here.
	fingerprint := d.Fingerprint
	dialer := &websocket.Dialer{
		HandshakeTimeout: 20 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS13,
			InsecureSkipVerify: true,
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 {
					return errors.New("coordinator supplied no certificate")
				}
				digest := sha256.Sum256(cs.PeerCertificates[0].Raw)
				got := base64.RawURLEncoding.EncodeToString(digest[:])
				if got != fingerprint {
					return fmt.Errorf("coordinator certificate fingerprint mismatch")
				}
				return nil
			},
		},
	}

	header := http.Header{}
	signHeaders(header, d.NetworkID, d.Device, http.MethodGet, Path)

	conn, resp, err := dialer.DialContext(ctx, endpoint.String(), header)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("coordinator refused the tunnel: %s", resp.Status)
		}
		return err
	}

	t := newTunnel(conn, d.NetworkID, d.Device.ID)
	conn.SetPongHandler(func(string) error { return nil })
	go t.writePump()
	if d.OnState != nil {
		d.OnState(true)
	}

	started := time.Now()
	t.serve(ctx, d.Handler)
	connectedFor = time.Since(started)
	return nil
}
