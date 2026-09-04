// Package accountclient talks to the configured Plainshow Account Server.
package accountclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/accountserver"
)

// Client is a small HTTPS client for central identity and aggregate check-ins.
type Client struct {
	endpoint string
	http     *http.Client
}

// AuthResponse is returned by registration and login.
type AuthResponse struct {
	Account accountserver.Account `json:"account"`
	Token   string                `json:"token"`
}

// SessionResponse describes the identity behind a bearer token.
type SessionResponse struct {
	Authenticated bool                  `json:"authenticated"`
	Account       accountserver.Account `json:"account"`
}

// NetworkResponse connects a peer network to the enterprise management plane
// and its single collaboration controller.
type NetworkResponse struct {
	Network    accountserver.EnterpriseNetwork `json:"network"`
	Controller struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Address     string `json:"address"`
		CollabToken string `json:"collab_token"`
	} `json:"controller"`
}

// New validates a configured account-server URL. Plain HTTP is accepted only
// on loopback so development does not weaken production credentials.
func New(endpoint string) (*Client, error) {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return nil, errors.New("account server must be a complete URL")
	}
	host := parsed.Hostname()
	loopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopback) {
		return nil, errors.New("account server must use HTTPS (plain HTTP is allowed only on loopback)")
	}
	return &Client{endpoint: endpoint, http: &http.Client{Timeout: 20 * time.Second}}, nil
}

// Register creates a global identity.
func (c *Client) Register(ctx context.Context, username, displayName, password, bootstrap string) (AuthResponse, error) {
	var out AuthResponse
	err := c.call(ctx, http.MethodPost, "/api/auth/register", "", map[string]string{
		"username": username, "display_name": displayName, "password": password,
		"bootstrap_token": bootstrap,
	}, &out)
	return out, err
}

// Login validates a global identity.
func (c *Client) Login(ctx context.Context, username, password string) (AuthResponse, error) {
	var out AuthResponse
	err := c.call(ctx, http.MethodPost, "/api/auth/login", "", map[string]string{
		"username": username, "password": password,
	}, &out)
	return out, err
}

// Session validates a bearer token and returns its global identity.
func (c *Client) Session(ctx context.Context, token string) (accountserver.Account, error) {
	var out SessionResponse
	if err := c.call(ctx, http.MethodGet, "/api/auth/status", token, nil, &out); err != nil {
		return out.Account, err
	}
	if !out.Authenticated || out.Account.ID == "" {
		return out.Account, errors.New("the Plainshow account session is no longer valid")
	}
	return out.Account, nil
}

// CheckIn sends aggregate device counts, never project or job contents.
func (c *Client) CheckIn(ctx context.Context, token string, input accountserver.NodeCheckIn) error {
	return c.call(ctx, http.MethodPost, "/api/nodes/check-in", token, input, nil)
}

// TailnetEnrollment asks the global account service for a one-time Headscale
// key. Users authenticate only to PlainShow; the private transport enrollment
// happens behind that account session.
func (c *Client) TailnetEnrollment(ctx context.Context, token string) (accountserver.TailnetEnrollment, error) {
	var out accountserver.TailnetEnrollment
	err := c.call(ctx, http.MethodPost, "/api/tailnet/enrollment", token, map[string]string{}, &out)
	return out, err
}

// SyncNetwork proves possession of a network key and obtains the enterprise
// collaboration-controller credential.
func (c *Client) SyncNetwork(ctx context.Context, token string,
	input accountserver.NetworkRegistration) (NetworkResponse, error) {
	var out NetworkResponse
	err := c.call(ctx, http.MethodPost, "/api/networks/sync", token, input, &out)
	return out, err
}

// GrantNetworkMember records the account authenticated by a consumed peer
// invitation in the enterprise registry.
func (c *Client) GrantNetworkMember(ctx context.Context, token, networkID,
	managementKey, accountID, role string) error {
	return c.call(ctx, http.MethodPost, "/api/networks/"+url.PathEscape(networkID)+"/members",
		token, map[string]string{"management_key": managementKey,
			"account_id": accountID, "role": role}, nil)
}

func (c *Client) call(ctx context.Context, method, path, token string, input, output any) error {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("account server is unavailable: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &failure)
		if failure.Error == "" {
			failure.Error = response.Status
		}
		return errors.New(failure.Error)
	}
	if output != nil {
		return json.Unmarshal(raw, output)
	}
	return nil
}
