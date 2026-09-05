package config

import (
	"errors"
	"os"
	"strings"
)

// SaveAccountToken atomically stores the global account bearer token inside
// the node root and readable only by its owner.
func SaveAccountToken(layout Layout, token string) error {
	temporary := layout.AccountToken() + ".tmp"
	if err := os.WriteFile(temporary, []byte(strings.TrimSpace(token)+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, layout.AccountToken())
}

// LoadAccountToken returns the cached central bearer token.
func LoadAccountToken(layout Layout) (string, error) {
	raw, err := os.ReadFile(layout.AccountToken())
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

// ForgetAccountToken removes the cached bearer token.
//
// Signing out has to reach this, not only the browser session: a node that
// keeps a valid account credential is a node that can hand itself a new session
// (see AuthConfig.RememberThisMachine), so deleting the cookie alone would make
// "Sign out" do nothing at all.
func ForgetAccountToken(layout Layout) error {
	err := os.Remove(layout.AccountToken())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
