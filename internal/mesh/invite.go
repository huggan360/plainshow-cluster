// Package mesh connects installations without merging their identities or
// network memberships. The direct transport is TLS with a certificate pinned
// by a signed, single-use invitation.
package mesh

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const invitePrefix = "psc1_"

type Invite struct {
	Version     int    `json:"v"`
	ID          string `json:"id"`
	NetworkID   string `json:"network_id"`
	NetworkName string `json:"network_name"`
	Endpoint    string `json:"endpoint"`
	Fingerprint string `json:"fingerprint"`
	Token       string `json:"token"`
	Role        string `json:"role"`
	Expires     string `json:"expires_at"`
}

func NewInvite(networkID, networkName, endpoint, fingerprint, role string,
	lifetime time.Duration) (Invite, string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return Invite{}, "", err
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	digest := sha256.Sum256([]byte(token))
	invite := Invite{
		Version: 1, ID: randomID(random[:8]), NetworkID: networkID,
		NetworkName: networkName, Endpoint: strings.TrimRight(endpoint, "/"),
		Fingerprint: fingerprint, Token: token, Role: role,
		Expires: time.Now().UTC().Add(lifetime).Format(time.RFC3339),
	}
	return invite, base64.RawURLEncoding.EncodeToString(digest[:]), nil
}

func (i Invite) Encode() (string, error) {
	if err := i.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(i)
	if err != nil {
		return "", err
	}
	return invitePrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func DecodeInvite(code string) (Invite, error) {
	var invite Invite
	code = strings.TrimSpace(code)
	if !strings.HasPrefix(code, invitePrefix) {
		return invite, errors.New("that is not a Plainshow Cluster join code")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(code, invitePrefix))
	if err != nil || json.Unmarshal(raw, &invite) != nil {
		return invite, errors.New("the join code is damaged or incomplete")
	}
	if err := invite.Validate(); err != nil {
		return invite, err
	}
	return invite, nil
}

func (i Invite) Validate() error {
	if i.Version != 1 || i.ID == "" || i.NetworkID == "" || i.Token == "" ||
		i.Endpoint == "" || i.Fingerprint == "" {
		return errors.New("the join code is missing required information")
	}
	expires, err := time.Parse(time.RFC3339, i.Expires)
	if err != nil {
		return errors.New("the join code has an invalid expiry")
	}
	if time.Now().After(expires) {
		return errors.New("the join code has expired")
	}
	return nil
}

func TokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func randomID(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }
