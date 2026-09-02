package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// CLIToken is the credential the command line uses to reach its own daemon.
//
// It lives in the install root at 0600. That grants nothing new: anyone who can
// read it can already read the database sitting next to it. What it buys is a
// command line that keeps working after the node has an owner account, without
// weakening the browser's session rules or trusting "it came from loopback".
func (l Layout) CLIToken() string { return filepath.Join(l.Root, "keys", "cli.token") }

// EnsureCLIToken returns the local token, creating it on first use.
func EnsureCLIToken(l Layout) (string, error) {
	if token, err := ReadCLIToken(l); err == nil {
		return token, nil
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	if err := os.MkdirAll(filepath.Dir(l.CLIToken()), 0o700); err != nil {
		return "", err
	}
	tmp := l.CLIToken() + ".tmp"
	if err := os.WriteFile(tmp, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, l.CLIToken()); err != nil {
		return "", err
	}
	return token, nil
}

// ReadCLIToken returns the saved local token.
func ReadCLIToken(l Layout) (string, error) {
	raw, err := os.ReadFile(l.CLIToken())
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", errors.New("the local token file is empty")
	}
	return token, nil
}
