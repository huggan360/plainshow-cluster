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
	Network    accountserver.EnterpriseNetwork  `json:"network"`
	Members    []accountserver.EnterpriseMember `json:"members"`
	Controller struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Address     string `json:"address"`
		CollabToken string `json:"collab_token"`
	} `json:"controller"`
}

func (c *Client) SetNetworkMemberRole(ctx context.Context, token, networkID,
	managementKey, accountID, role string) error {
	return c.call(ctx, http.MethodPut, "/api/networks/"+url.PathEscape(networkID)+
		"/members/"+url.PathEscape(accountID), token,
		map[string]string{"management_key": managementKey, "role": role}, nil)
}

func (c *Client) RemoveNetworkMember(ctx context.Context, token, networkID,
	managementKey, accountID string) error {
	return c.call(ctx, http.MethodDelete, "/api/networks/"+url.PathEscape(networkID)+
		"/members/"+url.PathEscape(accountID), token,
		map[string]string{"management_key": managementKey}, nil)
}

func (c *Client) DeleteNetwork(ctx context.Context, token, networkID, managementKey string) error {
	return c.call(ctx, http.MethodDelete, "/api/networks/"+url.PathEscape(networkID), token,
		map[string]string{"management_key": managementKey}, nil)
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

// CheckIn sends aggregate device counts, never project or job contents, and
// returns whatever the account service is asking this device to do next.
//
// The heartbeat is the only channel back to a device, so it carries both
// directions. A device that is offline is not a delivery failure: it collects
// its instruction when it returns, which is the first moment it could have
// acted on it anyway.
func (c *Client) CheckIn(ctx context.Context, token string,
	input accountserver.NodeCheckIn) (accountserver.NodeInstructions, error) {
	var out struct {
		Instructions accountserver.NodeInstructions `json:"instructions"`
	}
	err := c.call(ctx, http.MethodPost, "/api/nodes/check-in", token, input, &out)
	return out.Instructions, err
}

// Devices lists every machine signed in to this account.
func (c *Client) Devices(ctx context.Context, token string) ([]accountserver.AccountDevice, error) {
	var out struct {
		Devices []accountserver.AccountDevice `json:"devices"`
	}
	err := c.call(ctx, http.MethodGet, "/api/devices", token, nil, &out)
	return out.Devices, err
}

// SetDeviceNetwork asks one of this account's machines to work in a network.
func (c *Client) SetDeviceNetwork(ctx context.Context, token, nodeID, networkID string) error {
	return c.call(ctx, http.MethodPut, "/api/devices/"+url.PathEscape(nodeID)+"/network",
		token, map[string]string{"network_id": networkID}, nil)
}

// SignOutDevice asks a machine to forget its account credential.
func (c *Client) SignOutDevice(ctx context.Context, token, nodeID string) error {
	return c.call(ctx, http.MethodPost, "/api/devices/"+url.PathEscape(nodeID)+"/sign-out",
		token, map[string]string{}, nil)
}

// RemoveDevice forgets a machine.
func (c *Client) RemoveDevice(ctx context.Context, token, nodeID string) error {
	return c.call(ctx, http.MethodDelete, "/api/devices/"+url.PathEscape(nodeID), token, nil, nil)
}

// SearchAccounts finds people to invite by name.
func (c *Client) SearchAccounts(ctx context.Context, token, query string) ([]accountserver.Account, error) {
	var out struct {
		Accounts []accountserver.Account `json:"accounts"`
	}
	err := c.call(ctx, http.MethodGet, "/api/accounts/search?q="+url.QueryEscape(query),
		token, nil, &out)
	return out.Accounts, err
}

// Invitations lists what is waiting for this account to answer.
func (c *Client) Invitations(ctx context.Context, token string) ([]accountserver.Invitation, error) {
	var out struct {
		Invitations []accountserver.Invitation `json:"invitations"`
	}
	err := c.call(ctx, http.MethodGet, "/api/invitations", token, nil, &out)
	return out.Invitations, err
}

// NetworkInvitations lists what one network has outstanding.
func (c *Client) NetworkInvitations(ctx context.Context, token, networkID string) ([]accountserver.Invitation, error) {
	var out struct {
		Invitations []accountserver.Invitation `json:"invitations"`
	}
	err := c.call(ctx, http.MethodGet,
		"/api/networks/"+url.PathEscape(networkID)+"/invitations", token, nil, &out)
	return out.Invitations, err
}

// CreateInvitation offers network membership to one person.
func (c *Client) CreateInvitation(ctx context.Context, token, networkID, username,
	role string) (accountserver.Invitation, error) {
	var out accountserver.Invitation
	err := c.call(ctx, http.MethodPost, "/api/networks/"+url.PathEscape(networkID)+"/invitations",
		token, map[string]string{"username": username, "role": role}, &out)
	return out, err
}

// RespondToInvitation accepts or declines an offer addressed to this account.
func (c *Client) RespondToInvitation(ctx context.Context, token, id string,
	accept bool) (accountserver.Invitation, error) {
	action := "/decline"
	if accept {
		action = "/accept"
	}
	var out accountserver.Invitation
	err := c.call(ctx, http.MethodPost, "/api/invitations/"+url.PathEscape(id)+action,
		token, map[string]string{}, &out)
	return out, err
}

// RevokeInvitation withdraws an offer that has not been answered.
func (c *Client) RevokeInvitation(ctx context.Context, token, id string) error {
	return c.call(ctx, http.MethodDelete, "/api/invitations/"+url.PathEscape(id), token, nil, nil)
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

// MyNetworks lists every network this account belongs to, whether or not this
// device has ever heard of them. It is the read that lets a machine somebody
// has just signed in on show the networks they already have elsewhere.
func (c *Client) MyNetworks(ctx context.Context, token string) ([]accountserver.AccountNetwork, error) {
	state, err := c.MyNetworkState(ctx, token)
	return state.Networks, err
}

type NetworkState struct {
	Networks          []accountserver.AccountNetwork `json:"networks"`
	DeletedNetworkIDs []string                       `json:"deleted_network_ids"`
}

// MyNetworkState includes explicit deletion tombstones. Absence alone cannot
// delete a local network because it may be waiting for its first successful
// registration while the account service is temporarily unavailable.
func (c *Client) MyNetworkState(ctx context.Context, token string) (NetworkState, error) {
	var out NetworkState
	err := c.call(ctx, http.MethodGet, "/api/networks/mine", token, nil, &out)
	return out, err
}

// GitHubToken fetches the credential the account keeps, so a machine that has
// never had one connected picks it up.
func (c *Client) GitHubToken(ctx context.Context, token string) (string, string, error) {
	var out struct {
		Token string `json:"token"`
		Login string `json:"login"`
	}
	err := c.call(ctx, http.MethodGet, "/api/github/token", token, nil, &out)
	return out.Token, out.Login, err
}

// PublishGitHubToken shares a credential connected here with the account's
// other machines. An empty token disconnects everywhere.
func (c *Client) PublishGitHubToken(ctx context.Context, token, github, login string) error {
	return c.call(ctx, http.MethodPut, "/api/github/token", token,
		map[string]string{"token": github, "login": login}, nil)
}

// Projects lists every project this account owns or belongs to. Metadata only:
// the files never pass through the account service.
func (c *Client) Projects(ctx context.Context, token string) ([]accountserver.AccountProject, error) {
	var out struct {
		Projects []accountserver.AccountProject `json:"projects"`
	}
	err := c.call(ctx, http.MethodGet, "/api/projects/mine", token, nil, &out)
	return out.Projects, err
}

// SyncProject reports a project this machine holds.
func (c *Client) SyncProject(ctx context.Context, token string,
	input accountserver.ProjectRegistration) (accountserver.AccountProject, error) {
	var out accountserver.AccountProject
	err := c.call(ctx, http.MethodPost, "/api/projects/sync", token, input, &out)
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
