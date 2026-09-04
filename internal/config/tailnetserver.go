package config

import (
	"errors"
	"os"
	"strings"
)

// SaveTailnetServer records which control plane enrolled this device. The
// marker contains no credential, but uses private permissions alongside the
// account token so the node's identity state stays in one place.
func SaveTailnetServer(layout Layout, server string) error {
	temporary := layout.TailnetServer() + ".tmp"
	if err := os.WriteFile(temporary, []byte(strings.TrimRight(strings.TrimSpace(server), "/")+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, layout.TailnetServer())
}

// LoadTailnetServer returns the control plane that last enrolled this device.
func LoadTailnetServer(layout Layout) (string, error) {
	raw, err := os.ReadFile(layout.TailnetServer())
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}
