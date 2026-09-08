package mesh

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Watch opens the same pinned, signed private transport used by mesh requests.
func (c *Client) Watch(ctx context.Context, receive func([]byte) error) error {
	if c.Device == nil {
		return errors.New("missing device identity")
	}
	const path = "/mesh/v1/events"
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	sum := sha256.Sum256(nil)
	signature := c.Device.Sign([]byte("GET\n" + path + "\n" + stamp + "\n" + hex.EncodeToString(sum[:])))
	headers := http.Header{}
	headers.Set("X-Plainshow-Network", c.NetworkID)
	headers.Set("X-Plainshow-Device", c.Device.ID)
	headers.Set("X-Plainshow-Time", stamp)
	headers.Set("X-Plainshow-Signature", base64.RawURLEncoding.EncodeToString(signature))
	transport := c.http.Transport.(*http.Transport)
	dialer := websocket.Dialer{TLSClientConfig: transport.TLSClientConfig, HandshakeTimeout: 5 * time.Second}
	conn, response, err := dialer.DialContext(ctx, strings.Replace(c.Endpoint, "https://", "wss://", 1)+path, headers)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	conn.SetReadLimit(4 << 20)
	for {
		conn.SetReadDeadline(time.Now().Add(12 * time.Second))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if err := receive(raw); err != nil {
			return err
		}
	}
}
