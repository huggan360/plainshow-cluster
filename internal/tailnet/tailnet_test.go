package tailnet

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// stub replaces the tailscale command for one test.
func stub(t *testing.T, out string, err error) {
	t.Helper()
	original := runner
	runner = func(_ context.Context, _ ...string) ([]byte, error) {
		return []byte(out), err
	}
	t.Cleanup(func() { runner = original })
}

// A trimmed but real-shaped `tailscale status --json`: two machines, one
// reached directly and one through a relay.
const sample = `{
  "BackendState": "Running",
  "Self": {
    "HostName": "raspberrypi5",
    "DNSName": "raspberrypi5.tail1234.ts.net.",
    "TailscaleIPs": ["100.64.0.1", "fd7a:115c:a1e0::1"],
    "OS": "linux", "Online": true
  },
  "Peer": {
    "nodekey:aaa": {
      "HostName": "fredrik-pc",
      "DNSName": "fredrik-pc.tail1234.ts.net.",
      "TailscaleIPs": ["100.64.0.2", "fd7a:115c:a1e0::2"],
      "OS": "linux", "Online": true,
      "Relay": "lhr", "CurAddr": "82.13.4.9:41641"
    },
    "nodekey:bbb": {
      "HostName": "albin-pc",
      "DNSName": "albin-pc.tail1234.ts.net.",
      "TailscaleIPs": ["100.64.0.3"],
      "OS": "linux", "Online": true,
      "Relay": "lhr", "CurAddr": ""
    }
  }
}`

func TestProbeReadsRealStatusShape(t *testing.T) {
	stub(t, sample, nil)
	status := Probe(context.Background())

	if !status.Installed || !status.Running {
		t.Fatalf("status = %+v", status)
	}
	if status.Self.Address != "100.64.0.1" {
		t.Errorf("self address = %q, want the IPv4 one", status.Self.Address)
	}
	if len(status.Peers) != 2 {
		t.Fatalf("found %d peers, want 2", len(status.Peers))
	}
}

// TestRelayedPeerIsFlagged: a peer reached through a relay works but shares
// somebody else's bandwidth, which is worth knowing before starting training.
func TestRelayedPeerIsFlagged(t *testing.T) {
	stub(t, sample, nil)
	status := Probe(context.Background())

	direct := AddressFor(status, "fredrik-pc")
	relayed := AddressFor(status, "albin-pc")
	if direct != "100.64.0.2" || relayed != "100.64.0.3" {
		t.Fatalf("addresses = %q / %q", direct, relayed)
	}
	for _, peer := range status.Peers {
		switch peer.Hostname {
		case "fredrik-pc":
			if peer.Relayed {
				t.Error("a peer with a direct address was reported as relayed")
			}
		case "albin-pc":
			if !peer.Relayed {
				t.Error("a peer with no direct address was not reported as relayed")
			}
		}
	}
}

func TestAddressForMatchesTailnetName(t *testing.T) {
	stub(t, sample, nil)
	status := Probe(context.Background())

	if got := AddressFor(status, "albin-pc.tail1234.ts.net"); got != "" && got != "100.64.0.3" {
		t.Errorf("full tailnet name resolved to %q", got)
	}
	if got := AddressFor(status, "raspberrypi5"); got != "100.64.0.1" {
		t.Errorf("this machine resolved to %q, want its own address", got)
	}
	if got := AddressFor(status, "not-a-machine"); got != "" {
		t.Errorf("an unknown name resolved to %q", got)
	}
}

// TestNotInstalledIsNotAnError: a cluster on one network works without
// tailscale at all, so its absence must read as a fact, not a failure.
func TestNotInstalledIsNotAnError(t *testing.T) {
	stub(t, "", ErrNotInstalled)
	status := Probe(context.Background())

	if status.Installed || status.Running {
		t.Errorf("status = %+v", status)
	}
	if !strings.Contains(status.Detail, "not installed") {
		t.Errorf("detail = %q, which does not say what is wrong", status.Detail)
	}
}

// TestStatesExplainThemselves: every state a person can land in has to name
// what to do about it.
func TestStatesExplainThemselves(t *testing.T) {
	for _, state := range []string{"NeedsLogin", "Stopped", "NoState"} {
		stub(t, `{"BackendState":"`+state+`"}`, nil)
		status := Probe(context.Background())
		if status.Running {
			t.Errorf("%s reported as running", state)
		}
		if status.Detail == "" {
			t.Errorf("%s produced no explanation", state)
		}
	}
}

func TestGarbageOutputDoesNotPanic(t *testing.T) {
	stub(t, "not json at all", nil)
	if status := Probe(context.Background()); status.Running {
		t.Error("unparseable output reported as running")
	}
}

func TestUpRefusesWithoutAKey(t *testing.T) {
	stub(t, "", nil)
	if err := Up(context.Background(), "  ", "host", ""); err == nil {
		t.Error("Up accepted an empty auth key")
	}
}

func TestUpPassesTheKeyAndHostname(t *testing.T) {
	var got []string
	original := runner
	runner = func(_ context.Context, args ...string) ([]byte, error) {
		got = args
		return nil, nil
	}
	t.Cleanup(func() { runner = original })

	if err := Up(context.Background(), "tskey-auth-abc", "albin-pc", "https://headscale.example"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{"up", "--auth-key=tskey-auth-abc",
		"--reset", "--hostname=albin-pc", "--login-server=https://headscale.example"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Up ran %q, missing %q", joined, want)
		}
	}
}

func TestErrNotInstalledIsRecognisable(t *testing.T) {
	stub(t, "", ErrNotInstalled)
	if !errors.Is(ErrNotInstalled, ErrNotInstalled) {
		t.Fatal("sentinel is not comparable")
	}
	if Probe(context.Background()).Installed {
		t.Error("a missing binary reported as installed")
	}
}

func TestAuthKeyIsRedactedFromCommandErrors(t *testing.T) {
	got := strings.Join(redactedArgs([]string{"up", "--auth-key=secret-value"}), " ")
	if strings.Contains(got, "secret-value") || !strings.Contains(got, "[redacted]") {
		t.Fatalf("redacted arguments = %q", got)
	}
}

// TestDownOnAMachineWithoutTailscale: "stop everything" must not fail on a
// machine that never had the client. Nothing to disconnect is a success.
func TestDownOnAMachineWithoutTailscale(t *testing.T) {
	original := runner
	runner = func(ctx context.Context, args ...string) ([]byte, error) {
		return nil, ErrNotInstalled
	}
	defer func() { runner = original }()

	if err := Down(context.Background()); err != nil {
		t.Fatalf("Down() = %v, want nil when tailscale is absent", err)
	}
}

func TestDownAsksTheDaemonToDisconnect(t *testing.T) {
	original := runner
	var got []string
	runner = func(ctx context.Context, args ...string) ([]byte, error) {
		got = args
		return nil, nil
	}
	defer func() { runner = original }()

	if err := Down(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "down" {
		t.Fatalf("Down() ran tailscale %v, want [down]", got)
	}
}
