// Package tailnet drives the tailscale daemon.
//
// Plainshow does not implement a VPN, and does not embed one. It requires the
// tailscale daemon, brings it up with the join code's auth key, and asks it
// where the other machines are. That is the whole integration.
//
// The reason it has to be the real daemon rather than a library inside this
// binary: distributed training is torch and NCCL opening their own sockets, in
// their own processes. They need a network interface the kernel knows about.
// An in-process network stack would carry Plainshow's traffic and nothing else,
// which is exactly the traffic that was never the problem.
package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ErrNotInstalled is returned when the tailscale command is absent.
var ErrNotInstalled = errors.New("tailscale is not installed on this machine")

// runner executes the tailscale command. It is a variable so the parsing can be
// tested without a daemon present.
var runner = func(ctx context.Context, args ...string) ([]byte, error) {
	path, err := exec.LookPath("tailscale")
	if err != nil {
		return nil, ErrNotInstalled
	}
	cmd := exec.CommandContext(ctx, path, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = err.Error()
		}
		return out, fmt.Errorf("tailscale %s: %s", strings.Join(args, " "), message)
	}
	return out, nil
}

// Peer is one machine on the tailnet.
type Peer struct {
	Hostname string `json:"hostname"`
	Address  string `json:"address"`
	Online   bool   `json:"online"`
	OS       string `json:"os"`
	// Relayed reports that traffic is going through a relay rather than
	// directly. It still works, but it is slower and shares someone else's
	// bandwidth — worth showing before somebody starts a training run over it.
	Relayed bool `json:"relayed"`
}

// Status is what the daemon reports.
type Status struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	State     string `json:"state"`
	Self      Peer   `json:"self"`
	Peers     []Peer `json:"peers"`
	Detail    string `json:"detail,omitempty"`
}

// raw mirrors the parts of `tailscale status --json` this needs. The command
// reports a great deal more; naming only what is used keeps an upstream
// addition from breaking the parse.
type raw struct {
	BackendState string `json:"BackendState"`
	Self         *rawPeer
	Peer         map[string]*rawPeer
}

type rawPeer struct {
	HostName     string   `json:"HostName"`
	DNSName      string   `json:"DNSName"`
	TailscaleIPs []string `json:"TailscaleIPs"`
	OS           string   `json:"OS"`
	Online       bool     `json:"Online"`
	Relay        string   `json:"Relay"`
	CurAddr      string   `json:"CurAddr"`
}

// convert turns the daemon's shape into ours.
func (p *rawPeer) convert() Peer {
	if p == nil {
		return Peer{}
	}
	peer := Peer{
		Hostname: p.HostName, OS: p.OS, Online: p.Online,
		// A peer reached through a relay has a relay name and no direct
		// address; once a direct path is found CurAddr fills in.
		Relayed: p.Relay != "" && p.CurAddr == "",
	}
	if peer.Hostname == "" {
		peer.Hostname = strings.TrimSuffix(p.DNSName, ".")
	}
	for _, ip := range p.TailscaleIPs {
		// Prefer IPv4: torch, NCCL and every address written into a config are
		// happier with one, and every tailnet node has one.
		if strings.Contains(ip, ".") {
			peer.Address = ip
			break
		}
		if peer.Address == "" {
			peer.Address = ip
		}
	}
	return peer
}

// Probe asks the daemon what it knows.
//
// A machine without tailscale is not an error: the cluster still works over any
// network where machines can already reach each other. It only means this one
// cannot join a cluster that spans networks.
func Probe(ctx context.Context) Status {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	out, err := runner(ctx, "status", "--json")
	if errors.Is(err, ErrNotInstalled) {
		return Status{Detail: "tailscale is not installed"}
	}
	status := Status{Installed: true}
	if err != nil && len(out) == 0 {
		status.Detail = err.Error()
		return status
	}

	var parsed raw
	if err := json.Unmarshal(out, &parsed); err != nil {
		status.Detail = "could not read what tailscale reported"
		return status
	}

	status.State = parsed.BackendState
	status.Running = parsed.BackendState == "Running"
	status.Self = parsed.Self.convert()
	for _, peer := range parsed.Peer {
		converted := peer.convert()
		if converted.Address == "" {
			continue
		}
		status.Peers = append(status.Peers, converted)
	}
	if !status.Running {
		status.Detail = explainState(parsed.BackendState)
	}
	return status
}

// explainState turns the daemon's state into something to act on.
func explainState(state string) string {
	switch state {
	case "NeedsLogin":
		return "tailscale is installed but not signed in"
	case "Stopped":
		return "tailscale is installed but stopped"
	case "NoState", "":
		return "tailscale is installed but has not started"
	default:
		return "tailscale is " + state
	}
}

// Up signs this machine into a tailnet with a pre-authorised key.
//
// The key comes from the join code, so joining a Plainshow network and joining
// its tailnet are one step to the person doing it.
func Up(ctx context.Context, authKey, hostname, loginServer string) error {
	if strings.TrimSpace(authKey) == "" {
		return errors.New("no tailscale auth key was supplied")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	args := []string{"up", "--auth-key=" + authKey, "--accept-dns=false"}
	if hostname != "" {
		args = append(args, "--hostname="+hostname)
	}
	if loginServer != "" {
		// Set when the cluster runs its own coordination server rather than
		// using the hosted one.
		args = append(args, "--login-server="+loginServer)
	}
	_, err := runner(ctx, args...)
	return err
}

// Address returns this machine's tailnet address, or "" when it has none.
func Address(ctx context.Context) string { return Probe(ctx).Self.Address }

// AddressFor finds a peer's tailnet address by hostname.
func AddressFor(status Status, hostname string) string {
	target := strings.ToLower(strings.TrimSuffix(hostname, "."))
	for _, peer := range status.Peers {
		if strings.ToLower(peer.Hostname) == target {
			return peer.Address
		}
		// A tailnet name is the hostname plus the tailnet suffix, so match the
		// first label too.
		if label, _, ok := strings.Cut(strings.ToLower(peer.Hostname), "."); ok && label == target {
			return peer.Address
		}
	}
	if strings.EqualFold(status.Self.Hostname, target) {
		return status.Self.Address
	}
	return ""
}
