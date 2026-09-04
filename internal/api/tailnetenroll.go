package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/accountclient"
	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/tailnet"
)

// ensureManagedTailnet joins the device to PlainShow's Headscale control plane.
// The caller supplies only its already-authenticated PlainShow session; the
// one-time transport credential is minted and consumed without entering the UI
// or being written to disk.
func (s *Server) ensureManagedTailnet(ctx context.Context, client *accountclient.Client,
	token string, force bool) (bool, error) {
	s.tailnetMu.Lock()
	defer s.tailnetMu.Unlock()

	marker, err := config.LoadTailnetServer(s.layout)
	if err != nil {
		return false, err
	}
	status := tailnet.Probe(ctx)
	if marker != "" && status.Running && !force {
		return true, nil
	}
	if !force && time.Now().Before(s.tailnetRetry) {
		return false, nil
	}
	s.tailnetRetry = time.Now().Add(5 * time.Minute)

	enrollment, err := client.TailnetEnrollment(ctx, token)
	if err != nil {
		return false, fmt.Errorf("request private-network enrollment: %w", err)
	}
	if strings.TrimSpace(enrollment.LoginServer) == "" || strings.TrimSpace(enrollment.AuthKey) == "" {
		return false, fmt.Errorf("the PlainShow account server returned incomplete private-network enrollment")
	}
	if err := tailnet.Up(ctx, enrollment.AuthKey, s.cfg.Node.Name, enrollment.LoginServer); err != nil {
		return false, fmt.Errorf("connect the PlainShow private network: %w", err)
	}
	if err := config.SaveTailnetServer(s.layout, enrollment.LoginServer); err != nil {
		return false, err
	}
	s.tailnetRetry = time.Time{}
	return true, nil
}
