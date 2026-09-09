// Package github connects a node to GitHub with a personal access token.
//
// The workflow is the Plainshow console's: paste one token, and from then on
// the product can read your repositories, link them to projects, and keep the
// people who may work on a project in step with the repository's collaborators
// in both directions.
//
// The awkward parts of GitHub's model are handled here rather than pushed at
// the user. In particular a *personal* repository has exactly one collaborator
// level — write. pull, triage, maintain and admin are organisation-only, and
// asking a personal repository for one of them silently leaves the person on
// write. So on a personal repository GitHub can say *who* is a collaborator but
// not *what they may do*, and capabilities there stay local and are never
// overwritten from GitHub.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ErrNotConnected is returned when no token has been saved on this node.
var ErrNotConnected = errors.New("this node has not connected a GitHub account")

// ErrBadCredentials is returned when GitHub rejects the saved token.
var ErrBadCredentials = errors.New("GitHub rejected the saved token")

const apiBase = "https://api.github.com"

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// ValidRepository reports whether s looks like "owner/name".
func ValidRepository(s string) bool { return repoPattern.MatchString(s) }

// ---------------------------------------------------------------- storage --

// TokenStore keeps the node's GitHub credential inside the install root.
type TokenStore struct{ Path string }

// Read returns the saved token.
func (t TokenStore) Read() (string, error) {
	raw, err := os.ReadFile(t.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNotConnected
		}
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", ErrNotConnected
	}
	return token, nil
}

// Write saves a token, readable only by the account running the node.
func (t TokenStore) Write(token string) error {
	if err := os.MkdirAll(filepath.Dir(t.Path), 0o700); err != nil {
		return err
	}
	tmp := t.Path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.TrimSpace(token)+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, t.Path)
}

// Clear removes the saved token.
func (t TokenStore) Clear() error {
	err := os.Remove(t.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Connected reports whether a token is present.
func (t TokenStore) Connected() bool {
	_, err := t.Read()
	return err == nil
}

// ----------------------------------------------------------------- client --

// Client talks to the GitHub REST API as one account.
type Client struct {
	Token string
	HTTP  *http.Client
}

// New returns a client for a token.
func New(token string) *Client {
	return &Client{Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// APIError is a non-success response from GitHub, carrying enough to explain
// itself to a person.
type APIError struct {
	Status  int
	Message string
	Path    string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("GitHub returned %d for %s", e.Status, e.Path)
	}
	return fmt.Sprintf("GitHub: %s (%d)", e.Message, e.Status)
}

// Friendly renders an error in the words the interface should show.
func Friendly(err error) string {
	var apiErr *APIError
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrNotConnected):
		return "No GitHub account is connected to this node."
	case errors.Is(err, ErrBadCredentials):
		return "The saved token is no longer valid. Connect a new one."
	case errors.As(err, &apiErr):
		switch apiErr.Status {
		case http.StatusUnauthorized:
			return "The saved token is no longer valid. Connect a new one."
		case http.StatusForbidden:
			return "The token does not have permission for that. It needs the full repo scope."
		case http.StatusNotFound:
			return "GitHub could not find that repository, or the token cannot see it."
		case http.StatusUnprocessableEntity:
			return "GitHub refused that request: " + apiErr.Message
		}
		return apiErr.Error()
	}
	return err.Error()
}

// do performs one API call and decodes the result into out.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "plainshow-cluster")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer res.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return err
	}
	if res.StatusCode == http.StatusUnauthorized {
		return ErrBadCredentials
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var detail struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(payload, &detail)
		return &APIError{Status: res.StatusCode, Message: detail.Message, Path: path}
	}
	if out == nil || len(payload) == 0 {
		return nil
	}
	return json.Unmarshal(payload, out)
}

// ------------------------------------------------------------------ types --

// Account is the identity behind a token.
type Account struct {
	Login string `json:"login"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Type  string `json:"type"`
}

// Repository is one repository visible to the token.
type Repository struct {
	FullName    string `json:"full_name"`
	Name        string `json:"name"`
	Private     bool   `json:"private"`
	Description string `json:"description"`
	CloneURL    string `json:"clone_url"`
	Pushed      string `json:"pushed_at"`
	Permissions struct {
		Admin bool `json:"admin"`
		Push  bool `json:"push"`
		Pull  bool `json:"pull"`
	} `json:"permissions"`
	Owner struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"owner"`
}

// Collaborator is one person on a repository.
type Collaborator struct {
	Login       string `json:"login"`
	AvatarURL   string `json:"avatar_url"`
	RoleName    string `json:"role_name"`
	Permissions struct {
		Admin    bool `json:"admin"`
		Maintain bool `json:"maintain"`
		Push     bool `json:"push"`
		Triage   bool `json:"triage"`
		Pull     bool `json:"pull"`
	} `json:"permissions"`
}

// RepositoryInvitation is an open collaborator invitation. GitHub does not
// include these people in Collaborators until they accept, so sync must read
// both lists before deciding that somebody was removed.
type RepositoryInvitation struct {
	Invitee struct {
		Login string `json:"login"`
	} `json:"invitee"`
	Permissions string `json:"permissions"`
}

// Role reads a collaborator's effective role, preferring the permission flags
// because role_name is absent on some responses.
func (c Collaborator) Role() string {
	switch {
	case c.Permissions.Admin:
		return "admin"
	case c.Permissions.Maintain:
		return "maintain"
	case c.Permissions.Push:
		return "push"
	case c.Permissions.Triage:
		return "triage"
	case c.Permissions.Pull:
		return "pull"
	}
	if c.RoleName != "" {
		return strings.ToLower(c.RoleName)
	}
	return "pull"
}

// ------------------------------------------------------------------- calls --

// Viewer returns the account the token belongs to.
func (c *Client) Viewer(ctx context.Context) (Account, error) {
	var a Account
	err := c.do(ctx, http.MethodGet, "/user", nil, &a)
	return a, err
}

// Repositories lists everything the token can see, newest activity first.
func (c *Client) Repositories(ctx context.Context) ([]Repository, error) {
	all := []Repository{}
	for page := 1; page <= 10; page++ {
		var batch []Repository
		path := fmt.Sprintf(
			"/user/repos?visibility=all&affiliation=owner,collaborator,organization_member"+
				"&sort=updated&direction=desc&per_page=100&page=%d", page)
		if err := c.do(ctx, http.MethodGet, path, nil, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

// Repository fetches one repository.
func (c *Client) Repository(ctx context.Context, fullName string) (Repository, error) {
	var r Repository
	if !ValidRepository(fullName) {
		return r, fmt.Errorf("%q is not a repository name like owner/name", fullName)
	}
	err := c.do(ctx, http.MethodGet, "/repos/"+fullName, nil, &r)
	return r, err
}

// CreateRepository makes a new repository under the token's own account.
func (c *Client) CreateRepository(ctx context.Context, name, description string, private bool) (Repository, error) {
	var r Repository
	body := map[string]any{
		"name": name, "description": description, "private": private,
		// No auto-init: the project already has commits, and an initial commit
		// on GitHub would mean the first push is a conflict.
		"auto_init": false,
	}
	err := c.do(ctx, http.MethodPost, "/user/repos", body, &r)
	return r, err
}

// Collaborators lists the people directly on a repository.
func (c *Client) Collaborators(ctx context.Context, repo string) ([]Collaborator, error) {
	all := []Collaborator{}
	for page := 1; page <= 10; page++ {
		var batch []Collaborator
		path := fmt.Sprintf("/repos/%s/collaborators?affiliation=direct&per_page=100&page=%d",
			repo, page)
		if err := c.do(ctx, http.MethodGet, path, nil, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

// RepositoryInvitations lists currently open invitations for a repository.
// GitHub requires repository administration permission for this endpoint.
func (c *Client) RepositoryInvitations(ctx context.Context, repo string) ([]RepositoryInvitation, error) {
	all := []RepositoryInvitation{}
	for page := 1; page <= 10; page++ {
		var batch []RepositoryInvitation
		path := fmt.Sprintf("/repos/%s/invitations?per_page=100&page=%d", repo, page)
		if err := c.do(ctx, http.MethodGet, path, nil, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

// AddCollaborator invites or re-roles someone on a repository.
func (c *Client) AddCollaborator(ctx context.Context, repo, login, role string) error {
	return c.do(ctx, http.MethodPut,
		fmt.Sprintf("/repos/%s/collaborators/%s", repo, login),
		map[string]string{"permission": role}, nil)
}

// RemoveCollaborator withdraws someone from a repository.
func (c *Client) RemoveCollaborator(ctx context.Context, repo, login string) error {
	return c.do(ctx, http.MethodDelete,
		fmt.Sprintf("/repos/%s/collaborators/%s", repo, login), nil, nil)
}

// UserExists reports whether a GitHub account exists, so a typo is caught
// before it becomes a failed invitation.
func (c *Client) UserExists(ctx context.Context, login string) (bool, error) {
	err := c.do(ctx, http.MethodGet, "/users/"+login, nil, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
