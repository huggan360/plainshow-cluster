package api

import (
	"net"
	"strconv"
	"strings"
	"testing"
)

// TestProbeTCPFindsSomethingListening: the plain case.
func TestProbeTCPFindsSomethingListening(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)

	if got := probeTCP(host, port); !got.Reachable {
		t.Errorf("a listening port read as unreachable: %+v", got)
	}
}

// TestProbeTCPRefusedIsStillARoute is the subtle one, and the reason this is
// tested rather than assumed.
//
// The rendezvous port has nothing listening until the run actually starts. So
// "connection refused" is the *expected* answer from a machine that can reach
// its peer, and treating it as a failure would refuse every correctly
// configured cluster. What matters is that something answered at all.
func TestProbeTCPRefusedIsStillARoute(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	listener.Close() // nothing is listening now, but the host is right here

	got := probeTCP(host, port)
	if got.Reachable {
		t.Skip("the port was reused before the probe ran")
	}
	if !strings.Contains(got.Detail, "refused") {
		t.Fatalf("a closed local port reported %q, want a refusal", got.Detail)
	}
	// rendezvousReachable turns exactly this into "yes, there is a route".
	if got.Detail != "connection refused" {
		t.Errorf("detail is %q, which rendezvousReachable will not recognise as a route",
			got.Detail)
	}
}

func TestProbeTCPUnresolvableName(t *testing.T) {
	got := probeTCP("this-host-does-not-exist.invalid", 29500)
	if got.Reachable {
		t.Fatal("an invalid hostname reported as reachable")
	}
	if got.Detail == "" {
		t.Error("a failure with no explanation is not actionable")
	}
}

// TestProbeTCPUnroutableAddressTimesOut: a filtered port fails by silence,
// which is exactly the home-network case, and it must not hang.
func TestProbeTCPUnroutableAddressTimesOut(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for a timeout")
	}
	// 198.51.100.0/24 is reserved for documentation and routes nowhere.
	got := probeTCP("198.51.100.1", 29500)
	if got.Reachable {
		t.Error("an unroutable address reported as reachable")
	}
	if got.Detail == "" {
		t.Error("a timeout produced no explanation")
	}
}
