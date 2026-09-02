package api

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// reachRequest asks a machine whether it can open a plain TCP connection
// somewhere.
type reachRequest struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
}

type reachResult struct {
	Reachable bool   `json:"reachable"`
	Detail    string `json:"detail,omitempty"`
}

// acceptReachCheck answers whether this machine can reach an address.
//
// This exists because distributed training does not use the mesh. Ranks talk to
// each other over raw TCP that torch and NCCL open themselves, in their own
// processes: rank 1 must genuinely be able to connect to rank 0's rendezvous
// port, on a real network interface. Whether it can is a fact only rank 1 can establish, so it is
// asked.
func (s *Server) acceptReachCheck(w http.ResponseWriter, r *http.Request) {
	var body reachRequest
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if body.Address == "" || body.Port <= 0 {
		fail(w, 400, "No address to check.")
		return
	}
	writeJSON(w, 200, probeTCP(body.Address, body.Port))
}

// probeTCP tries to open a connection, and closes it immediately.
//
// The timeout is short on purpose. This runs while somebody waits for a
// "can I start?" answer, and a filtered port fails by silence — so a generous
// timeout would just move the ten-minute hang earlier in the flow.
func probeTCP(address string, port int) reachResult {
	target := net.JoinHostPort(address, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		return reachResult{Reachable: false, Detail: friendlyDialError(err)}
	}
	_ = conn.Close()
	return reachResult{Reachable: true}
}

// friendlyDialError says what a failed dial means in the terms that matter
// here, since the raw message names syscalls.
func friendlyDialError(err error) string {
	var netErr net.Error
	switch {
	case err == nil:
		return ""
	case errorsAs(err, &netErr) && netErr.Timeout():
		return "no answer — the port is filtered or unreachable from here"
	}
	message := err.Error()
	switch {
	case contains(message, "connection refused"):
		// Something answered, which means the route works. For a rendezvous
		// port nothing is listening on yet, that is the expected good case.
		return "connection refused"
	case contains(message, "no such host"):
		return "that name does not resolve from here"
	}
	return message
}

// rendezvousReachable reports whether every rank can reach the coordinating
// rank's port.
//
// "Connection refused" counts as reachable: the rendezvous port has nothing
// listening until the run starts, so a refusal proves the route exists, which
// is the thing being tested.
func (s *Server) rendezvousReachable(networkID, nodeID, address string, port int) (bool, string) {
	client, err := s.clientForNode(networkID, nodeID)
	if err != nil {
		return false, err.Error()
	}
	var result reachResult
	if err := client.JSON("POST", "/mesh/v1/reach",
		reachRequest{Address: address, Port: port}, &result, true); err != nil {
		return false, fmt.Sprintf("could not ask it: %s", err)
	}
	if result.Reachable || result.Detail == "connection refused" {
		return true, ""
	}
	return false, result.Detail
}

func errorsAs(err error, target *net.Error) bool { return errors.As(err, target) }

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }
