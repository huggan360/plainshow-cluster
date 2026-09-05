package accountclient

// Watching the account service for changes to this device.
//
// The heartbeat is not replaced. It carries telemetry this cannot and it is the
// floor when the socket is down or half-open, so a lost connection costs
// latency and nothing else. What arrives here is a single word — the devices
// you own changed, an invitation is waiting — never the change itself, so the
// device answers by asking the same authenticated question it would have asked
// on its next heartbeat.

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// WatchEvent is one wake-up.
type WatchEvent struct {
	Topic string `json:"topic"`
}

// Watch streams wake-ups until the connection drops or ctx is cancelled. It
// always returns an error, because returning is what "it stopped" means.
func (c *Client) Watch(ctx context.Context, token string, onEvent func(WatchEvent)) error {
	endpoint, err := websocketURL(c.endpoint + "/api/events")
	if err != nil {
		return err
	}
	dialer := websocket.Dialer{HandshakeTimeout: 20 * time.Second}
	conn, response, err := dialer.DialContext(ctx, endpoint,
		http.Header{"Authorization": []string{"Bearer " + token}})
	if err != nil {
		if response != nil {
			return errors.New("account service refused the live connection: " + response.Status)
		}
		return err
	}
	defer conn.Close()

	// The server pings every 30 seconds. Expecting one inside 90 turns a
	// half-open connection — the failure this whole path is most likely to hit,
	// through a proxy or a sleeping laptop — into a reconnect rather than a
	// device that thinks it is live and hears nothing ever again.
	const silenceLimit = 90 * time.Second
	_ = conn.SetReadDeadline(time.Now().Add(silenceLimit))
	conn.SetPingHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(silenceLimit))
		return conn.WriteControl(websocket.PongMessage, nil, time.Now().Add(10*time.Second))
	})

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		var event WatchEvent
		if err := conn.ReadJSON(&event); err != nil {
			return err
		}
		_ = conn.SetReadDeadline(time.Now().Add(silenceLimit))
		if event.Topic != "" && onEvent != nil {
			onEvent(event)
		}
	}
}

// websocketURL turns the account service's address into its socket scheme.
func websocketURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	default:
		return "", errors.New("account server address must be http or https")
	}
	return parsed.String(), nil
}
