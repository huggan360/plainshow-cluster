package accountserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TailnetEnrollment is the short-lived material a signed-in PlainShow node
// needs to join the shared private network. The key is one-time and is never
// stored by PlainShow.
type TailnetEnrollment struct {
	LoginServer string `json:"login_server"`
	AuthKey     string `json:"auth_key"`
	ExpiresIn   string `json:"expires_in"`
}

type tailnetProvisioner interface {
	Enrollment(context.Context, Account) (TailnetEnrollment, error)
	Disable(context.Context, string) error
}

type headscaleNode struct {
	ID uint64 `json:"id"`
}

// headscaleProvisioner maps each global PlainShow identity to one Headscale
// user and mints a one-time device key. The mutex closes the harmless but noisy
// race where two devices create the same user simultaneously.
type headscaleProvisioner struct {
	config TailnetConfig
	mu     sync.Mutex
}

type headscaleUser struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
}

type headscalePreauthKey struct {
	Key string `json:"key"`
}

func (p *headscaleProvisioner) Enrollment(ctx context.Context, account Account) (TailnetEnrollment, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	ttl := p.config.EnrollmentTTL
	duration, err := time.ParseDuration(ttl)
	if err != nil || duration < time.Minute || duration > time.Hour {
		return TailnetEnrollment{}, errors.New("Headscale enrollment_ttl must be between 1m and 1h")
	}
	name := "plainshow-" + account.ID
	user, err := p.user(ctx, name, account.DisplayName)
	if err != nil {
		return TailnetEnrollment{}, err
	}
	raw, err := p.run(ctx, "--output", "json", "preauthkeys", "create",
		"--user", strconv.FormatUint(user.ID, 10), "--expiration", ttl)
	if err != nil {
		return TailnetEnrollment{}, err
	}
	var key headscalePreauthKey
	if err := json.Unmarshal(raw, &key); err != nil || strings.TrimSpace(key.Key) == "" {
		return TailnetEnrollment{}, errors.New("Headscale returned an invalid enrollment key")
	}
	return TailnetEnrollment{LoginServer: p.config.LoginServer,
		AuthKey: key.Key, ExpiresIn: ttl}, nil
}

// Disable immediately expires every transport identity belonging to a disabled
// PlainShow account. Re-enabling the account requires a fresh PlainShow login,
// which automatically enrolls each device again.
func (p *headscaleProvisioner) Disable(ctx context.Context, accountID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	name := "plainshow-" + accountID
	raw, err := p.run(ctx, "--output", "json", "users", "list")
	if err != nil {
		return err
	}
	var users []headscaleUser
	if strings.TrimSpace(string(raw)) != "null" {
		if err := json.Unmarshal(raw, &users); err != nil {
			return errors.New("Headscale returned an invalid user list")
		}
	}
	found := false
	for _, user := range users {
		if user.Name == name {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	raw, err = p.run(ctx, "--output", "json", "nodes", "list", "--user", name)
	if err != nil {
		return err
	}
	var nodes []headscaleNode
	if strings.TrimSpace(string(raw)) != "null" {
		if err := json.Unmarshal(raw, &nodes); err != nil {
			return errors.New("Headscale returned an invalid node list")
		}
	}
	for _, node := range nodes {
		if _, err := p.run(ctx, "nodes", "expire", "--identifier", strconv.FormatUint(node.ID, 10)); err != nil {
			return err
		}
	}
	return nil
}

func (p *headscaleProvisioner) user(ctx context.Context, name, displayName string) (headscaleUser, error) {
	raw, err := p.run(ctx, "--output", "json", "users", "list")
	if err != nil {
		return headscaleUser{}, err
	}
	var users []headscaleUser
	if string(raw) != "null\n" && string(raw) != "null" {
		if err := json.Unmarshal(raw, &users); err != nil {
			return headscaleUser{}, errors.New("Headscale returned an invalid user list")
		}
	}
	for _, user := range users {
		if user.Name == name {
			return user, nil
		}
	}
	args := []string{"--output", "json", "users", "create", name}
	if strings.TrimSpace(displayName) != "" {
		args = append(args, "--display-name", displayName)
	}
	raw, err = p.run(ctx, args...)
	if err != nil {
		return headscaleUser{}, err
	}
	var user headscaleUser
	if err := json.Unmarshal(raw, &user); err != nil || user.ID == 0 {
		return headscaleUser{}, errors.New("Headscale returned an invalid new user")
	}
	return user, nil
}

func (p *headscaleProvisioner) run(ctx context.Context, args ...string) ([]byte, error) {
	if p.config.HeadscaleConfig != "" {
		args = append([]string{"--config", p.config.HeadscaleConfig}, args...)
	}
	command := exec.CommandContext(ctx, p.config.HeadscaleBin, args...)
	raw, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(raw))
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("Headscale enrollment failed: %s", message)
	}
	return raw, nil
}
