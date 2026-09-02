package mesh

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/identity"
)

const maxClockSkew = 5 * time.Minute

type Client struct {
	Endpoint    string
	Fingerprint string
	NetworkID   string
	Device      *identity.Device
	http        *http.Client
}

func NewClient(endpoint, fingerprint, networkID string, device *identity.Device) *Client {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true}
	tlsConfig.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("mesh peer supplied no certificate")
		}
		digest := sha256.Sum256(cs.PeerCertificates[0].Raw)
		got := base64.RawURLEncoding.EncodeToString(digest[:])
		if got != fingerprint {
			return fmt.Errorf("mesh certificate fingerprint mismatch")
		}
		return nil
	}
	return &Client{Endpoint: strings.TrimRight(endpoint, "/"), Fingerprint: fingerprint,
		NetworkID: networkID, Device: device, http: &http.Client{Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		}, Timeout: 2 * time.Minute}}
}

func (c *Client) JSON(method, path string, input, output any, authenticate bool) error {
	var body []byte
	var err error
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequest(method, c.Endpoint+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if authenticate {
		if c.Device == nil {
			return errors.New("mesh client has no device identity")
		}
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		sum := sha256.Sum256(body)
		message := method + "\n" + req.URL.Path + "\n" + stamp + "\n" + hex.EncodeToString(sum[:])
		req.Header.Set("X-Plainshow-Network", c.NetworkID)
		req.Header.Set("X-Plainshow-Device", c.Device.ID)
		req.Header.Set("X-Plainshow-Time", stamp)
		req.Header.Set("X-Plainshow-Signature", base64.RawURLEncoding.EncodeToString(c.Device.Sign([]byte(message))))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 128<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &failure)
		if failure.Error == "" {
			failure.Error = strings.TrimSpace(string(raw))
		}
		return fmt.Errorf("peer returned %s: %s", resp.Status, failure.Error)
	}
	if output != nil && len(raw) > 0 {
		return json.Unmarshal(raw, output)
	}
	return nil
}

type PublicKeyLookup func(networkID, deviceID string) (ed25519.PublicKey, error)

func Authenticate(next http.Handler, lookup PublicKeyLookup) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		networkID, deviceID := r.Header.Get("X-Plainshow-Network"), r.Header.Get("X-Plainshow-Device")

		stamp := r.Header.Get("X-Plainshow-Time")
		when, err := strconv.ParseInt(stamp, 10, 64)
		if err != nil || time.Since(time.Unix(when, 0)) > maxClockSkew || time.Until(time.Unix(when, 0)) > maxClockSkew {
			http.Error(w, `{"error":"mesh request timestamp is invalid"}`, http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 128<<20))
		if err != nil {
			http.Error(w, `{"error":"could not read request"}`, 400)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		key, err := lookup(networkID, deviceID)
		if err != nil {
			http.Error(w, `{"error":"device is not enrolled in this network"}`, 401)
			return
		}
		sig, err := base64.RawURLEncoding.DecodeString(r.Header.Get("X-Plainshow-Signature"))
		sum := sha256.Sum256(body)
		message := r.Method + "\n" + r.URL.Path + "\n" + stamp + "\n" + hex.EncodeToString(sum[:])
		if err != nil || !ed25519.Verify(key, []byte(message), sig) {
			http.Error(w, `{"error":"mesh request signature is invalid"}`, 401)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func TLSConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13,
		ClientAuth: tls.NoClientCert, RootCAs: x509.NewCertPool()}
}
